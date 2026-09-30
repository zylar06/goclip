package media

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

func TestPreviewPlanNoOverlaysAndPreservesTime(t *testing.T) {
	tools := New(Config{})
	for _, audio := range []bool{false, true} {
		plan := tools.planPreview("source spaces' 中文.mp4", Info{Duration: 12.345, Width: 1921, Height: 1081, HasAudio: audio})
		if plan.duration != 12.345 || plan.width != 1920 || plan.height%2 != 0 || plan.audio != audio {
			t.Fatalf("invalid preview plan: %+v", plan)
		}
		for _, flag := range []string{"-copyts", "-sn", "-dn"} {
			if !hasArg(plan.args, flag) {
				t.Fatalf("missing %s", flag)
			}
		}
		for _, pair := range [][2]string{
			{"-fps_mode", "passthrough"}, {"-enc_time_base:v", "1:1000000"}, {"-c:v", "libx264"},
			{"-pix_fmt", "yuv420p"}, {"-movflags", "+faststart"},
		} {
			if valueAfter(plan.args, pair[0]) != pair[1] {
				t.Fatalf("missing %v in %v", pair, plan.args)
			}
		}
		for _, arg := range plan.args {
			if strings.Contains(arg, "title") || strings.Contains(arg, "captions") || strings.Contains(arg, "setpts") ||
				strings.Contains(arg, "fps=") || arg == "-filter_complex" || arg == "-start_at_zero" || arg == "-t" {
				t.Fatalf("preview has editing transformation: %s", arg)
			}
		}
		if audio && valueAfter(plan.args, "-c:a") != "aac" || !audio && !hasArg(plan.args, "-an") {
			t.Fatal("preview audio contract violated")
		}
	}
}

func TestPreviewPublicationAndFailureCleanup(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "bad-render", "bad-codec", "preview-fail", "callback-0", "callback-99", "callback-100", "cancel-99"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if strings.HasPrefix(mode, "callback-") || mode == "cancel-99" {
				nativeMode = "normal"
			}
			t.Setenv("AUTOCLIP_MEDIA_FAKE", nativeMode)
			out := t.TempDir()
			sentinel := errors.New("callback refused")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var last float64
			_, err := tools.CompatiblePreview(ctx, source, out, "preview.mp4", func(stage string, p *float64) error {
				if p == nil || *p < last || *p > 100 || !strings.HasPrefix(stage, "preview") {
					t.Fatalf("invalid progress: %s %v after %v", stage, p, last)
				}
				last = *p
				if *p < 100 {
					if _, e := os.Stat(filepath.Join(out, "preview.mp4")); !errors.Is(e, os.ErrNotExist) {
						t.Fatalf("output published before validation: %v", e)
					}
				}
				if mode == "cancel-99" && *p == 99 {
					cancel()
				}
				if mode == "callback-0" && *p == 0 || mode == "callback-99" && *p == 99 || mode == "callback-100" && *p == 100 {
					return sentinel
				}
				return nil
			})
			if mode == "normal" {
				if err != nil || last != 100 {
					t.Fatalf("preview: %v progress=%v", err, last)
				}
				entries, e := os.ReadDir(out)
				if e != nil || len(entries) != 1 || entries[0].Name() != "preview.mp4" {
					t.Fatalf("scratch leaked: %v %v", entries, e)
				}
			} else {
				if err == nil {
					t.Fatalf("%s unexpectedly succeeded", mode)
				}
				if strings.HasPrefix(mode, "callback-") && !errors.Is(err, sentinel) {
					t.Fatalf("lost callback error: %v", err)
				}
				if mode == "cancel-99" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				assertEmptyDirectory(t, out)
			}
		})
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(before) {
		t.Fatal("preview modified source")
	}
}

func TestPreviewTimeout(t *testing.T) {
	tools, source, _ := fakeTools(t, "preview-wait")
	out := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := tools.CompatiblePreview(ctx, source, out, "preview.mp4", nil); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 8*time.Second {
		t.Fatalf("preview timeout lost: %v", err)
	}
	assertEmptyDirectory(t, out)
}

func TestMP4CodecValidationRejectsMalformedOutputs(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	for _, raw := range []string{
		"invalid JSON",
		`{"format":{"format_name":"matroska"},"streams":[]}`,
		`{"format":{"format_name":"mp4"},"streams":[]}`,
		`{"format":{"format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv444p"}]}`,
		`{"format":{"format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"},{"codec_type":"audio","codec_name":"mp3"}]}`,
		`{"format":{"format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"},{"codec_type":"subtitle","codec_name":"mov_text"}]}`,
		`{"format":{"format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"}]}`,
	} {
		t.Setenv("AUTOCLIP_MEDIA_CODEC_JSON", raw)
		if err := tools.validateMP4Encoding(context.Background(), source, true); err == nil {
			t.Fatalf("accepted bad codec metadata: %s", raw)
		}
	}
	t.Setenv("AUTOCLIP_MEDIA_CODEC_JSON", `{"format":{"format_name":"mp4"},"streams":[{"codec_type":"video","codec_name":"h264","pix_fmt":"yuv420p"}]}`)
	if err := tools.validateMP4Encoding(context.Background(), source, false); err != nil {
		t.Fatalf("valid silent MP4 rejected: %v", err)
	}
}

func TestPreviewRejectsTargetsAndLimits(t *testing.T) {
	tools, source, dir := fakeTools(t, "normal")
	for _, target := range []string{source, "../escape.mp4", "nested/result.mp4", "preview.webm", ""} {
		if _, err := tools.CompatiblePreview(context.Background(), source, dir, target, nil); err == nil {
			t.Fatalf("accepted target %s", target)
		}
	}
	tools.cfg.MaxBytes = 12 // Source is 10 bytes, fake rendered output is 19.
	out := t.TempDir()
	if _, err := tools.CompatiblePreview(context.Background(), source, out, "large.mp4", nil); err == nil {
		t.Fatal("output byte limit ignored")
	}
	assertEmptyDirectory(t, out)
	tools.cfg.MaxBytes, tools.cfg.MaxDuration = 100, .5
	if _, err := tools.CompatiblePreview(context.Background(), source, out, "long.mp4", nil); err == nil {
		t.Fatal("source duration limit ignored")
	}
	assertEmptyDirectory(t, out)
}

func TestRenderSubtitlesDisabledIgnoresCues(t *testing.T) {
	tools, source, dir := fakeTools(t, "normal")
	log := filepath.Join(dir, "argv.jsonl")
	t.Setenv("AUTOCLIP_MEDIA_ARGV", log)
	draft := testDraft()
	draft.Subtitles = false
	invalid := []domain.Cue{{Start: math.NaN(), End: -1, Text: "\xff"}}
	if _, err := tools.Render(context.Background(), source, t.TempDir(), "off.mp4", draft, invalid, nil); err != nil {
		t.Fatalf("disabled subtitles depend on cues: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "captions.ass") || strings.Contains(string(data), "fontsdir") {
		t.Fatal("disabled subtitles entered native graph")
	}
	draft.Subtitles = true
	if _, err := tools.Render(context.Background(), source, t.TempDir(), "on.mp4", draft, invalid, nil); err == nil {
		t.Fatal("enabled subtitles accepted malformed cues")
	}
}

func TestRenderSourceEndMillisecondTolerance(t *testing.T) {
	tools, source, _ := fakeTools(t, "normal")
	for _, end := range []float64{1, 1.0009, 1.001, 1.0011, 1.049} {
		d := testDraft()
		d.Scenes[0].End = end
		_, err := tools.Render(context.Background(), source, t.TempDir(), "result.mp4", d, nil, nil)
		if (err == nil) != (end <= 1.001) {
			t.Errorf("source end %.6f: %v", end, err)
		}
	}
}
