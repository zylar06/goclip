package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
)

func (w *Worker) ensureTranscript(ctx context.Context, pid, dir string, progress domain.ProgressFunc) ([]domain.Cue, error) {
	if err := w.Store.RequireSourceReady(pid); err != nil {
		return nil, fmt.Errorf("finish or retry source import before preparing subtitles: %w", err)
	}
	p, err := w.Store.Project(pid)
	if err != nil {
		return nil, err
	}
	cues, err := w.readCues(pid)
	if err == nil && len(cues) > 0 {
		if err = validateCues(cues, p.Duration); err != nil {
			return nil, err
		}
		if p.SubtitleStatus != "available" {
			path, e := w.Store.Asset(pid, "subtitles")
			if e != nil {
				return nil, e
			}
			rel, e := filepath.Rel(dir, path)
			if e != nil {
				return nil, e
			}
			source := p.SubtitleSource
			if source == "" {
				source = "existing"
			}
			if filepath.Base(path) == "subtitles-asr.json" {
				source = "asr"
			}
			if e = w.Store.CompleteTranscript(pid, rel, source, len(cues)); e != nil {
				return nil, e
			}
		}
		return cues, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	// A fully written ASR checkpoint can outlive a failed/cancelled DB commit.
	// Keep it separate from uploaded evidence, validate it, then publish atomically.
	const asrRel = "subtitles-asr.json"
	cues, err = readCueFile(filepath.Join(dir, asrRel))
	if err == nil {
		if err = validateCues(cues, p.Duration); err != nil {
			return nil, err
		}
		return cues, w.Store.CompleteTranscript(pid, asrRel, "asr", len(cues))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if p.HasAudio != nil && !*p.HasAudio {
		return []domain.Cue{}, nil
	}
	source, err := w.Store.Asset(pid, "source")
	if err != nil {
		return nil, err
	}
	if err = progress("transcribe", nil); err != nil {
		return nil, err
	}
	cues, err = w.Media.Transcribe(ctx, source, dir, progress)
	if err != nil {
		p.SubtitleStatus = "failed"
		if saveErr := w.Store.UpdateProject(p); saveErr != nil {
			return nil, fmt.Errorf("transcription failed; recording its state also failed: %w", saveErr)
		}
		return nil, err
	}
	if err = validateCues(cues, p.Duration); err != nil {
		return nil, err
	}
	if err = atomicJSON(filepath.Join(dir, asrRel), cues); err != nil {
		return nil, err
	}
	if err = w.Store.CompleteTranscript(pid, asrRel, "asr", len(cues)); err != nil {
		return nil, err
	}
	return cues, nil
}

func validateCues(cues []domain.Cue, duration float64) error {
	for _, cue := range cues {
		if cue.Start < 0 || cue.End <= cue.Start || cue.End > duration+.5 {
			return errors.New("subtitle timestamps exceed source")
		}
	}
	return nil
}

func readCueFile(path string) ([]domain.Cue, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return nil, err
	}
	if len(data) > 10<<20 {
		return nil, errors.New("subtitle checkpoint exceeds 10 MiB")
	}
	var cues []domain.Cue
	err = json.Unmarshal(data, &cues)
	return cues, err
}

type routeResult struct {
	drafts     []domain.Draft
	candidates []domain.Candidate
	err        error
}

func (w *Worker) produce(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	wf, err := w.Store.Workflow(t.WorkflowID)
	if err != nil {
		return err
	}
	o := wf.Options
	if !o.Confirmed {
		return errors.New("production was not confirmed")
	}
	p, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return err
	}
	cp := filepath.Join(dir, "analysis", t.ID)
	if err = os.MkdirAll(cp, 0700); err != nil {
		return err
	}
	cache := map[string]routeResult{}
	analyze := func(mode string) routeResult {
		if r, ok := cache[mode]; ok {
			return r
		}
		r := routeResult{}
		options := o
		options.Mode = mode
		routeDir := filepath.Join(cp, mode)
		kind := "text"
		if mode == "visual" {
			kind = "vision"
			options.Goals = []string{"highlight"}
		} else {
			options.Goals = []string{"content"}
		}
		model, e := w.Store.Model(kind)
		if e != nil {
			r.err = e
			cache[mode] = r
			return r
		}
		if mode == "subtitle" {
			cues, e := w.ensureTranscript(ctx, t.ProjectID, dir, progress)
			if e != nil {
				r.err = e
			} else if len(cues) == 0 {
				r.err = &ai.Error{Code: ai.CodeNoHighlights, Message: "No usable speech subtitles; choose visual analysis or provide an SRT."}
			} else {
				r.drafts, r.candidates, r.err = ai.AnalyzeText(ctx, ai.New(model), cues, options, routeDir, analysisProgress(progress))
			}
		} else {
			if !o.AllowVisual {
				r.err = errors.New("image transmission not permitted")
			} else {
				source, e := w.Store.Asset(t.ProjectID, "source")
				if e != nil {
					r.err = e
				} else {
					frames, e := w.Media.Sample(ctx, source, routeDir, p.Duration, progress)
					if e != nil {
						r.err = e
					} else {
						r.drafts, r.candidates, r.err = ai.AnalyzeVisualWithSampler(ctx, ai.New(model), frames, p.Duration, options, routeDir, analysisProgress(progress),
							func(ctx context.Context, times []float64) ([]domain.Frame, error) {
								return w.Media.SampleAt(ctx, source, routeDir, times, progress)
							})
					}
				}
			}
		}
		cache[mode] = r
		return r
	}
	var drafts []domain.Draft
	var candidates []domain.Candidate
	results := []domain.GoalResult{}
	retryable := false
	for _, g := range wf.Goals {
		if err = ctx.Err(); err != nil {
			return err
		}
		// A retry is for failed goals only, not another paid production of successes.
		if len(g.DraftIDs) > 0 {
			continue
		}
		if err = progress("produce-"+g.Goal, nil); err != nil {
			return err
		}
		mode := o.Mode
		if g.Goal == "content" {
			mode = "subtitle"
		}
		r := analyze(mode)
		selected := r.drafts
		goalErr := r.err
		if goalErr == nil && g.Goal != "content" {
			selected = highlightDrafts(r.drafts, r.candidates)
		}
		if goalErr == nil && g.Goal == "promo" {
			model, e := w.Store.Model("text")
			if e != nil {
				goalErr = e
			} else {
				items := []domain.Candidate{}
				for _, d := range selected {
					if len(d.Scenes) > 0 {
						items = append(items, domain.Candidate{Scene: d.Scenes[0], Score: 1, Kind: "content"})
					}
				}
				// Upstream visual promos use the best complete event with different hooks.
				if mode == "visual" && len(items) > 1 {
					items = items[:1]
				}
				selected, goalErr = ai.MakePromos(ctx, ai.New(model), items, o, filepath.Join(cp, "promo-"+mode), analysisProgress(progress))
			}
		}
		if goalErr == nil && len(selected) == 0 {
			goalErr = &ai.Error{Code: ai.CodeNoHighlights, Message: "No valid complete clips for this goal."}
		}
		result := domain.GoalResult{Goal: g.Goal, Status: "completed", DraftIDs: []string{}, ExportTaskIDs: []string{}}
		if goalErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			result.Status = "failed"
			result.Error = goalErr.Error()
			var ae *ai.Error
			if errors.As(goalErr, &ae) {
				retryable = retryable || ae.Retryable
			} else {
				retryable = true
			}
		} else {
			for i, d := range selected {
				hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d", wf.ID, g.Goal, d.ID, i)))
				d.ID = fmt.Sprintf("%x", hash[:16])
				d.Goal = g.Goal
				d.Subtitles = o.BurnSubtitles
				d.Origin = mode + "-" + g.Goal
				if g.Goal == "content" {
					d.Hook = ""
					enabled := false
					d.TitleEnabled = &enabled
				} // plain content cuts preserve the original picture
				drafts = append(drafts, d)
				result.DraftIDs = append(result.DraftIDs, d.ID)
			}
		}
		results = append(results, result)
	}
	// Candidate IDs are mode-local; prefix when combining independent analyses.
	for _, mode := range []string{"subtitle", "visual"} {
		if r, ok := cache[mode]; ok && r.err == nil {
			for _, c := range r.candidates {
				c.ID = mode + "-" + c.ID
				candidates = append(candidates, c)
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return w.Store.CompleteProductionAnalysis(t.ID, drafts, candidates, results, retryable)
}

func highlightDrafts(drafts []domain.Draft, candidates []domain.Candidate) []domain.Draft {
	scores := map[string]float64{}
	for _, c := range candidates {
		scores[c.ID] = c.Score
	}
	out := append([]domain.Draft(nil), drafts...)
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Scenes) == 0 || len(out[j].Scenes) == 0 {
			return len(out[i].Scenes) > len(out[j].Scenes)
		}
		return scores[out[i].Scenes[0].ID] > scores[out[j].Scenes[0].ID]
	})
	kept := []domain.Draft{}
	for _, d := range out {
		if len(d.Scenes) == 0 {
			continue
		}
		s := d.Scenes[0]
		duplicate := false
		for _, other := range kept {
			o := other.Scenes[0]
			overlap := math.Min(s.End, o.End) - math.Max(s.Start, o.Start)
			if overlap > 0 && overlap/math.Min(s.End-s.Start, o.End-o.Start) > .8 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, d)
		}
		if len(kept) == 6 {
			break
		}
	}
	return kept
}

func (w *Worker) inspectSource(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	var options domain.InspectOptions
	if err := json.Unmarshal(t.Payload, &options); err != nil {
		return err
	}
	p, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return err
	}
	p, err = w.Store.ProjectEvidence(p)
	if err != nil {
		return err
	}
	rev := 1
	if p.Plan != nil {
		rev = p.Plan.Revision + 1
	}
	plan := domain.LocalPlan(p, rev)
	if p.Plan != nil {
		plan.Options = p.Plan.Options
		plan.Options.Confirmed = false
		plan.AutoExport = p.Plan.AutoExport
	}
	if options.AllowVisual {
		if !options.Confirmed {
			return errors.New("visual screening was not confirmed")
		}
		model, err := w.Store.Model("vision")
		if err != nil {
			return err
		}
		source, err := w.Store.Asset(t.ProjectID, "source")
		if err != nil {
			return err
		}
		times := []float64{0, math.Max(0, p.Duration-.1) / 3, math.Max(0, p.Duration-.1) * 2 / 3, math.Max(0, p.Duration-.1)}
		frames, err := w.Media.SampleAt(ctx, source, dir, times, progress)
		if err != nil {
			return err
		}
		screen, err := ai.Screen(ctx, ai.New(model), frames, p.Duration)
		if err != nil {
			return err
		}
		plan.Reason = screen.Reason
		plan.SuggestedGoals = screen.Goals
		// A recommendation does not grant production image consent.
		plan.Options.Mode = screen.Mode
		plan.Options.Goals = screen.Goals
		plan.Options.AllowVisual = false
	}
	p.Plan = &plan
	return w.Store.CompleteInspection(t.ID, p)
}

func (w *Worker) preparePreview(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	source, err := w.Store.Asset(t.ProjectID, "source")
	if err != nil {
		return err
	}
	out := filepath.Join(dir, "preview", t.ID)
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	output := filepath.Join(out, "preview.mp4")
	if _, err = os.Stat(output); err == nil {
		// Recover publication before the DB commit without re-encoding.
		if _, err = w.Media.VerifyPreview(ctx, source, output); err != nil {
			return fmt.Errorf("existing preview checkpoint invalid: %w", err)
		}
		rel, err := filepath.Rel(dir, output)
		if err != nil || !filepath.IsLocal(rel) {
			return errors.New("preview path outside project")
		}
		return w.Store.CompletePreview(t.ID, t.ProjectID, rel)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err = w.Media.CompatiblePreview(ctx, source, out, output, progress); err != nil {
		return err
	}
	rel, err := filepath.Rel(dir, output)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("preview path outside project")
	}
	return w.Store.CompletePreview(t.ID, t.ProjectID, rel)
}
