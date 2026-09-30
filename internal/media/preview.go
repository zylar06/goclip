package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	"autoclip-go/internal/domain"
)

// CompatiblePreview produces an immutable browser-compatible MP4. Unlike Render
// it has no draft, titles, subtitle assets, scene cuts, or frame-rate conversion.
// Timestamps (including VFR gaps, nonzero input origins and A/V offsets) are
// retained, never reset or independently rebased per stream.
func (t *Tools) CompatiblePreview(ctx context.Context, source, outDir, output string, progress domain.ProgressFunc) (result Info, err error) {
	if _, err = outputPath(outDir, output); err != nil {
		return Info{}, err
	}
	info, err := t.Probe(ctx, source)
	if err != nil {
		return Info{}, err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return Info{}, err
	}
	if err = report(progress, "preview", percent(0)); err != nil {
		return Info{}, err
	}
	dir, err := workspace(outDir, ".preview-")
	if err != nil {
		return Info{}, err
	}
	defer cleanup(dir, &err)
	plan := t.planPreview(source, info)
	_, err = run(ctx, command{exe: t.cfg.FFmpeg, args: plan.args, dir: dir,
		timeout: budget(info.Duration, 30, 5*time.Minute, 2*time.Hour),
		line:    ffmpegProgress(progress, "preview", info.Duration, 98)})
	if err != nil {
		return Info{}, err
	}
	return t.publishMP4(ctx, dir, outDir, output, plan, "preview", progress)
}

func (t *Tools) planPreview(source string, info Info) renderPlan {
	w, h := OutputDimensions(info, "original")
	args := append(ffmpegBase(), "-copyts", "-threads", "2",
		"-protocol_whitelist", "file,pipe", "-i", source, "-map", "0:v:0",
		"-vf", fmt.Sprintf("scale=%d:%d,setsar=1", w, h),
		// A numeric microsecond time base works on both older and newer FFmpeg
		// (the historical -1 "demux" alias was removed in newer builds).
		"-fps_mode", "passthrough", "-enc_time_base:v", "1:1000000")
	if info.HasAudio {
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", "160k")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p",
		// With copyts, output -t is an absolute timestamp cutoff, not a
		// source-length cap: it would truncate a source with a positive origin.
		// Probe, timeout and byte limits bound the complete-source transcode.
		"-threads", "2", "-avoid_negative_ts", "disabled",
		"-fs", fmt.Sprint(t.cfg.MaxBytes+1), "-movflags", "+faststart", "-f", "mp4", "partial.mp4")
	return renderPlan{args: args, width: w, height: h, duration: info.Duration, audio: info.HasAudio}
}

// VerifyPreview validates a published checkpoint after a worker restart.
func (t *Tools) VerifyPreview(ctx context.Context, source, output string) (Info, error) {
	input, err := t.Probe(ctx, source)
	if err != nil {
		return Info{}, err
	}
	got, err := t.Probe(ctx, output)
	if err != nil {
		return Info{}, err
	}
	w, h := OutputDimensions(input, "original")
	if got.Width != w || got.Height != h || got.HasAudio != input.HasAudio || math.Abs(got.Duration-input.Duration) > .12 {
		return Info{}, errors.New("media: preview checkpoint does not match the source")
	}
	if err = t.validateMP4Encoding(ctx, output, input.HasAudio); err != nil {
		return Info{}, err
	}
	return got, nil
}

// Check actual codecs rather than trusting either a filename or an encoder's
// exit code. Kept private so Info's public JSON and positional struct contract
// remain unchanged.
func (t *Tools) validateMP4Encoding(ctx context.Context, path string, audio bool) error {
	data, err := run(ctx, command{exe: t.cfg.FFprobe, timeout: 30 * time.Second, stdoutLimit: 1 << 20,
		args: []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries",
			"format=format_name:stream=codec_type,codec_name,pix_fmt", "-of", "json", path}})
	if err != nil {
		return fmt.Errorf("MP4 codec validation: %w", err)
	}
	var raw struct {
		Streams []struct {
			Type        string `json:"codec_type"`
			Name        string `json:"codec_name"`
			PixelFormat string `json:"pix_fmt"`
		}
		Format struct {
			Name string `json:"format_name"`
		}
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("MP4 codec JSON: %w", err)
	}
	if raw.Format.Name != "mov,mp4,m4a,3gp,3g2,mj2" && raw.Format.Name != "mp4" {
		return errors.New("media: output is not an MP4 container")
	}
	videoCount, audioCount := 0, 0
	for _, s := range raw.Streams {
		switch s.Type {
		case "video":
			videoCount++
			if s.Name != "h264" || s.PixelFormat != "yuv420p" {
				return errors.New("media: output video must be H264/yuv420p")
			}
		case "audio":
			audioCount++
			if s.Name != "aac" {
				return errors.New("media: output audio must be AAC")
			}
		default:
			return errors.New("media: unexpected non-audio/video output stream")
		}
	}
	wantAudio := 0
	if audio {
		wantAudio = 1
	}
	if videoCount != 1 || audioCount != wantAudio {
		return errors.New("media: unexpected MP4 stream count")
	}
	return nil
}
