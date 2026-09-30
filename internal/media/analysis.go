package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"autoclip-go/internal/domain"
)

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, limit+1))
	if err = errors.Join(readErr, f.Close()); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file %q exceeds %d-byte limit", path, limit)
	}
	return data, nil
}

func number(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }

func ffmpegBase() []string {
	return []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error", "-progress", "pipe:1", "-nostats"}
}

func ffmpegProgress(fn domain.ProgressFunc, stage string, duration float64, scale float64) func(string) error {
	last := -1.0
	return func(line string) error {
		if !strings.HasPrefix(line, "out_time_us=") {
			return nil
		}
		v, err := strconv.ParseFloat(strings.TrimPrefix(line, "out_time_us="), 64)
		if err != nil || !finite(v) || duration <= 0 {
			return nil // FFmpeg legitimately emits N/A before the first timestamp.
		}
		p := min(scale, max(0, v/1e6/duration*scale))
		if p <= last {
			return nil
		}
		last = p
		return report(fn, stage, percent(p))
	}
}

var whisperProgressRE = regexp.MustCompile(`progress\s*=\s*(\d{1,3})%`)

// Transcribe runs whisper.cpp locally, using a 16 kHz mono PCM WAV and SRT output.
// Empty ASR output is valid (silence); missing/malformed output is not.
func (t *Tools) Transcribe(ctx context.Context, video, outDir string, progress domain.ProgressFunc) (cues []domain.Cue, err error) {
	info, err := t.Probe(ctx, video)
	if err != nil {
		return nil, err
	}
	if !info.HasAudio {
		return nil, errors.New("media: cannot transcribe video without audio")
	}
	video, err = filepath.Abs(video)
	if err != nil {
		return nil, err
	}
	dir, err := workspace(outDir, ".transcribe-")
	if err != nil {
		return nil, err
	}
	defer cleanup(dir, &err)
	if err = report(progress, "transcribe-audio", percent(0)); err != nil {
		return nil, err
	}
	args := append(ffmpegBase(), "-protocol_whitelist", "file,pipe", "-i", video, "-map", "0:a:0", "-vn",
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", "-t", number(info.Duration), "audio.wav")
	if _, err = run(ctx, command{exe: t.cfg.FFmpeg, args: args, dir: dir,
		timeout: budget(info.Duration, 2, time.Minute, 30*time.Minute),
		line:    ffmpegProgress(progress, "transcribe-audio", info.Duration, 99)}); err != nil {
		return nil, err
	}
	signal, err := wavHasSignal(filepath.Join(dir, "audio.wav"))
	if err != nil {
		return nil, fmt.Errorf("transcribe PCM validation: %w", err)
	}
	if !signal {
		if err = report(progress, "transcribe", percent(100)); err != nil {
			return nil, err
		}
		return []domain.Cue{}, nil
	}
	if t.cfg.Model == "" {
		return nil, errors.New("media: whisper.cpp Model path is required for non-silent audio")
	}
	model, err := os.Stat(t.cfg.Model)
	if err != nil {
		return nil, fmt.Errorf("whisper model: %w", err)
	}
	if !model.Mode().IsRegular() || model.Size() == 0 {
		return nil, errors.New("media: whisper model must be a nonempty regular file")
	}
	if err = report(progress, "transcribe", nil); err != nil {
		return nil, err
	}
	last := -1
	_, err = run(ctx, command{exe: t.cfg.Whisper, dir: dir,
		timeout: budget(info.Duration, 20, 10*time.Minute, 4*time.Hour),
		args: []string{"--model", t.cfg.Model, "--file", "audio.wav", "--language", "auto",
			"--output-srt", "--output-file", "transcript", "--print-progress"},
		line: func(line string) error {
			m := whisperProgressRE.FindStringSubmatch(line)
			if m == nil {
				return nil
			}
			n, _ := strconv.Atoi(m[1]) // Bounded digits from the regexp.
			n = min(n, 99)
			if n <= last {
				return nil
			}
			last = n
			return report(progress, "transcribe", percent(float64(n)))
		}})
	if err != nil {
		return nil, err
	}
	data, err := readLimited(filepath.Join(dir, "transcript.srt"), subtitleLimit)
	if err != nil {
		return nil, fmt.Errorf("whisper SRT output: %w", err)
	}
	cues, err = ParseSRT(data)
	if err != nil {
		return nil, fmt.Errorf("whisper SRT output: %w", err)
	}
	for i := range cues {
		if cues[i].Start >= info.Duration || cues[i].End > info.Duration+.5 {
			return nil, errors.New("whisper returned out-of-range subtitle timing")
		}
		cues[i].End = min(cues[i].End, info.Duration)
	}
	if err = report(progress, "transcribe", percent(100)); err != nil {
		return nil, err
	}
	return cues, nil
}

// Sample extracts <=60 deterministic JPEGs, at most 640 px per edge. duration is the
// requested source window starting at zero (0 means the entire video). Each Frame
// records the actual requested seek time, including for very short videos.
func (t *Tools) Sample(ctx context.Context, video, outDir string, duration float64, progress domain.ProgressFunc) (frames []domain.Frame, err error) {
	if !finite(duration) || duration < 0 {
		return nil, errors.New("media: invalid sampling duration")
	}
	info, err := t.Probe(ctx, video)
	if err != nil {
		return nil, err
	}
	if duration == 0 {
		duration = info.Duration
	}
	if duration > info.Duration+.001 {
		return nil, errors.New("media: sample window exceeds source duration")
	}
	duration = min(duration, info.Duration)
	return t.sampleAt(ctx, video, outDir, overallSampleTimes(duration), progress)
}

func (t *Tools) sampleAt(ctx context.Context, video, outDir string, times []float64, progress domain.ProgressFunc) (frames []domain.Frame, err error) {
	video, err = filepath.Abs(video)
	if err != nil {
		return nil, err
	}
	dir, err := workspace(outDir, ".frames-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			cleanup(dir, &err)
		}
	}()
	if err = report(progress, "sample", percent(0)); err != nil {
		return nil, err
	}
	for i, at := range times {
		path := filepath.Join(dir, fmt.Sprintf("frame-%03d.jpg", i+1))
		data, e := t.extractJPEG(ctx, video, at)
		if e != nil {
			return nil, e
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			return nil, err
		}
		frames = append(frames, domain.Frame{Time: at, Path: path})
		if err = report(progress, "sample", percent(100*float64(i+1)/float64(len(times)))); err != nil {
			return nil, err
		}
	}
	keep = true
	return frames, nil
}
