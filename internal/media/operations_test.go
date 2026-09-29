package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
)

func TestProbeLimitsJSONAndRotation(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	info, err := tools.Probe(context.Background(), source)
	if err != nil || info.Duration != 1 || info.Width != 320 || !info.HasAudio {
		t.Fatalf("probe: %+v %v", info, err)
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "rotated")
	info, err = tools.Probe(context.Background(), source)
	if err != nil || info.Width != 180 || info.Height != 320 {
		t.Fatalf("rotation: %+v %v", info, err)
	}
	for _, mode := range []string{"bad-probe", "nan-probe"} {
		t.Setenv("AUTOCLIP_MEDIA_FAKE", mode)
		if _, err = tools.Probe(context.Background(), source); err == nil {
			t.Errorf("accepted %s", mode)
		}
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "normal")
	tools.cfg.MaxDuration = .5
	if _, err = tools.Probe(context.Background(), source); err == nil {
		t.Fatal("duration limit ignored")
	}
	tools.cfg.MaxDuration, tools.cfg.MaxBytes = 1, 1
	if _, err = tools.Probe(context.Background(), source); err == nil {
		t.Fatal("byte limit ignored")
	}
}

func TestDownloadOutputCookiesAndNoSubtitle(t *testing.T) {
	tools, _, dir := fakeTools(t, "normal")
	cookies := filepath.Join(dir, "original-cookies.txt")
	cookieData := []byte("# Netscape HTTP Cookie File\n")
	if err := os.WriteFile(cookies, cookieData, 0600); err != nil {
		t.Fatal(err)
	}
	video, subtitle, err := tools.Download(context.Background(), "https://youtu.be/dQw4w9WgXcQ", dir, cookies, nil)
	if err != nil || subtitle != "" || filepath.Base(video) != "source.mp4" {
		t.Fatalf("download: %q %q %v", video, subtitle, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(video), "cookies.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("private cookie copy leaked")
	}
	data, err := os.ReadFile(cookies)
	if err != nil || string(data) != string(cookieData) {
		t.Fatal("caller cookie file modified")
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "download-subtitles")
	_, subtitle, err = tools.Download(context.Background(), "https://youtu.be/dQw4w9WgXcQ", dir, "", nil)
	if err != nil || !strings.HasSuffix(subtitle, "source.en.srt") {
		t.Fatalf("subtitle discovery: %q %v", subtitle, err)
	}
}

func TestDownloadFailureCleanupAndValidation(t *testing.T) {
	tools, _, _ := fakeTools(t, "filtered-download")
	dir := t.TempDir()
	if _, _, err := tools.Download(context.Background(), "https://youtu.be/dQw4w9WgXcQ", dir, "", nil); err == nil {
		t.Fatal("filtered download must not look successful")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed download left partial files")
	}
	if _, _, err := tools.Download(context.Background(), "https://evil.test", dir, "", nil); err == nil {
		t.Fatal("invalid source accepted")
	}
	t.Setenv("AUTOCLIP_MEDIA_FAKE", "download-limit")
	tools.cfg.MaxBytes = 100
	if _, _, err := tools.Download(context.Background(), "https://youtu.be/dQw4w9WgXcQ", dir, "", nil); err == nil {
		t.Fatal("download byte limit ignored")
	}
}

func TestTranscribeSilenceEmptyAndErrors(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		want   int
		hasErr bool
	}{
		{"normal", 1, false}, {"silence", 0, false}, {"empty-srt", 0, false},
		{"missing-srt", 0, true}, {"bad-srt", 0, true}, {"overrun-srt", 0, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			tools, source, dir := fakeTools(t, tc.mode)
			log := filepath.Join(dir, "argv.jsonl")
			t.Setenv("AUTOCLIP_MEDIA_ARGV", log)
			out := t.TempDir()
			if tc.mode == "silence" {
				tools.cfg.Model, tools.cfg.Whisper = "", "must-not-be-invoked"
			}
			cues, err := tools.Transcribe(context.Background(), source, out, nil)
			if (err != nil) != tc.hasErr || len(cues) != tc.want || (!tc.hasErr && cues == nil) {
				t.Fatalf("transcribe %s: %+v %v", tc.mode, cues, err)
			}
			calls, e := os.ReadFile(log)
			if e != nil {
				t.Fatal(e)
			}
			if tc.mode == "silence" && strings.Contains(string(calls), "--output-srt") {
				t.Fatal("silent audio invoked ASR; may hallucinate")
			}
			if tc.mode == "normal" && (!strings.Contains(string(calls), "--print-progress") ||
				!strings.Contains(string(calls), "16000") || !strings.Contains(string(calls), "--language")) {
				t.Fatal("missing ASR/extraction argv")
			}
			entries, e := os.ReadDir(out)
			if e != nil || len(entries) != 0 {
				t.Fatal("transcription scratch files not cleaned")
			}
		})
	}
}

func TestWAVSilenceAndMalformedPCM(t *testing.T) {
	for _, sample := range []int16{0, 32, -32, 33, -33, 32767, -32768} {
		path := filepath.Join(t.TempDir(), "audio.wav")
		if err := os.WriteFile(path, testWAV(sample), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := wavHasSignal(path)
		if err != nil || got != (sample > 32 || sample < -32) {
			t.Fatalf("sample=%d signal=%t err=%v", sample, got, err)
		}
	}
	path := filepath.Join(t.TempDir(), "truncated.wav")
	if err := os.WriteFile(path, testWAV(0)[:100], 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := wavHasSignal(path); err == nil {
		t.Fatal("truncated PCM accepted as silence")
	}
}

func TestSampleAndCancellation(t *testing.T) {
	tools, source, dir := fakeTools(t, "normal")
	frames, err := tools.Sample(context.Background(), source, dir, 0, nil)
	if err != nil || len(frames) != 1 || frames[0].Time != 0 {
		t.Fatalf("sample: %+v %v", frames, err)
	}
	if _, err := os.Stat(frames[0].Path); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("cancel progress")
	out := t.TempDir()
	if _, err := tools.Sample(context.Background(), source, out, 0, func(string, *float64) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("sample callback error: %v", err)
	}
	if _, err := tools.Sample(context.Background(), source, out, 2, nil); err == nil {
		t.Fatal("out-of-range sampling accepted")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatal("canceled sample left files")
	}
}

func TestRenderPublicationAndFailureCleanup(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	for _, mode := range []string{"normal", "bad-render", "callback-99", "callback-100"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if strings.HasPrefix(mode, "callback") {
				nativeMode = "normal"
			}
			t.Setenv("AUTOCLIP_MEDIA_FAKE", nativeMode)
			out := t.TempDir()
			var fn domain.ProgressFunc
			if strings.HasPrefix(mode, "callback") {
				fn = func(_ string, p *float64) error {
					if p != nil && ((mode == "callback-99" && *p == 99) || (mode == "callback-100" && *p == 100)) {
						return errors.New("callback rejected")
					}
					return nil
				}
			}
			_, err := tools.Render(context.Background(), source, out, "result.mp4", testDraft(), nil, fn)
			if (err != nil) != (mode != "normal") {
				t.Fatalf("render %s: %v", mode, err)
			}
			entries, e := os.ReadDir(out)
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if mode == "normal" {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("render leaked partial/failure output: %v", entries)
			}
		})
	}
	out := t.TempDir()
	target := filepath.Join(out, "existing.mp4")
	if err := os.WriteFile(target, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Render(context.Background(), source, out, target, testDraft(), nil, nil); err == nil {
		t.Fatal("existing output overwritten")
	}
	if _, err := outputPath(out, "../escape.mp4"); err == nil {
		t.Fatal("output traversal accepted")
	}
}
