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
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

type Worker struct {
	Store       *store.Store
	Media       *media.Tools
	MaxDuration float64
	MaxBytes    int64
	TaskTimeout time.Duration
	// Execute is a test seam. Production uses execute.
	Execute func(context.Context, domain.Task, domain.ProgressFunc) error
}

func (w *Worker) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
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
	timeout := w.TaskTimeout
	if timeout == 0 {
		timeout = 6 * time.Hour
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	stopped := make(chan struct{})
	monitorDone := make(chan struct{})
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
	current, loadErr := w.Store.Task(t.ID)
	if loadErr != nil {
		return loadErr
	}
	if current.Status != "running" {
		return nil
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
	p, e := w.Store.Project(t.ProjectID)
	if e != nil {
		return e
	}
	if status == "completed" {
		p.Error = ""
		switch t.Kind {
		case "import":
			p.Status = "source_ready"
		case "analyze":
			p.Status = "drafts_ready"
		case "export":
			p.Status = "exported"
		}
	} else {
		p.Error = message
		p.Status = status
	}
	if e = w.Store.UpdateProject(p); e != nil {
		return e
	}
	slog.Info("task finished", "task", t.ID, "status", status)
	return nil
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
			video, subtitle, e := w.Media.Download(ctx, input.URL, dir, cookieFile, progress)
			if e != nil {
				return e
			}
			source = video
			if subtitle != "" {
				rel, e := filepath.Rel(dir, subtitle)
				if e != nil || !filepath.IsLocal(rel) {
					return errors.New("download subtitle outside project")
				}
				input.Subtitle = rel
				if e = w.Store.SetAsset(t.ProjectID, "source_srt", rel); e != nil {
					return e
				}
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
	if err = w.Store.UpdateProject(p); err != nil {
		return err
	}
	rel, err := filepath.Rel(dir, source)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("source outside project directory")
	}
	if err = w.Store.SetAsset(t.ProjectID, "source", rel); err != nil {
		return err
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
		} else if info.HasAudio {
			cues, err = w.Media.Transcribe(ctx, source, dir, progress)
			if err != nil {
				return err
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
	return w.Store.SetAsset(t.ProjectID, "subtitles", "subtitles.json")
}
func (w *Worker) readCues(pid string) ([]domain.Cue, error) {
	path, err := w.Store.Asset(pid, "subtitles")
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cues []domain.Cue
	err = json.Unmarshal(b, &cues)
	return cues, err
}

// LLM completion weights are not duration/progress measurements. Keep their
// task progress indeterminate; transitions still record completed steps.
func analysisProgress(progress domain.ProgressFunc) domain.ProgressFunc {
	return func(stage string, _ *float64) error { return progress(stage, nil) }
}
func (w *Worker) analyze(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	var o domain.AnalysisOptions
	if err := json.Unmarshal(t.Payload, &o); err != nil {
		return err
	}
	if !o.Confirmed {
		return errors.New("analysis not confirmed")
	}
	cp := filepath.Join(dir, "analysis", t.ID)
	if err := os.MkdirAll(cp, 0700); err != nil {
		return err
	}
	var drafts []domain.Draft
	var candidates []domain.Candidate
	var err error
	if o.Mode == "visual" {
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
		drafts, candidates, err = ai.AnalyzeVisual(ctx, ai.New(m), frames, p.Duration, o, cp, analysisProgress(progress))
	} else if o.Mode == "subtitle" {
		m, e := w.Store.Model("text")
		if e != nil {
			return e
		}
		cues, e := w.readCues(t.ProjectID)
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
	return w.Store.SaveAnalysis(t.ID, t.ProjectID, drafts, candidates)
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
		return w.Store.SaveExport(t.ID, t.ProjectID, input.Draft)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	source, err := w.Store.Asset(t.ProjectID, "source")
	if err != nil {
		return err
	}
	cues, err := w.readCues(t.ProjectID)
	if err != nil {
		return err
	}
	renderDraft := input.Draft
	if renderDraft.Language != "source" {
		translation := filepath.Join(out, "translation.json")
		var saved struct {
			Draft domain.Draft `json:"draft"`
			Cues  []domain.Cue `json:"cues"`
		}
		if b, e := os.ReadFile(translation); e == nil {
			if e = json.Unmarshal(b, &saved); e != nil {
				return fmt.Errorf("translation checkpoint: %w", e)
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		} else {
			if err = progress("translation", nil); err != nil {
				return err
			}
			m, e := w.Store.Model("text")
			if e != nil {
				return e
			}
			saved.Draft, saved.Cues, e = ai.Translate(ctx, ai.New(m), renderDraft, cues)
			if e != nil {
				return e
			}
			if e = atomicJSON(translation, saved); e != nil {
				return e
			}
		}
		renderDraft, cues = saved.Draft, saved.Cues
	}
	if _, err = w.Media.Render(ctx, source, out, output, renderDraft, cues, progress); err != nil {
		return err
	}
	return w.Store.SaveExport(t.ID, t.ProjectID, input.Draft)
}
