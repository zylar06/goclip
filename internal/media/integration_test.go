package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

func integrationTools(t *testing.T) *Tools {
	t.Helper()
	find := func(env, name string) string {
		if value := os.Getenv(env); value != "" {
			return value
		}
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
		if runtime.GOOS == "windows" {
			path := filepath.Join(os.Getenv("LOCALAPPDATA"), "AutoClip Desktop", "resources", "ffmpeg", name+".exe")
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
				return path
			}
		}
		return ""
	}
	ffmpeg, ffprobe := find("MEDIA_FFMPEG", "ffmpeg"), find("MEDIA_FFPROBE", "ffprobe")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("FFmpeg/ffprobe not available; set MEDIA_FFMPEG and MEDIA_FFPROBE")
	}
	t.Logf("native tools: %s; %s", ffmpeg, ffprobe)
	return New(Config{FFmpeg: ffmpeg, FFprobe: ffprobe, Whisper: os.Getenv("MEDIA_WHISPER"), Model: os.Getenv("MEDIA_WHISPER_MODEL")})
}

// Fixture: red for 3 seconds, blue for 3 seconds, with an optional audio stream.
func generateSource(t *testing.T, tools *Tools, audio string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source spaces' 中文.mp4")
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-filter_complex_threads", "2",
		"-f", "lavfi", "-i", "color=c=red:s=320x180:r=30:d=3",
		"-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=3"}
	if audio != "none" {
		input := "sine=frequency=440:sample_rate=48000:duration=6"
		if audio == "silence" {
			input = "anullsrc=r=48000:cl=mono"
		}
		args = append(args, "-f", "lavfi", "-i", input)
	}
	args = append(args, "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]")
	if audio != "none" {
		args = append(args, "-map", "2:a:0", "-c:a", "aac")
	}
	args = append(args, "-t", "6", "-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", path)
	if _, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, args: args, timeout: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	return path
}

func nativeFrame(t *testing.T, tools *Tools, video string, at float64) image.Image {
	t.Helper()
	data, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 30 * time.Second, stdoutLimit: 12 << 20,
		args: []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-ss", number(at), "-i", video,
			"-frames:v", "1", "-threads", "2", "-c:v", "png", "-f", "image2pipe", "pipe:1"}})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestIntegrationChineseSubtitleFont(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fonts"), 0700); err != nil {
		t.Fatal(err)
	}
	font, err := os.ReadFile(filepath.Join(tools.cfg.FontDir, "NotoSansSC-StaticBold.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fonts", "NotoSansSC-StaticBold.ttf"), font, 0600); err != nil {
		t.Fatal(err)
	}
	cues := []domain.Cue{{Start: 0, End: 1, Text: strings.Repeat("中文測試字幕 简体繁體", 5)}}
	captions, err := tools.subtitleASS(cues, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(captions, []byte(`\N`)) {
		t.Fatal("long Chinese subtitle must wrap before reaching libass")
	}
	if err := os.WriteFile(filepath.Join(dir, "captions.ass"), captions, 0600); err != nil {
		t.Fatal(err)
	}
	var diagnostic strings.Builder
	_, err = run(context.Background(), command{
		exe: tools.cfg.FFmpeg, dir: dir, timeout: 30 * time.Second,
		args: []string{"-hide_banner", "-nostdin", "-loglevel", "info",
			"-f", "lavfi", "-i", "color=black:s=640x360:d=1",
			"-vf", "ass=filename=captions.ass:fontsdir=fonts",
			"-frames:v", "1", "-threads", "2", "-c:v", "png", "-f", "image2", "subtitle.png"},
		line: func(line string) error {
			diagnostic.WriteString(line + "\n")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := diagnostic.String()
	if !strings.Contains(log, "fontselect:") || !strings.Contains(log, "NotoSansSC-Thin") ||
		strings.Contains(log, "Glyph ") || strings.Contains(log, "failed to find") {
		t.Fatalf("Chinese subtitles must use the bundled CJK font without missing-glyph fallback: %s", log)
	}
	pixels, err := os.ReadFile(filepath.Join(dir, "subtitle.png"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pixels))
	if err != nil {
		t.Fatal(err)
	}
	visible := 0
	for y := 0; y < 360; y++ {
		for x := 0; x < 640; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 45000 && g > 45000 && b > 45000 {
				visible++
				if x < 640/30 || x >= 640-640/30 || y < 360/30 || y >= 360-360/30 {
					t.Fatalf("Chinese subtitle overflows the frame margins at %d,%d", x, y)
				}
			}
		}
	}
	if visible < 200 {
		t.Fatalf("Chinese subtitle image is empty or truncated: %d bright pixels", visible)
	}
}

func TestIntegrationRenderConcatSubtitlesTitleTiming(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "tone")
	info, err := tools.Probe(context.Background(), source)
	if err != nil || math.Abs(info.Duration-6) > .1 || info.Width != 320 || !info.HasAudio {
		t.Fatalf("source probe: %+v %v", info, err)
	}
	d := domain.NewDraft("Opening title", []domain.Scene{
		{ID: "blue", Start: 3, End: 6}, {ID: "red", Start: 0, End: 2},
	})
	d.TitleStyle, d.TitleMotion = "card", false
	d.Subtitles = true // Test burning explicitly; new drafts default to subtitles off.
	cues := []domain.Cue{{Start: 3.1, End: 3.7, Text: "Blue subtitle"}, {Start: .2, End: .6, Text: "Red subtitle"}}
	out := filepath.Join(t.TempDir(), "export spaces' 中文")
	var progress []float64
	result, err := tools.Render(context.Background(), source, out, "final.mp4", d, cues, func(_ string, p *float64) error {
		if p != nil {
			progress = append(progress, *p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 320 || result.Height != 180 || !result.HasAudio || math.Abs(result.Duration-5) > .12 {
		t.Fatalf("render info: %+v", result)
	}
	for i, p := range progress {
		if p < 0 || p > 100 || (i > 0 && p < progress[i-1]) {
			t.Fatalf("non-monotonic render progress: %v", progress)
		}
	}
	if len(progress) == 0 || progress[len(progress)-1] != 100 {
		t.Fatalf("missing publication progress: %v", progress)
	}
	final := filepath.Join(out, "final.mp4")
	early, noSubtitle, late := nativeFrame(t, tools, final, .4), nativeFrame(t, tools, final, .9), nativeFrame(t, tools, final, 4.5)
	r, _, b, _ := early.At(3, 3).RGBA()
	if b < 50000 || r > 10000 {
		t.Fatal("first concatenated scene is not blue")
	}
	r, _, b, _ = late.At(3, 3).RGBA()
	if r < 50000 || b > 10000 {
		t.Fatal("reordered final scene is not red")
	}
	whiteBottom := func(img image.Image) int {
		count := 0
		for y := 125; y < 175; y++ {
			for x := 20; x < 300; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r > 45000 && g > 45000 && b > 45000 {
					count++
				}
			}
		}
		return count
	}
	if whiteBottom(early) < 10 || whiteBottom(noSubtitle) > 3 {
		t.Fatalf("subtitle timeline wrong: visible=%d absent=%d", whiteBottom(early), whiteBottom(noSubtitle))
	}
	nonBackground := func(img image.Image, blue bool) int {
		count := 0
		for y := 10; y < 110; y++ {
			for x := 20; x < 300; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				background := r > 50000 && g < 10000 && b < 10000
				if blue {
					background = b > 50000 && r < 10000 && g < 10000
				}
				if !background {
					count++
				}
			}
		}
		return count
	}
	if nonBackground(early, true) < 100 || nonBackground(late, false) > 10 {
		t.Fatal("title must be visible initially and completely absent after four seconds")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 1 || entries[0].Name() != "final.mp4" {
		t.Fatalf("render scratch cleanup failed: %v %v", entries, err)
	}
}

func TestIntegrationLayoutsAudioAndFrames(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "none")
	for _, tc := range []struct {
		layout, aspect, style string
		w, h                  int
	}{
		{"fit", "original", "plain", 320, 180},
		{"crop", "portrait", "comic", 1080, 1920},
		{"blur", "landscape", "neon", 1920, 1080},
	} {
		t.Run(tc.layout, func(t *testing.T) {
			d := domain.NewDraft("Go 原生", []domain.Scene{{ID: "s1", Start: 1.117, End: 1.617}})
			d.Layout, d.Aspect, d.TitleStyle = tc.layout, tc.aspect, tc.style
			out := t.TempDir()
			result, err := tools.Render(context.Background(), source, out, "result.mp4", d, nil, nil)
			if err != nil || result.Width != tc.w || result.Height != tc.h || result.HasAudio || math.Abs(result.Duration-.5) > .12 {
				t.Fatalf("layout %s: %+v %v", tc.layout, result, err)
			}
		})
	}
	frames, err := tools.Sample(context.Background(), source, t.TempDir(), 6, nil)
	if err != nil || len(frames) != 3 || frames[0].Time != 0 || frames[1].Time != 2 || frames[2].Time != 4 {
		t.Fatalf("frames: %+v %v", frames, err)
	}
	f, err := os.Open(frames[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	img, _, decodeErr := image.Decode(f)
	if err := errors.Join(decodeErr, f.Close()); err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 {
		t.Fatalf("bad sampled image: %v", img.Bounds())
	}
	if _, err := tools.Transcribe(context.Background(), source, t.TempDir(), nil); err == nil {
		t.Fatal("no-audio ASR must report an explicit error")
	}
}

func TestIntegrationSilentAudioNeverInvokesWhisper(t *testing.T) {
	tools := integrationTools(t)
	tools.cfg.Whisper, tools.cfg.Model = "absent-whisper-must-not-run", ""
	source := generateSource(t, tools, "silence")
	out := t.TempDir()
	cues, err := tools.Transcribe(context.Background(), source, out, nil)
	if err != nil || cues == nil || len(cues) != 0 {
		t.Fatalf("valid silent audio should return [], not a fake transcript: %+v %v", cues, err)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatal("silent ASR left scratch output")
	}
}

func TestIntegrationRenderDropsOriginalAudio(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "tone")
	d := testDraft()
	d.OriginalAudio = false
	result, err := tools.Render(context.Background(), source, t.TempDir(), "muted.mp4", d, nil, nil)
	if err != nil || result.HasAudio {
		t.Fatalf("original_audio=false ignored: %+v %v", result, err)
	}
}

func TestIntegrationWhisperOptional(t *testing.T) {
	if os.Getenv("MEDIA_WHISPER") == "" || os.Getenv("MEDIA_WHISPER_MODEL") == "" || os.Getenv("MEDIA_WHISPER_FIXTURE") == "" {
		t.Skip("real ASR requires MEDIA_WHISPER, MEDIA_WHISPER_MODEL and MEDIA_WHISPER_FIXTURE (speech video)")
	}
	tools := integrationTools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cues, err := tools.Transcribe(ctx, os.Getenv("MEDIA_WHISPER_FIXTURE"), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) == 0 || strings.TrimSpace(cues[0].Text) == "" {
		t.Fatal("provided speech fixture produced no transcript")
	}
	var text []string
	for _, cue := range cues {
		text = append(text, cue.Text)
	}
	transcript := strings.Join(text, " ")
	t.Logf("local transcript: %s", transcript)
	if expected := os.Getenv("MEDIA_WHISPER_EXPECT"); expected != "" &&
		!strings.Contains(strings.ToLower(transcript), strings.ToLower(expected)) {
		t.Fatalf("transcript did not contain expected phrase %q", expected)
	}
}
