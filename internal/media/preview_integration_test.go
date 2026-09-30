package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

// A controlled fixture, not a user video: red then blue, known Chinese text
// burned at the TOP only, variable-rate MPEG4 video and PCM tone delayed 400 ms.
// The non-browser codecs force a real conversion, while the top/bottom separation
// makes a second subtitle layer objectively measurable without OCR.
func chinesePreviewFixture(t *testing.T, tools *Tools) string {
	t.Helper()
	base := generateSource(t, tools, "none")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fonts"), 0700); err != nil {
		t.Fatal(err)
	}
	font, err := os.ReadFile(filepath.Join(tools.cfg.FontDir, "NotoSansSC-StaticBold.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "fonts", "NotoSansSC-StaticBold.ttf"), font, 0600); err != nil {
		t.Fatal(err)
	}
	ass, err := tools.subtitleASS([]domain.Cue{{Start: 0, End: 6, Text: "原片中文字幕"}}, 320, 180)
	if err != nil {
		t.Fatal(err)
	}
	ass = bytes.Replace(ass, []byte(",0,2,"), []byte(",0,8,"), 1) // top-center alignment
	if err = os.WriteFile(filepath.Join(dir, "captions.ass"), ass, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "controlled 中文.mkv")
	_, err = run(context.Background(), command{exe: tools.cfg.FFmpeg, dir: dir, timeout: 30 * time.Second,
		args: []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error",
			"-i", base, "-itsoffset", "0.4", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=5.6",
			"-map", "0:v:0", "-map", "1:a:0",
			"-vf", "ass=filename=captions.ass:fontsdir=fonts,select='not(mod(n,3))+eq(mod(n,7),0)'",
			"-fps_mode", "vfr", "-c:v", "mpeg4", "-q:v", "2", "-threads", "2", "-c:a", "pcm_s16le", "-t", "6", source}})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func brightPixels(img image.Image, rect image.Rectangle) int {
	n := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 45000 && g > 45000 && b > 45000 {
				n++
			}
		}
	}
	return n
}

func nativeVideoTimes(t *testing.T, tools *Tools, source string) []float64 {
	t.Helper()
	data, err := run(context.Background(), command{exe: tools.cfg.FFprobe, timeout: 30 * time.Second, stdoutLimit: 1 << 20,
		args: []string{"-v", "error", "-select_streams", "v:0", "-show_entries", "frame=best_effort_timestamp_time", "-of", "json", source}})
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Frames []struct {
			Time string `json:"best_effort_timestamp_time"`
		}
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	times := make([]float64, len(raw.Frames))
	for i, frame := range raw.Frames {
		times[i], err = strconv.ParseFloat(frame.Time, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	return times
}

// Decode onto the source zero-based audio timeline (rather than resetting PTS).
// This exposes lost offsets as well as "has audio" files containing only silence.
func nativeTone(t *testing.T, tools *Tools, source string) (onset, frequency float64) {
	t.Helper()
	data, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 30 * time.Second, stdoutLimit: 1 << 20,
		args: []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-i", source,
			"-map", "0:a:0", "-af", "aresample=48000:async=1:first_pts=0",
			"-ac", "1", "-ar", "48000", "-c:a", "pcm_s16le", "-f", "s16le", "pipe:1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 48000*2*5 {
		t.Fatalf("audio unexpectedly short: %d bytes", len(data))
	}
	first, crossings := -1, 0
	var previous int16
	for i := 0; i+1 < len(data); i += 2 {
		value := int16(binary.LittleEndian.Uint16(data[i:]))
		sample := i / 2
		if first < 0 && (value > 400 || value < -400) {
			first = sample
		}
		if sample >= 48000 && sample < 96000 && previous <= 0 && value > 0 {
			crossings++
		}
		previous = value
	}
	if first < 0 {
		t.Fatal("audio stream is silent")
	}
	return float64(first) / 48000, float64(crossings)
}

func TestIntegrationPreviewPreservesTimelineAudioAndSingleChineseLayer(t *testing.T) {
	tools := integrationTools(t)
	source := chinesePreviewFixture(t, tools)
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	// Previews must work even when title/subtitle fonts are unavailable.
	noFonts := New(Config{FFmpeg: tools.cfg.FFmpeg, FFprobe: tools.cfg.FFprobe, FontDir: filepath.Join(out, "missing-fonts")})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	info, err := noFonts.CompatiblePreview(ctx, source, out, "preview.mp4", nil)
	if err != nil || info.Width != 320 || info.Height != 180 || !info.HasAudio || math.Abs(info.Duration-6) > .05 {
		t.Fatalf("compatible preview: %+v %v", info, err)
	}
	preview := filepath.Join(out, "preview.mp4")
	if err = tools.validateMP4Encoding(ctx, preview, true); err != nil {
		t.Fatal(err)
	}
	srcTimes, dstTimes := nativeVideoTimes(t, tools, source), nativeVideoTimes(t, tools, preview)
	if len(srcTimes) < 20 || len(srcTimes) != len(dstTimes) {
		t.Fatalf("VFR frame count changed: %d -> %d", len(srcTimes), len(dstTimes))
	}
	variable := false
	for i, at := range srcTimes {
		if math.Abs(at-dstTimes[i]) > .001 {
			t.Fatalf("frame %d shifted: %.6f -> %.6f", i, at, dstTimes[i])
		}
		if i > 1 && math.Abs((at-srcTimes[i-1])-(srcTimes[1]-srcTimes[0])) > .005 {
			variable = true
		}
	}
	if !variable {
		t.Fatal("fixture is not variable frame rate")
	}
	srcOnset, srcHz := nativeTone(t, tools, source)
	dstOnset, dstHz := nativeTone(t, tools, preview)
	if math.Abs(srcOnset-.4) > .01 || math.Abs(srcOnset-dstOnset) > .025 || math.Abs(srcHz-440) > 2 || math.Abs(dstHz-440) > 2 {
		t.Fatalf("audio offset or content changed: onset %.4f -> %.4f, Hz %.1f -> %.1f", srcOnset, dstOnset, srcHz, dstHz)
	}
	top, bottom := image.Rect(0, 0, 320, 65), image.Rect(0, 120, 320, 180)
	for _, at := range []float64{.5, 4.5} {
		src, dst := nativeFrame(t, tools, source, at), nativeFrame(t, tools, preview, at)
		a, b := brightPixels(src, top), brightPixels(dst, top)
		if a < 40 || math.Abs(float64(a-b))/float64(a) > .2 || brightPixels(dst, bottom) > 3 {
			t.Fatalf("Chinese layer changed at %.1f: top %d -> %d, extra bottom=%d", at, a, b, brightPixels(dst, bottom))
		}
		r, _, blue, _ := dst.At(3, 90).RGBA()
		if at < 3 && r < 50000 || at > 3 && blue < 50000 {
			t.Fatal("preview changed scene order/timing")
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source bytes changed")
	}
	t.Logf("VFR frames=%d, duration=%.3fs, audio onset %.4f -> %.4f, tone %.1f -> %.1fHz; Chinese remains one layer",
		len(srcTimes), info.Duration, srcOnset, dstOnset, srcHz, dstHz)
}

func TestIntegrationSubtitlesOffDoesNotBurnSecondChineseLayer(t *testing.T) {
	tools := integrationTools(t)
	source := chinesePreviewFixture(t, tools)
	draft := domain.NewDraft("Test", []domain.Scene{{ID: "all", Start: 0, End: 6}})
	top, bottom := image.Rect(0, 0, 320, 65), image.Rect(0, 120, 320, 180)
	for _, mode := range []string{"off-nil", "off-invalid", "off-valid", "on"} {
		t.Run(mode, func(t *testing.T) {
			draft.Subtitles = mode == "on"
			cues := []domain.Cue{{Start: 0, End: 6, Text: "第二层中文字幕"}}
			if mode == "off-nil" {
				cues = nil
			} else if mode == "off-invalid" {
				cues = []domain.Cue{{Start: math.NaN(), End: -1, Text: "\xff"}}
			}
			out := t.TempDir()
			info, err := tools.Render(context.Background(), source, out, "result.mp4", draft, cues, nil)
			if err != nil || math.Abs(info.Duration-6) > .05 || !info.HasAudio {
				t.Fatalf("render: %+v %v", info, err)
			}
			img := nativeFrame(t, tools, filepath.Join(out, "result.mp4"), 4.5) // title has expired
			if brightPixels(img, top) < 40 {
				t.Fatal("source Chinese text lost")
			}
			n := brightPixels(img, bottom)
			if mode == "on" && n < 40 || strings.HasPrefix(mode, "off-") && n > 3 {
				t.Fatalf("subtitle switch ignored: bottom=%d", n)
			}
			t.Logf("%s: original Chinese=%d pixels; added subtitle=%d pixels", mode, brightPixels(img, top), n)
		})
	}
}

func TestIntegrationPreciseSamplingThumbnailAndSilentPreview(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "none")
	times := []float64{4.321, .123, 2.999}
	frames, err := tools.SampleAt(context.Background(), source, t.TempDir(), times, nil)
	if err != nil || len(frames) != len(times) {
		t.Fatalf("SampleAt: %v %v", frames, err)
	}
	for i, frame := range frames {
		data, err := tools.Thumbnail(context.Background(), source, times[i])
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.ReadFile(frame.Path)
		if err != nil || !bytes.Equal(data, file) || frame.Time != times[i] {
			t.Fatalf("SampleAt and Thumbnail seek disagree: %v", err)
		}
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r, _, b, _ := img.At(3, 3).RGBA()
		// Accurate seek returns the first frame at/after the target: 2.999 -> 3.
		if times[i] >= 2.999 && b < 50000 || times[i] < 2.999 && r < 50000 {
			t.Fatalf("wrong frame at %v", times[i])
		}
	}
	info, err := tools.CompatiblePreview(context.Background(), source, t.TempDir(), "silent.mp4", nil)
	if err != nil || info.HasAudio || math.Abs(info.Duration-6) > .05 {
		t.Fatalf("silent preview: %+v %v", info, err)
	}
}

func TestIntegrationThumbnailBoundsPortrait(t *testing.T) {
	tools := integrationTools(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "portrait.mp4")
	_, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 30 * time.Second,
		args: []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-f", "lavfi",
			"-i", "color=red:s=320x1280:r=5:d=0.4", "-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", source}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := tools.Thumbnail(context.Background(), source, 0)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds().Dx() != 160 || img.Bounds().Dy() != 640 || len(data) > jpegLimit {
		t.Fatalf("portrait thumbnail bounds: %v %v", img, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "portrait.mp4" {
		t.Fatalf("thumbnail created filesystem output: %v %v", entries, err)
	}
}

func TestIntegrationPreviewKeepsNonzeroTimeOrigin(t *testing.T) {
	tools := integrationTools(t)
	base := generateSource(t, tools, "none")
	dir := t.TempDir()
	source := filepath.Join(dir, "offset.mp4")
	_, err := run(context.Background(), command{exe: tools.cfg.FFmpeg, timeout: 30 * time.Second,
		args: []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-i", base,
			"-c", "copy", "-output_ts_offset", "5", source}})
	if err != nil {
		t.Fatal(err)
	}
	srcInfo, err := tools.Probe(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := tools.CompatiblePreview(context.Background(), source, dir, "preview.mp4", nil)
	if err != nil || math.Abs(info.Duration-srcInfo.Duration) > .05 {
		t.Fatalf("offset preview: source=%+v output=%+v err=%v", srcInfo, info, err)
	}
	src, dst := nativeVideoTimes(t, tools, source), nativeVideoTimes(t, tools, filepath.Join(dir, "preview.mp4"))
	if len(src) == 0 || len(src) != len(dst) || math.Abs(src[0]-5) > .001 {
		t.Fatalf("unexpected offset fixture/frame counts: source=%v output=%v", src, dst)
	}
	for i, at := range src {
		if math.Abs(at-dst[i]) > .001 {
			t.Fatalf("nonzero origin changed at frame %d: %.6f -> %.6f", i, at, dst[i])
		}
	}
	t.Logf("nonzero origin retained: %.3f -> %.3f; duration %.3f -> %.3f", src[0], dst[0], srcInfo.Duration, info.Duration)
}

func TestIntegrationContentTitleDisabledPreservesSourcePicture(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "tone")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	draft := domain.NewDraft("Visible title if the flag is lost", []domain.Scene{{ID: "whole", Start: 0, End: 6}})
	disabled := false
	draft.Goal, draft.Hook, draft.TitleEnabled, draft.Subtitles = "content", "", &disabled, false
	out := t.TempDir()
	noFonts := New(Config{FFmpeg: tools.cfg.FFmpeg, FFprobe: tools.cfg.FFprobe, FontDir: filepath.Join(out, "missing-fonts")})
	info, err := noFonts.Render(ctx, source, out, "plain.mp4", draft, nil, nil)
	if err != nil || !info.HasAudio || math.Abs(info.Duration-6) > .05 {
		t.Fatalf("content render: %+v %v", info, err)
	}
	changed := func(a, b image.Image) int {
		n := 0
		if a.Bounds() != b.Bounds() {
			t.Fatal("render changed original dimensions")
		}
		for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
			for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
				ar, ag, ab, _ := a.At(x, y).RGBA()
				br, bg, bb, _ := b.At(x, y).RGBA()
				if math.Abs(float64(ar)-float64(br)) > 20*257 ||
					math.Abs(float64(ag)-float64(bg)) > 20*257 ||
					math.Abs(float64(ab)-float64(bb)) > 20*257 {
					n++
				}
			}
		}
		return n
	}
	for _, at := range []float64{.5, 2.5, 3.5, 4.5} {
		original := nativeFrame(t, tools, source, at)
		plain := nativeFrame(t, tools, filepath.Join(out, "plain.mp4"), at)
		if n := changed(original, plain); n != 0 {
			t.Fatalf("disabled content title changed %d source pixels at %.1fs", n, at)
		}
	}
	// Positive control ensures the first-four-seconds pixel check detects titles.
	enabled := true
	draft.TitleEnabled = &enabled
	if _, err := tools.Render(ctx, source, out, "with-title.mp4", draft, nil, nil); err != nil {
		t.Fatal(err)
	}
	n := changed(nativeFrame(t, tools, source, .5), nativeFrame(t, tools, filepath.Join(out, "with-title.mp4"), .5))
	if n < 100 {
		t.Fatal("positive title control was not visible", n)
	}
	t.Logf("content: zero changed pixels at 0.5/2.5/3.5/4.5s; title-on control changed %d pixels", n)
}

func TestIntegrationVerifyPreviewAcceptsOnlyCompatibleMatchingCheckpoint(t *testing.T) {
	tools := integrationTools(t)
	source := generateSource(t, tools, "tone")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := t.TempDir()
	valid := filepath.Join(out, "valid.mp4")
	if _, err := tools.CompatiblePreview(ctx, source, out, valid, nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	// A restart verifier needs only ffprobe; it must not re-encode a valid file.
	verifier := New(Config{FFprobe: tools.cfg.FFprobe, FFmpeg: filepath.Join(out, "must-not-run")})
	if info, err := verifier.VerifyPreview(ctx, source, valid); err != nil || !info.HasAudio || math.Abs(info.Duration-6) > .05 {
		t.Fatalf("valid published checkpoint rejected: %+v %v", info, err)
	}
	after, err := os.ReadFile(valid)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("verification rewrote the existing preview", err)
	}
	for _, tc := range []struct {
		name, ext string
		options   []string
	}{
		{"short", ".mp4", []string{"-t", "3"}},
		{"dimensions", ".mp4", []string{"-vf", "scale=160:90"}},
		{"no-audio", ".mp4", []string{"-an"}},
		{"wrong-video-codec", ".mp4", []string{"-c:v", "mpeg4"}},
		{"wrong-pixel-format", ".mp4", []string{"-pix_fmt", "yuv444p"}},
		{"wrong-container", ".mkv", []string{"-f", "matroska"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(out, tc.name+tc.ext)
			args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-i", source,
				"-map", "0:v:0", "-map", "0:a:0", "-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", "-c:a", "aac"}
			args = append(args, tc.options...)
			args = append(args, path)
			if _, err := run(ctx, command{exe: tools.cfg.FFmpeg, timeout: 15 * time.Second, args: args}); err != nil {
				t.Fatal(err)
			}
			if _, err := tools.Probe(ctx, path); err != nil {
				t.Fatal("negative fixture must be playable media, not just corrupt bytes", err)
			}
			if _, err := verifier.VerifyPreview(ctx, source, path); err == nil {
				t.Fatal("incompatible or mismatched checkpoint accepted", tc.name)
			}
		})
	}
}
