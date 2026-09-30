package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/engine"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

type Worker struct {
	Store       *store.Store
	Media       *media.Tools
	MaxDuration float64
	MaxBytes    int64
	TaskTimeout time.Duration
	// Engine is the optional Python AutoClip media engine. Go retains task
	// ownership and durable state; a nil engine preserves the legacy path while
	// the migration is rolled out incrementally.
	Engine *engine.Runner
	// HealthBeat is invoked only by the OS-lock owner, never a standby worker.
	HealthBeat func() error
	// Execute is a test seam. Production uses execute.
	Execute func(context.Context, domain.Task, domain.ProgressFunc) error
}

func (w *Worker) Run(ctx context.Context) error {
	lock, err := acquireWorkerLock(ctx, w.Store.Dir)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.Close(); err != nil {
			slog.Error("worker execution lock close failed", "error", err)
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if w.HealthBeat != nil {
			if err := w.HealthBeat(); err != nil {
				return err
			}
		}
		n, err := w.Store.Recover(time.Now().Add(-90 * time.Second))
		if err != nil {
			return err
		}
		if n > 0 {
			slog.Warn("expired tasks interrupted", "count", n)
		}
		t, err := w.Store.Claim(ctx)
		if err == nil {
			if err = w.runTask(ctx, t); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, context.Canceled) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
func (w *Worker) runTask(parent context.Context, t domain.Task) error {
	scoped := *w
	scoped.Store = w.Store.ForTask(t)
	w = &scoped
	timeout := w.TaskTimeout
	if timeout == 0 {
		timeout = 6 * time.Hour
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	stopped := make(chan struct{})
	monitorDone := make(chan struct{})
	healthErr := make(chan error, 1)
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stopped:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				if w.HealthBeat != nil {
					if err := w.HealthBeat(); err != nil {
						healthErr <- err
						cancel()
						return
					}
				}
				if err := w.Store.Heartbeat(t.ID); err != nil {
					if !errors.Is(err, store.ErrConflict) {
						slog.Error("task heartbeat failed", "task", t.ID, "error", err)
					}
					cancel()
					return
				}
			}
		}
	}()
	slog.Info("task started", "task", t.ID, "kind", t.Kind)
	progress := func(stage string, percent *float64) error { return w.Store.Progress(t.ID, stage, percent) }
	exec := w.Execute
	if exec == nil {
		exec = w.execute
	}
	err := exec(ctx, t, progress)
	close(stopped)
	<-monitorDone
	var healthFailure error
	select {
	case healthFailure = <-healthErr:
		err = healthFailure
	default:
	}
	current, loadErr := w.Store.Task(t.ID)
	if loadErr != nil {
		return loadErr
	}
	if current.Status != "running" || current.LeaseID != t.LeaseID {
		return healthFailure
	}
	status, message, retryable := "completed", "", false
	if current.CancelRequested {
		status = "cancelled"
		message = "Cancelled by user"
		retryable = true
	} else if parent.Err() != nil {
		status = "interrupted"
		message = "Worker stopped; explicit retry required"
		retryable = true
	} else if err != nil {
		status = "failed"
		message = err.Error()
		retryable = true
		var modelError *ai.Error
		if errors.As(err, &modelError) {
			retryable = modelError.Retryable
		}
		if errors.Is(err, store.ErrLegacyCandidateOwnership) {
			retryable = false
		}
	}
	if status != "completed" {
		slog.Warn("task stopped", "task", t.ID, "status", status, "error", message)
	}
	if e := w.Store.Finish(t.ID, status, message, retryable); e != nil {
		return e
	}
	finished, e := w.Store.Task(t.ID)
	if e != nil {
		return e
	}
	status, message = finished.Status, finished.Error
	slog.Info("task finished", "task", t.ID, "status", status)
	return healthFailure
}
func (w *Worker) execute(ctx context.Context, t domain.Task, progress domain.ProgressFunc) error {
	dir, err := w.Store.ProjectDir(t.ProjectID)
	if err != nil {
		return err
	}
	switch t.Kind {
	case "import":
		return w.importSource(ctx, t, dir, progress)
	case "analyze":
		return w.analyze(ctx, t, dir, progress)
	case "export":
		return w.export(ctx, t, dir, progress)
	case "inspect":
		return w.inspectSource(ctx, t, dir, progress)
	case "preview":
		return w.preparePreview(ctx, t, dir, progress)
	default:
		return fmt.Errorf("unsupported task kind %q", t.Kind)
	}
}
func atomicJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".partial-" + domain.ID()
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		if cleanup := os.Remove(tmp); cleanup != nil {
			slog.Error("temporary file cleanup", "error", cleanup)
		}
		return err
	}
	return nil
}
func (w *Worker) importSource(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	var input domain.ImportPayload
	if err := json.Unmarshal(t.Payload, &input); err != nil {
		return err
	}
	source, err := w.Store.Asset(t.ProjectID, "source")
	if errors.Is(err, store.ErrNotFound) && input.URL != "" {
		checkpoint, e := readDownloadCheckpoint(dir, t.ID, input)
		if e == nil {
			source = filepath.Join(dir, checkpoint.Video)
			if input.Subtitle == "" {
				input.Subtitle = checkpoint.Subtitle
			}
			err = nil
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		if input.URL != "" {
			cookieFile := ""
			cookies, e := w.Store.Secret("cookies")
			if e != nil && !errors.Is(e, store.ErrNotFound) {
				return e
			}
			if len(cookies) > 0 {
				f, e := os.CreateTemp("", "autoclip-cookies-*.txt")
				if e != nil {
					return e
				}
				cookieFile = f.Name()
				defer func() {
					if e := os.Remove(cookieFile); e != nil {
						slog.Error("cookie tempfile cleanup", "error", e)
					}
				}()
				if _, e = f.Write(cookies); e != nil {
					f.Close()
					return e
				}
				if e = f.Close(); e != nil {
					return e
				}
			}
			video, subtitle, e := w.Media.DownloadWithSubtitles(ctx, input.URL, dir, cookieFile, input.Subtitle == "", progress)
			if e != nil {
				return e
			}
			source = video
			if subtitle != "" && input.Subtitle == "" {
				rel, e := filepath.Rel(dir, subtitle)
				if e != nil || !filepath.IsLocal(rel) {
					return errors.New("download subtitle outside project")
				}
				input.Subtitle = rel
			}
			videoRel, e := filepath.Rel(dir, source)
			if e != nil || !filepath.IsLocal(videoRel) {
				return errors.New("download video outside project")
			}
			checkpoint := downloadCheckpoint{TaskID: t.ID, URL: input.URL, Video: videoRel, Subtitle: input.Subtitle}
			if e = atomicJSON(downloadCheckpointPath(dir, t.ID), checkpoint); e != nil {
				return e
			}
		} else {
			if !filepath.IsLocal(input.Video) {
				return errors.New("invalid upload path")
			}
			source = filepath.Join(dir, input.Video)
		}
	} else if err != nil {
		return err
	}
	if input.Subtitle == "" {
		if path, e := w.Store.Asset(t.ProjectID, "source_srt"); e == nil {
			input.Subtitle, e = filepath.Rel(dir, path)
			if e != nil || !filepath.IsLocal(input.Subtitle) {
				return errors.New("invalid subtitle checkpoint path")
			}
		} else if !errors.Is(e, store.ErrNotFound) {
			return e
		}
	}
	if err = progress("probe", nil); err != nil {
		return err
	}
	stat, err := os.Stat(source)
	if err != nil {
		return err
	}
	maxBytes := w.MaxBytes
	if maxBytes == 0 {
		maxBytes = 4 << 30
	}
	if stat.Size() > maxBytes {
		return errors.New("video exceeds configured size limit")
	}
	info, err := w.Media.Probe(ctx, source)
	if err != nil {
		return err
	}
	maxDuration := w.MaxDuration
	if maxDuration == 0 {
		maxDuration = 7200
	}
	if info.Duration <= 0 || info.Duration > maxDuration {
		return errors.New("video duration exceeds configured limit or is invalid")
	}
	p, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return err
	}
	p.Duration = info.Duration
	p.Width = info.Width
	p.Height = info.Height
	p.HasAudio = &info.HasAudio
	rel, err := filepath.Rel(dir, source)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("source outside project directory")
	}
	cues := []domain.Cue{}
	// Reuse completed ASR checkpoint only when it is valid JSON; never skip malformed checkpoints.
	cuePath := filepath.Join(dir, "subtitles.json")
	if b, e := os.ReadFile(cuePath); e == nil {
		if e = json.Unmarshal(b, &cues); e != nil {
			return fmt.Errorf("invalid subtitles checkpoint: %w", e)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else {
		if input.Subtitle != "" {
			if !filepath.IsLocal(input.Subtitle) {
				return errors.New("invalid subtitle path")
			}
			b, e := os.ReadFile(filepath.Join(dir, input.Subtitle))
			if e != nil {
				return e
			}
			cues, e = media.ParseSRT(b)
			if e != nil {
				return e
			}
		}
		if err = atomicJSON(cuePath, cues); err != nil {
			return err
		}
	}
	for _, cue := range cues {
		if cue.Start < 0 || cue.End <= cue.Start || cue.End > info.Duration+.5 {
			return errors.New("subtitle timestamps exceed source; correct SRT and re-import")
		}
	}
	if err = progress("subtitles", nil); err != nil {
		return err
	}
	p.SubtitleStatus = "missing"
	p.SubtitleSource = ""
	if len(cues) > 0 {
		p.SubtitleStatus = "available"
		p.SubtitleSource = "uploaded"
		if input.URL != "" && input.Subtitle != "uploaded.srt" {
			p.SubtitleSource = "platform"
		}
	}
	plan := domain.LocalPlan(p, 1)
	plan.Options.Instruction = input.Instruction
	p.Plan = &plan
	return w.Store.CompleteImport(t.ID, p, rel, "subtitles.json")
}
func (w *Worker) readCues(pid string) ([]domain.Cue, error) {
	path, err := w.Store.Asset(pid, "subtitles")
	if err != nil {
		return nil, err
	}
	return readCueFile(path)
}

// LLM completion weights are not duration/progress measurements. Keep their
// task progress indeterminate; transitions still record completed steps.
func analysisProgress(progress domain.ProgressFunc) domain.ProgressFunc {
	return func(stage string, _ *float64) error { return progress(stage, nil) }
}
func (w *Worker) analyze(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	if t.WorkflowID != "" {
		return w.produce(ctx, t, dir, progress)
	}
	var o domain.AnalysisOptions
	if err := json.Unmarshal(t.Payload, &o); err != nil {
		return err
	}
	if !o.Confirmed {
		return errors.New("analysis not confirmed")
	}
	if w.Engine != nil && (o.Mode == "subtitle" || (o.Mode == "auto" && !o.AllowVisual)) {
		return w.analyzeWithEngine(ctx, t, dir, o, progress)
	}
	cp := filepath.Join(dir, "analysis", t.ID)
	if err := os.MkdirAll(cp, 0700); err != nil {
		return err
	}
	var drafts []domain.Draft
	var candidates []domain.Candidate
	var err error
	if o.Mode == "visual" || (o.Mode == "auto" && o.AllowVisual) {
		if !o.AllowVisual {
			return errors.New("image transmission not permitted")
		}
		m, e := w.Store.Model("vision")
		if e != nil {
			return e
		}
		source, e := w.Store.Asset(t.ProjectID, "source")
		if e != nil {
			return e
		}
		p, e := w.Store.Project(t.ProjectID)
		if e != nil {
			return e
		}
		frames, e := w.Media.Sample(ctx, source, cp, p.Duration, progress)
		if e != nil {
			return e
		}
		drafts, candidates, err = ai.AnalyzeVisualWithSampler(ctx, ai.New(m), frames, p.Duration, o, cp, analysisProgress(progress),
			func(ctx context.Context, times []float64) ([]domain.Frame, error) {
				return w.Media.SampleAt(ctx, source, cp, times, progress)
			})
		if err == nil {
			err = w.attachVisualReframes(ctx, dir, source, drafts, o.Aspect, progress)
		}
	} else if o.Mode == "subtitle" || o.Mode == "auto" {
		m, e := w.Store.Model("text")
		if e != nil {
			return e
		}
		cues, e := w.ensureTranscript(ctx, t.ProjectID, dir, progress)
		if e != nil {
			return e
		}
		if len(cues) == 0 {
			return errors.New("No speech subtitles available; use visual analysis or upload an SRT")
		}
		drafts, candidates, err = ai.AnalyzeText(ctx, ai.New(m), cues, o, cp, analysisProgress(progress))
	} else {
		return errors.New("invalid analysis mode")
	}
	if err != nil {
		return err
	}
	if len(drafts) == 0 {
		return errors.New("model produced no valid drafts")
	}
	if err = progress("drafts", nil); err != nil {
		return err
	}
	return w.Store.CompleteAnalysis(t.ID, t.ProjectID, drafts, candidates)
}
func (w *Worker) export(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	var input domain.ExportPayload
	if err := json.Unmarshal(t.Payload, &input); err != nil {
		return err
	}
	p, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return err
	}
	if err = input.Draft.Validate(p.Duration); err != nil {
		return err
	}
	out := filepath.Join(dir, "exports", t.ID)
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	output := filepath.Join(out, "output.mp4")
	if _, err = os.Stat(output); err == nil {
		if _, err = w.Media.Probe(ctx, output); err != nil {
			return fmt.Errorf("existing export checkpoint invalid: %w", err)
		}
		return w.Store.CompleteExport(t.ID, t.ProjectID, input.Draft)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, err := w.Store.Asset(t.ProjectID, "source")
	if err != nil {
		return err
	}
	if input.Draft.Origin == "python-engine" && w.Engine != nil {
		if err = w.exportWithEngine(ctx, t, dir, source, output, input.Draft, progress); err != nil {
			return err
		}
		return w.Store.CompleteExport(t.ID, t.ProjectID, input.Draft)
	}
	var cues []domain.Cue
	if input.Draft.Subtitles {
		cues, err = w.ensureTranscript(ctx, t.ProjectID, dir, progress)
		if err != nil {
			return err
		}
		if len(cues) == 0 {
			return errors.New("No subtitles are available; disable added subtitles or provide an SRT")
		}
	}
	renderDraft := input.Draft
	if _, err = w.Media.Render(ctx, source, out, output, renderDraft, cues, progress); err != nil {
		return err
	}
	return w.Store.CompleteExport(t.ID, t.ProjectID, input.Draft)
}
