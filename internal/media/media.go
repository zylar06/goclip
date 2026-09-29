// Package media runs local media tools. It never invokes a shell or a cloud API.
package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"autoclip-go/internal/domain"
	"golang.org/x/image/font/opentype"
)

type Config struct {
	FFmpeg, FFprobe, YTDLP, Whisper, Model, FontDir string
	MaxDuration                                     float64
	MaxBytes                                        int64
}

type Info struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	HasAudio bool    `json:"has_audio"`
}

type Tools struct {
	cfg    Config
	cfgErr error
	fontMu sync.Mutex
	fonts  map[string]*opentype.Font
}

// New applies defaults (2 hours, 4 GiB). Invalid configuration is reported by
// operations, since this constructor deliberately has no error return.
func New(cfg Config) *Tools {
	for dst, fallback := range map[*string]string{
		&cfg.FFmpeg: "ffmpeg", &cfg.FFprobe: "ffprobe",
		&cfg.YTDLP: "yt-dlp", &cfg.Whisper: "whisper-cli",
	} {
		if *dst == "" {
			*dst = fallback
		}
		// yt-dlp's --ffmpeg-location needs a real path, not just a PATH name.
		// Absent optional tools are resolved lazily by run, which returns an
		// explicit error only when that capability is actually requested.
		if resolved, err := exec.LookPath(*dst); err == nil {
			*dst = resolved
		}
	}
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = 7200
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 4 << 30
	}
	t := &Tools{cfg: cfg, fonts: make(map[string]*opentype.Font)}
	if !finite(cfg.MaxDuration) || cfg.MaxDuration < 0 || cfg.MaxBytes < 0 || cfg.MaxBytes == math.MaxInt64 {
		t.cfgErr = errors.New("media: invalid duration or byte limit")
	}
	if t.cfg.FontDir == "" {
		t.cfg.FontDir = defaultFontDir()
	}
	// Commands run in private working directories; resolve explicit relative paths now.
	for _, p := range []*string{&t.cfg.FontDir, &t.cfg.Model, &t.cfg.FFmpeg, &t.cfg.FFprobe, &t.cfg.YTDLP, &t.cfg.Whisper} {
		if *p != "" && (p == &t.cfg.FontDir || p == &t.cfg.Model || strings.ContainsAny(*p, `/\`)) {
			abs, err := filepath.Abs(*p)
			if err != nil {
				t.cfgErr = errors.Join(t.cfgErr, err)
			} else {
				*p = abs
			}
		}
	}
	return t
}

func defaultFontDir() string {
	dir, err := os.Getwd()
	if err == nil {
		for {
			candidate := filepath.Join(dir, "assets", "fonts")
			if st, e := os.Stat(candidate); e == nil && st.IsDir() {
				return candidate
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if exe, e := os.Executable(); e == nil {
		return filepath.Join(filepath.Dir(exe), "assets", "fonts")
	}
	return filepath.Join("assets", "fonts")
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (t *Tools) localFile(ctx context.Context, path string) (string, error) {
	if t.cfgErr != nil {
		return "", t.cfgErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("media: empty input path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("media input: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("media input: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > t.cfg.MaxBytes {
		return "", errors.New("media: input is not a nonempty regular file within MaxBytes")
	}
	return abs, nil
}

func workspace(parent, prefix string) (string, error) {
	if strings.TrimSpace(parent) == "" {
		return "", errors.New("media: output directory is required")
	}
	abs, err := filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return "", fmt.Errorf("media output directory: %w", err)
	}
	dir, err := os.MkdirTemp(abs, prefix)
	if err != nil {
		return "", fmt.Errorf("media workspace: %w", err)
	}
	return dir, nil
}

// Cleanup errors remain visible, including on an otherwise successful operation.
func cleanup(dir string, result *error) {
	if err := os.RemoveAll(dir); err != nil {
		*result = errors.Join(*result, fmt.Errorf("media cleanup %q: %w", dir, err))
	}
}

func report(fn domain.ProgressFunc, stage string, percent *float64) error {
	if fn == nil {
		return nil
	}
	if err := fn(stage, percent); err != nil {
		return fmt.Errorf("media progress %s: %w", stage, err)
	}
	return nil
}

func percent(v float64) *float64 { return &v }

func budget(duration, multiplier float64, base, maximum time.Duration) time.Duration {
	seconds := math.Min(duration*multiplier, maximum.Seconds())
	result := base + time.Duration(seconds*float64(time.Second))
	return min(result, maximum)
}
