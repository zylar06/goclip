package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

func TestDownloadWithSubtitlesNativePolicy(t *testing.T) {
	for _, mode := range []string{"normal", "download-subtitles", "download-empty-subtitles",
		"download-invalid-subtitles", "download-unreadable-subtitles", "download-oversized-subtitles", "download-convert-failure"} {
		for _, policy := range []string{"legacy", "enabled", "disabled"} {
			t.Run(mode+"/"+policy, func(t *testing.T) {
				tools, _, scratch := fakeTools(t, mode)
				log := filepath.Join(scratch, "argv.jsonl")
				t.Setenv("AUTOCLIP_MEDIA_ARGV", log)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				out := t.TempDir()
				complete := false
				progress := func(stage string, percent *float64) error {
					if stage == "download" && percent != nil && *percent == 100 {
						complete = true
					}
					return nil
				}
				var video, subtitle string
				var err error
				const raw = "https://www.bilibili.com/video/BV1xx411c7mD"
				if policy == "legacy" {
					video, subtitle, err = tools.Download(ctx, raw, out, "", progress)
				} else {
					video, subtitle, err = tools.DownloadWithSubtitles(ctx, raw, out, "", policy == "enabled", progress)
				}
				wantError := policy != "disabled" && (strings.Contains(mode, "invalid") ||
					strings.Contains(mode, "unreadable") || strings.Contains(mode, "oversized") || strings.Contains(mode, "failure"))
				if (err != nil) != wantError {
					t.Fatalf("policy=%s mode=%s video=%q subtitle=%q err=%v", policy, mode, video, subtitle, err)
				}
				if complete == wantError {
					t.Fatal("completion reported on failure or missing on success")
				}
				if wantError {
					entries, e := os.ReadDir(out)
					if e != nil || len(entries) != 0 || video != "" || subtitle != "" {
						t.Fatalf("failed native operation leaked output: %v %v", entries, e)
					}
					if mode == "download-invalid-subtitles" && !strings.Contains(err.Error(), "download subtitles") {
						t.Fatal("subtitle validation failure was obscured", err)
					}
					if mode == "download-convert-failure" {
						var native *ToolError
						if !errors.As(err, &native) || !strings.Contains(err.Error(), "platform subtitle conversion failed") {
							t.Fatal("native subtitle conversion failure swallowed", err)
						}
					}
				} else {
					if _, e := os.Stat(video); e != nil {
						t.Fatal("successful download missing video", e)
					}
					wantSubtitle := policy != "disabled" && mode == "download-subtitles"
					if (subtitle != "") != wantSubtitle {
						t.Fatalf("unexpected selected platform subtitle: %q", subtitle)
					}
				}
				data, e := os.ReadFile(log)
				if e != nil {
					t.Fatal(e)
				}
				var args []string
				if e := json.Unmarshal(bytes.Split(data, []byte("\n"))[0], &args); e != nil {
					t.Fatal(e)
				}
				if !hasArg(args, "--ignore-config") || hasArg(args, "--convert-subs") != (policy != "disabled") ||
					hasArg(args, "--write-subs") != (policy != "disabled") || hasArg(args, "--write-auto-subs") != (policy != "disabled") {
					t.Fatalf("actual subprocess received wrong subtitle policy: %q", args)
				}
			})
		}
	}
}

func TestDownloadWithoutSubtitlesPreservesSafetyFailures(t *testing.T) {
	for _, mode := range []string{"filtered-download", "download-limit", "bad-probe", "fail", "callback"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if mode == "callback" {
				nativeMode = "normal"
			}
			tools, _, _ := fakeTools(t, nativeMode)
			tools.cfg.MaxBytes = 100
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out := t.TempDir()
			sentinel := errors.New("completion rejected")
			var progress domain.ProgressFunc
			if mode == "callback" {
				progress = func(stage string, percent *float64) error {
					if stage == "download" && percent != nil && *percent == 100 {
						return sentinel
					}
					return nil
				}
			}
			video, subtitle, err := tools.DownloadWithSubtitles(ctx, "https://youtu.be/dQw4w9WgXcQ", out, "", false, progress)
			if err == nil || video != "" || subtitle != "" {
				t.Fatal("subtitle opt-out swallowed unrelated failure", err)
			}
			if mode == "callback" && !errors.Is(err, sentinel) {
				t.Fatal("lost progress callback error", err)
			}
			entries, e := os.ReadDir(out)
			if e != nil || len(entries) != 0 {
				t.Fatalf("failed download leaked scratch files: %v %v", entries, e)
			}
		})
	}
}

func TestIntegrationDownloadWithoutPlatformSubtitles(t *testing.T) {
	native := integrationTools(t)
	source := generateSource(t, native, "tone")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	// Real native test-binary downloader, real ffprobe validation and actual
	// FFmpeg fixture: no network/platform availability is involved.
	tools, _, _ := fakeTools(t, "download-invalid-subtitles")
	tools.cfg.FFmpeg, tools.cfg.FFprobe = native.cfg.FFmpeg, native.cfg.FFprobe
	t.Setenv("AUTOCLIP_MEDIA_DOWNLOAD_FIXTURE", source)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	video, subtitle, err := tools.DownloadWithSubtitles(ctx, "https://youtu.be/dQw4w9WgXcQ", t.TempDir(), "", false, nil)
	if err != nil || subtitle != "" {
		t.Fatalf("unrelated invalid SRT blocked real media: %q %v", subtitle, err)
	}
	after, err := os.ReadFile(video)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("downloaded fixture bytes changed", err)
	}
	info, err := native.Probe(ctx, video)
	if err != nil || !info.HasAudio || info.Duration != 6 {
		t.Fatalf("real audio/video validation not retained: %+v %v", info, err)
	}
	sidecar, err := os.ReadFile(filepath.Join(filepath.Dir(video), "source.ai-zh.srt"))
	if err != nil || string(sidecar) != "invalid unrelated platform SRT" {
		t.Fatal("unrelated sidecar was modified", err)
	}
	if _, _, err := tools.DownloadWithSubtitles(ctx, "https://youtu.be/dQw4w9WgXcQ", t.TempDir(), "", true, nil); err == nil ||
		!strings.Contains(err.Error(), "download subtitles") {
		t.Fatal("enabled policy did not reject the same invalid sidecar", err)
	}
}
