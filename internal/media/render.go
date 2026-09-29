package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"autoclip-go/internal/domain"
)

const renderFPS = 30

func sceneFrames(s domain.Scene) int {
	return max(1, int(math.Round((s.End-s.Start)*renderFPS)))
}

func sceneDuration(s domain.Scene) float64 { return float64(sceneFrames(s)) / renderFPS }

// OutputDimensions is shared by title-preview endpoints and Render. Original
// preserves aspect, never upscales, caps the long edge at 1920, and rounds down
// to even dimensions. Invalid aspects or original source sizes return (0, 0).
func OutputDimensions(info Info, aspect string) (int, int) {
	switch aspect {
	case "portrait":
		return 1080, 1920
	case "landscape":
		return 1920, 1080
	case "original":
		if info.Width < 2 || info.Height < 2 || info.Width > 16384 || info.Height > 16384 {
			return 0, 0
		}
		scale := min(1.0, 1920/float64(max(info.Width, info.Height)))
		return max(2, int(float64(info.Width)*scale)/2*2), max(2, int(float64(info.Height)*scale)/2*2)
	default:
		return 0, 0
	}
}

func layoutFilter(input, output, layout string, w, h int, cropX float64, index int) string {
	size := fmt.Sprintf("%d:%d", w, h)
	fit := "scale=" + size + ":force_original_aspect_ratio=decrease:force_divisible_by=2"
	crop := "scale=" + size + ":force_original_aspect_ratio=increase:force_divisible_by=2,crop=" +
		size + ":x='(iw-ow)*" + number(cropX) + "':y='(ih-oh)/2'"
	switch layout {
	case "crop":
		return input + crop + ",setsar=1" + output
	case "blur":
		a, b := fmt.Sprintf("[bg%d]", index), fmt.Sprintf("[fg%d]", index)
		bg, fg := fmt.Sprintf("[blur%d]", index), fmt.Sprintf("[fit%d]", index)
		return input + "split=2" + a + b + ";" + a + crop + ",gblur=sigma=24,setsar=1" + bg + ";" +
			b + fit + ",setsar=1" + fg + ";" + bg + fg + "overlay=(W-w)/2:(H-h)/2:shortest=1,setsar=1" + output
	default:
		return input + fit + ",pad=" + size + ":(ow-iw)/2:(oh-ih)/2:color=black,setsar=1" + output
	}
}

type renderPlan struct {
	args     []string
	graph    string
	duration float64
	width    int
	height   int
	audio    bool
}

func (t *Tools) planRender(source string, info Info, draft domain.Draft, subtitles bool) renderPlan {
	w, h := OutputDimensions(info, draft.Aspect)
	plan := renderPlan{width: w, height: h, audio: draft.OriginalAudio && info.HasAudio}
	args := append(ffmpegBase(), "-filter_complex_threads", "2")
	var graph, inputs []string
	for i, scene := range draft.Scenes {
		duration := sceneDuration(scene)
		plan.duration += duration
		// Independent accurate seeks avoid decoding hours of discarded footage.
		args = append(args, "-ss", number(scene.Start), "-t", number(scene.End-scene.Start),
			"-threads", "2", "-protocol_whitelist", "file,pipe", "-i", source)
		raw, out := fmt.Sprintf("[raw%d]", i), fmt.Sprintf("[v%d]", i)
		graph = append(graph, fmt.Sprintf("[%d:v:0]setpts=PTS-STARTPTS,fps=%d,tpad=stop_mode=clone:stop_duration=0.1,trim=end_frame=%d,setpts=N/(%d*TB)%s",
			i, renderFPS, sceneFrames(scene), renderFPS, raw))
		graph = append(graph, layoutFilter(raw, out, draft.Layout, w, h, draft.CropX, i))
		inputs = append(inputs, out)
		if plan.audio {
			audio := fmt.Sprintf("[a%d]", i)
			graph = append(graph, fmt.Sprintf("[%d:a:0]asetpts=PTS-STARTPTS,aresample=48000:async=1:first_pts=0,aformat=sample_fmts=fltp:channel_layouts=stereo,apad,atrim=duration=%s%s",
				i, number(duration), audio))
			inputs = append(inputs, audio)
		}
	}
	audio := 0
	outputs := "[joined]"
	if plan.audio {
		audio, outputs = 1, "[joined][audio]"
	}
	graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=1:a=%d%s", strings.Join(inputs, ""), len(draft.Scenes), audio, outputs))
	video := "[joined]"
	if subtitles {
		graph = append(graph, video+"ass=filename=captions.ass:fontsdir=fonts[subbed]")
		video = "[subbed]"
	}
	args = append(args, "-loop", "1", "-framerate", "30", "-protocol_whitelist", "file,pipe", "-i", "title.png")
	x, y := "0", "0"
	if draft.TitleMotion && draft.TitleStyle != "pixel" && draft.TitleStyle != "frosted" {
		if draft.TitleStyle == "arena" {
			x = "-36*max(0,1-t/0.18)"
		} else {
			y = fmt.Sprintf("%d*max(0,1-t/0.18)", max(1, h*16/1000))
		}
	}
	graph = append(graph, fmt.Sprintf("[%d:v:0]format=rgba[title];%s[title]overlay=x='%s':y='%s':enable='lt(t,4)':shortest=1,format=yuv420p[video]",
		len(draft.Scenes), video, x, y))
	plan.graph = strings.Join(graph, ";")
	// Pass the graph as one argv entry, never through a shell. Unlike
	// filter_complex_script, this option is supported by old and new FFmpeg.
	args = append(args, "-filter_complex", plan.graph, "-map", "[video]")
	if plan.audio {
		args = append(args, "-map", "[audio]", "-c:a", "aac", "-b:a", "160k")
	} else {
		args = append(args, "-an")
	}
	plan.args = append(args, "-map_metadata", "-1", "-map_chapters", "-1", "-c:v", "libx264", "-preset", "veryfast",
		"-crf", "20", "-threads", "2", "-pix_fmt", "yuv420p", "-t", number(plan.duration),
		"-fs", fmt.Sprint(t.cfg.MaxBytes+1), "-movflags", "+faststart", "-f", "mp4", "partial.mp4")
	return plan
}

func assTime(seconds float64) string {
	cs := int64(math.Round(seconds * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

func escapeASS(text string) string {
	text = normalizeText(text)
	return strings.NewReplacer(`\`, `\\`, "{", `\{`, "}", `\}`, "\n", `\N`).Replace(text)
}

func subtitleFontSize(w, h int) int {
	return max(12, int(float64(min(w, h))*.048))
}

func (t *Tools) subtitleASS(cues []domain.Cue, w, h int) (data []byte, err error) {
	faces, err := t.titleFaces("NotoSansSC-StaticBold.ttf", float64(subtitleFontSize(w, h)))
	if err != nil {
		return nil, fmt.Errorf("subtitle font: %w", err)
	}
	defer func() { err = errors.Join(err, faces.close()) }()
	// Older libass builds do not wrap CJK without spaces. Explicit line breaks
	// use the bundled font's real metrics, leaving room for outlines/shaping.
	width := w - 2*(w/15) - 2*max(1, h/540) - subtitleFontSize(w, h)/5
	height := h - 2*max(6, h/16)
	wrapped := make([]domain.Cue, len(cues))
	for i, cue := range cues {
		text := strings.ReplaceAll(normalizeText(cue.Text), "\t", " ")
		lines, e := wrapTitle(faces, text, width)
		if e != nil {
			return nil, fmt.Errorf("subtitle cue %d: %w", i+1, e)
		}
		if len(lines)*faces.latin.Metrics().Height.Ceil() > height {
			return nil, fmt.Errorf("subtitle cue %d has too many lines to fit the frame; split or edit it", i+1)
		}
		wrapped[i] = cue
		wrapped[i].Text = strings.Join(lines, "\n")
	}
	return formatASS(wrapped, w, h), nil
}

func formatASS(cues []domain.Cue, w, h int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\nWrapStyle: 0\nScaledBorderAndShadow: yes\n\n", w, h)
	b.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	// The pinned weight-700 static instance retains this legacy family name.
	// libass resolves embedded fonts by that name, not the typographic family
	// "Noto Sans SC"; the latter silently falls back to missing CJK glyphs.
	fmt.Fprintf(&b, "Style: Default,Noto Sans SC Thin,%d,&H00FFFFFF,&H00FFFFFF,&H00101010,&H80000000,-1,0,0,0,100,100,0,0,1,%d,0,2,%d,%d,%d,1\n\n",
		subtitleFontSize(w, h), max(1, h/540), w/15, w/15, max(6, h/16))
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, cue := range cues {
		start := math.Round(cue.Start*100) / 100
		end := max(start+.01, math.Round(cue.End*100)/100)
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTime(start), assTime(end), escapeASS(cue.Text))
	}
	return []byte(b.String())
}

func outputPath(outDir, output string) (string, error) {
	if strings.TrimSpace(outDir) == "" || strings.TrimSpace(output) == "" {
		return "", errors.New("media: output directory and filename are required")
	}
	parent, err := filepath.Abs(outDir)
	if err != nil {
		return "", err
	}
	target := output
	if !filepath.IsAbs(target) {
		target = filepath.Join(parent, output)
	}
	target = filepath.Clean(target)
	// A direct child keeps rename on the same filesystem and avoids traversing
	// caller-controlled symlink subdirectories.
	if filepath.Dir(target) != parent || !strings.EqualFold(filepath.Ext(target), ".mp4") {
		return "", errors.New("media: output must be an MP4 directly inside outDir")
	}
	if _, err = os.Lstat(target); err == nil {
		return "", errors.New("media: output already exists; exports are immutable")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return target, nil
}

// Render publishes a verified H.264/AAC MP4 using a same-filesystem atomic rename.
// output is an absolute path or filename directly inside outDir; existing files
// are never intentionally overwritten. Callers must serialize identical targets.
func (t *Tools) Render(ctx context.Context, source, outDir, output string, draft domain.Draft, cues []domain.Cue, progress domain.ProgressFunc) (result Info, err error) {
	target, err := outputPath(outDir, output)
	if err != nil {
		return Info{}, err
	}
	info, err := t.Probe(ctx, source)
	if err != nil {
		return Info{}, err
	}
	if err = draft.Validate(info.Duration); err != nil {
		return Info{}, fmt.Errorf("render draft: %w", err)
	}
	if err = validateCues(cues); err != nil {
		return Info{}, err
	}
	for _, s := range draft.Scenes {
		if s.Start >= info.Duration || s.End > info.Duration+.001 {
			return Info{}, errors.New("render scene extends past source")
		}
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return Info{}, err
	}
	if source == target {
		return Info{}, errors.New("media: output cannot replace source")
	}
	if err = report(progress, "render", percent(0)); err != nil {
		return Info{}, err
	}
	dir, err := workspace(outDir, ".render-")
	if err != nil {
		return Info{}, err
	}
	defer cleanup(dir, &err)
	timedCues := timelineCues(draft.Scenes, cues)
	subtitles := draft.Subtitles && len(timedCues) > 0
	plan := t.planRender(source, info, draft, subtitles)
	if plan.duration > t.cfg.MaxDuration {
		return Info{}, errors.New("rendered duration exceeds MaxDuration")
	}
	title, err := t.TitlePNG(draft, plan.width, plan.height)
	if err != nil {
		return Info{}, err
	}
	if err = os.WriteFile(filepath.Join(dir, "title.png"), title, 0600); err != nil {
		return Info{}, err
	}
	if subtitles {
		captions, e := t.subtitleASS(timedCues, plan.width, plan.height)
		if e != nil {
			return Info{}, e
		}
		fontData, e := readLimited(filepath.Join(t.cfg.FontDir, "NotoSansSC-StaticBold.ttf"), 32<<20)
		if e != nil {
			return Info{}, fmt.Errorf("subtitle font: %w", e)
		}
		if err = os.Mkdir(filepath.Join(dir, "fonts"), 0700); err != nil {
			return Info{}, err
		}
		if err = os.WriteFile(filepath.Join(dir, "fonts", "NotoSansSC-StaticBold.ttf"), fontData, 0600); err != nil {
			return Info{}, err
		}
		if err = os.WriteFile(filepath.Join(dir, "captions.ass"), captions, 0600); err != nil {
			return Info{}, err
		}
	}
	_, err = run(ctx, command{exe: t.cfg.FFmpeg, args: plan.args, dir: dir,
		timeout: budget(plan.duration, 30, 5*time.Minute, 2*time.Hour),
		line:    ffmpegProgress(progress, "render", plan.duration, 98)})
	if err != nil {
		return Info{}, err
	}
	if err = report(progress, "render-validate", percent(99)); err != nil {
		return Info{}, err
	}
	partial := filepath.Join(dir, "partial.mp4")
	result, err = t.Probe(ctx, partial)
	if err != nil {
		return Info{}, fmt.Errorf("render output validation: %w", err)
	}
	if result.Width != plan.width || result.Height != plan.height || result.HasAudio != plan.audio ||
		math.Abs(result.Duration-plan.duration) > .12 {
		return Info{}, fmt.Errorf("render output mismatch: got %+v, expected %dx%d %.3fs audio=%t",
			result, plan.width, plan.height, plan.duration, plan.audio)
	}
	f, err := os.OpenFile(partial, os.O_RDWR, 0)
	if err != nil {
		return Info{}, err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return Info{}, fmt.Errorf("sync render output: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return Info{}, err
	}
	if _, err = outputPath(outDir, output); err != nil {
		return Info{}, err
	}
	if err = os.Rename(partial, target); err != nil {
		return Info{}, fmt.Errorf("publish render output: %w", err)
	}
	if err = report(progress, "render", percent(100)); err != nil {
		return Info{}, errors.Join(err, os.Remove(target))
	}
	return result, nil
}
