package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"math"
	"time"

	"autoclip-go/internal/domain"
)

const maxSamples = 60
const jpegLimit = 2 << 20
const jpegEdge = 640

// Match the upstream sparse scan, including its 100 ms end guard. Tiny videos
// still get their first frame rather than an empty scan.
func overallSampleTimes(duration float64) []float64 {
	interval := math.Max(2, duration/maxSamples)
	times := []float64{0}
	for i := 1; i < maxSamples; i++ {
		at := float64(i) * interval
		if at >= duration-.1 {
			break
		}
		times = append(times, math.Round(at*1000)/1000)
	}
	return times
}

// SampleAt extracts a caller-ordered, nonempty list of up to 60 source times.
// Times must be finite, unique and in [0,duration). Millisecond normalization is
// explicit: returned times and native seeks use the same normalized value.
// Duplicate times after normalization are rejected, never silently dropped.
func (t *Tools) SampleAt(ctx context.Context, video, outDir string, times []float64, progress domain.ProgressFunc) ([]domain.Frame, error) {
	if len(times) == 0 || len(times) > maxSamples {
		return nil, errors.New("media: SampleAt requires 1–60 timestamps")
	}
	normalized := make([]float64, len(times))
	seen := make(map[float64]bool, len(times))
	for i, at := range times {
		if !finite(at) || at < 0 || at > t.cfg.MaxDuration {
			return nil, fmt.Errorf("media: invalid sample timestamp at index %d", i)
		}
		normalized[i] = math.Round(at*1000) / 1000
		if seen[normalized[i]] {
			return nil, fmt.Errorf("media: duplicate sample timestamp at index %d after millisecond rounding", i)
		}
		seen[normalized[i]] = true
	}
	info, err := t.Probe(ctx, video)
	if err != nil {
		return nil, err
	}
	for i, at := range normalized {
		if times[i] >= info.Duration || at >= info.Duration {
			return nil, fmt.Errorf("media: sample timestamp at index %d exceeds source duration", i)
		}
	}
	return t.sampleAt(ctx, video, outDir, normalized, progress)
}

// Thumbnail returns a validated JPEG, not a filesystem path. The complete
// operation (probe included) is limited to 30 seconds, 640x640 pixels and 2 MiB.
func (t *Tools) Thumbnail(ctx context.Context, video string, at float64) ([]byte, error) {
	if !finite(at) || at < 0 {
		return nil, errors.New("media: invalid thumbnail timestamp")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := t.Probe(ctx, video)
	if err != nil {
		return nil, fmt.Errorf("thumbnail probe: %w", err)
	}
	if at >= info.Duration {
		return nil, errors.New("media: thumbnail timestamp exceeds source duration")
	}
	video, err = t.localFile(ctx, video)
	if err != nil {
		return nil, err
	}
	return t.extractJPEG(ctx, video, at)
}

func (t *Tools) extractJPEG(ctx context.Context, video string, at float64) ([]byte, error) {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-ss", number(at),
		"-threads", "2", "-protocol_whitelist", "file,pipe", "-i", video,
		"-map", "0:v:0", "-frames:v", "1", "-vf",
		"scale=w='min(640,iw)':h='min(640,ih)':force_original_aspect_ratio=decrease",
		"-threads", "2", "-q:v", "3", "-an", "-sn", "-dn", "-c:v", "mjpeg", "-f", "image2pipe", "pipe:1"}
	data, err := run(ctx, command{exe: t.cfg.FFmpeg, args: args, timeout: 30 * time.Second, stdoutLimit: jpegLimit})
	if err != nil {
		return nil, fmt.Errorf("thumbnail JPEG generation (30s timeout, 2 MiB limit): %w", err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("thumbnail invalid or missing JPEG: %w", err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > jpegEdge || cfg.Height > jpegEdge {
		return nil, fmt.Errorf("thumbnail dimensions %dx%d exceed %dx%d limit", cfg.Width, cfg.Height, jpegEdge, jpegEdge)
	}
	if _, err = jpeg.Decode(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("thumbnail corrupt JPEG: %w", err)
	}
	return data, nil
}
