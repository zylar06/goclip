package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOverallSampleTimesParity(t *testing.T) {
	for _, tc := range []struct {
		duration float64
		count    int
		last     float64
	}{
		{.04, 1, 0}, {1, 1, 0}, {2.05, 1, 0}, {2.101, 2, 2},
		{6, 3, 4}, {119, 60, 118}, {120, 60, 118},
		{121, 60, 118.983}, {299.84, 60, 294.843}, {7200, 60, 7080},
	} {
		times := overallSampleTimes(tc.duration)
		if len(times) != tc.count || times[len(times)-1] != tc.last {
			t.Errorf("duration=%v: got %d times, last=%v", tc.duration, len(times), times[len(times)-1])
		}
		for i, at := range times {
			if at != math.Round(at*1000)/1000 || at >= tc.duration || (i > 0 && at <= times[i-1]) {
				t.Fatalf("invalid overall times: %v", times)
			}
		}
	}
}

func TestEverySecondTimes(t *testing.T) {
	for _, tc := range []struct {
		duration float64
		want     []float64
	}{
		{0, []float64{0}},
		{0.4, []float64{0}},
		{1, []float64{0}},
		{2.1, []float64{0, 1, 2}},
		{60, []float64{0, 1, 2}},
	} {
		got := EverySecondTimes(tc.duration)
		if tc.duration == 60 {
			if len(got) != 60 || got[59] != 59 {
				t.Fatalf("duration=%v: got %d samples, last=%v", tc.duration, len(got), got[len(got)-1])
			}
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("duration=%v: got %v, want %v", tc.duration, got, tc.want)
		}
	}
}

func TestSampleAtExactSeeksAndOrder(t *testing.T) {
	tools, source, dir := fakeTools(t, "normal")
	log := filepath.Join(dir, "argv.jsonl")
	t.Setenv("AUTOCLIP_MEDIA_ARGV", log)
	times := []float64{.75, .1234, 0, .5}
	original := append([]float64(nil), times...)
	var progress []float64
	frames, err := tools.SampleAt(context.Background(), source, t.TempDir(), times, func(stage string, p *float64) error {
		if stage != "sample" || p == nil {
			t.Fatal("invalid sample progress")
		}
		progress = append(progress, *p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(times, original) || len(frames) != 4 || !reflect.DeepEqual(progress, []float64{0, 25, 50, 75, 100}) {
		t.Fatalf("input changed or incomplete progress: %v %v", times, progress)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var seeks []string
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var args []string
		if err = json.Unmarshal(line, &args); err != nil {
			t.Fatal(err)
		}
		if hasArg(args, "-ss") {
			seeks = append(seeks, valueAfter(args, "-ss"))
			if valueAfter(args, "-f") != "image2pipe" || !hasArg(args, "-sn") {
				t.Fatalf("unsafe frame args: %v", args)
			}
		}
	}
	if !reflect.DeepEqual(seeks, []string{"0.750000", "0.123000", "0.000000", "0.500000"}) {
		t.Fatalf("seeks differ from requested times: %v", seeks)
	}
	for i, frame := range frames {
		if frame.Time != math.Round(times[i]*1000)/1000 {
			t.Fatalf("wrong frame time: %+v", frame)
		}
		data, err := os.ReadFile(frame.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSampleAtValidationAndLimit(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	for _, times := range [][]float64{
		nil, {}, make([]float64, 61), {-.001}, {math.NaN()}, {math.Inf(1)},
		{math.MaxFloat64}, {1}, {1.0001}, {.9999}, {.1, .1}, {.1231, .1232},
	} {
		dir := t.TempDir()
		if frames, err := tools.SampleAt(context.Background(), source, dir, times, nil); err == nil || frames != nil {
			t.Fatalf("accepted invalid sample list %v: %v", times, err)
		}
		assertEmptyDirectory(t, dir)
	}
	times := make([]float64, 60)
	for i := range times {
		times[i] = float64(i) / 100
	}
	frames, err := tools.SampleAt(context.Background(), source, t.TempDir(), times, nil)
	if err != nil || len(frames) != 60 {
		t.Fatalf("60-frame request: %d %v", len(frames), err)
	}
}

func assertEmptyDirectory(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected residual output in %s: %v %v", dir, entries, err)
	}
}

func TestSampleAtCleanupAndCancel(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	sentinel := errors.New("stop after first sample")
	out := t.TempDir()
	frames, err := tools.SampleAt(context.Background(), source, out, []float64{0, .5}, func(_ string, p *float64) error {
		if p != nil && *p > 0 {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) || frames != nil {
		t.Fatalf("lost callback failure: %v", err)
	}
	assertEmptyDirectory(t, out)
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "jpeg-invalid")
	if _, err = tools.SampleAt(context.Background(), source, out, []float64{0}, nil); err == nil {
		t.Fatal("accepted broken JPEG")
	}
	assertEmptyDirectory(t, out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = tools.SampleAt(ctx, source, out, []float64{0}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	assertEmptyDirectory(t, out)
}

func TestThumbnailValidationLimitsAndDeadline(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	data, err := tools.Thumbnail(context.Background(), source, .125)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	for _, at := range []float64{-1, 1, 2, math.NaN(), math.Inf(1)} {
		if _, err = tools.Thumbnail(context.Background(), source, at); err == nil {
			t.Fatalf("accepted thumbnail time %v", at)
		}
	}
	for _, tc := range []struct{ mode, message string }{
		{"jpeg-invalid", "JPEG"}, {"jpeg-corrupt", "corrupt JPEG"}, {"jpeg-large", "capture limit"}, {"jpeg-dimensions", "dimensions"},
	} {
		t.Setenv("AUTOCLIP_MEDIA_FAKE", tc.mode)
		if data, err = tools.Thumbnail(context.Background(), source, 0); err == nil || !strings.Contains(err.Error(), tc.message) || data != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "jpeg-wait")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = tools.Thumbnail(ctx, source, 0); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 8*time.Second {
		t.Fatalf("thumbnail deadline not honored: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err = tools.Thumbnail(ctx, source, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("thumbnail cancellation: %v", err)
	}
}
