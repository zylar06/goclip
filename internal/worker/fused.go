package worker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/media"
)

// produceFused is the single-route highlight workflow. Subtitle and visual
// evidence are collected independently, then merged locally before drafts are
// persisted. Keeping the fusion here makes it impossible for one provider to
// silently overwrite the other provider's evidence.
func (w *Worker) produceFused(ctx context.Context, t domain.Task, dir string, progress domain.ProgressFunc) error {
	wf, err := w.Store.Workflow(t.WorkflowID)
	if err != nil {
		return err
	}
	p, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return err
	}
	o := wf.Options
	o.Mode = "subtitle"
	o.Goals = []string{"highlight"}
	cp := filepath.Join(dir, "analysis", t.ID, "fused")
	if err = osMkdir(cp); err != nil {
		return err
	}

	textModel, err := w.Store.Model("text")
	if err != nil {
		return err
	}
	cues, textErr := w.ensureTranscript(ctx, t.ProjectID, dir, progress)
	var textDrafts []domain.Draft
	var textCandidates []domain.Candidate
	if textErr == nil && len(cues) > 0 {
		textDrafts, textCandidates, textErr = ai.AnalyzeText(ctx, ai.New(textModel), cues, o, filepath.Join(cp, "text"), analysisProgress(progress))
	}

	visionModel, err := w.Store.Model("vision")
	if err != nil {
		return err
	}
	source, err := w.Store.Asset(t.ProjectID, "source")
	if err != nil {
		return err
	}
	times := mediaEverySecondTimes(p.Duration)
	visualDrafts := []domain.Draft{}
	visualCandidates := []domain.Candidate{}
	batchCount := (len(times) + 59) / 60
	var visualErr error
	for start := 0; start < len(times); start += 60 {
		end := start + 60
		if end > len(times) {
			end = len(times)
		}
		batch := times[start:end]
		batchNo := start/60 + 1
		batchDir := filepath.Join(cp, fmt.Sprintf("visual-%02d", batchNo))
		frames, e := w.Media.SampleAt(ctx, source, batchDir, batch, func(stage string, percent *float64) error {
			if percent == nil {
				return progress(fmt.Sprintf("sample-%02d/%02d", batchNo, batchCount), nil)
			}
			return progress(fmt.Sprintf("sample-%02d/%02d", batchNo, batchCount), percent)
		})
		if e != nil {
			visualErr = e
			break
		}
		vd, vc, e := ai.AnalyzeVisualWithSampler(ctx, ai.New(visionModel), frames, p.Duration, func() domain.AnalysisOptions { x := o; x.Mode = "visual"; x.Goals = []string{"highlight"}; return x }(), filepath.Join(batchDir, "analysis"), analysisProgress(func(stage string, percent *float64) error {
			return progress(fmt.Sprintf("visual-%02d/%02d-%s", batchNo, batchCount, stage), percent)
		}), func(ctx context.Context, requested []float64) ([]domain.Frame, error) {
			return w.Media.SampleAt(ctx, source, filepath.Join(batchDir, "refine"), requested, nil)
		})
		if e != nil {
			visualErr = e
			break
		}
		visualDrafts = append(visualDrafts, vd...)
		visualCandidates = append(visualCandidates, vc...)
	}
	if textErr != nil && visualErr != nil {
		return fmt.Errorf("subtitle analysis: %v; visual analysis: %w", textErr, visualErr)
	}
	fused := ai.FuseCandidates(textCandidates, visualCandidates)
	if len(fused) == 0 {
		return fmt.Errorf("no highlight candidates from subtitle or visual evidence")
	}
	drafts := fusedDrafts(fused, append(textDrafts, visualDrafts...), o)
	if err = w.attachVisualReframes(ctx, dir, source, drafts, o.Aspect, progress); err != nil {
		return err
	}
	for i := range drafts {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", wf.ID, drafts[i].Scenes[0].ID, i)))
		drafts[i].ID = fmt.Sprintf("%x", hash[:16])
		drafts[i].Goal = "highlight"
		drafts[i].Origin = "fused-highlight"
	}
	result := domain.GoalResult{Goal: "highlight", Status: "completed", DraftIDs: make([]string, 0, len(drafts))}
	for _, d := range drafts {
		result.DraftIDs = append(result.DraftIDs, d.ID)
	}
	return w.Store.CompleteProductionAnalysis(t.ID, drafts, fused, []domain.GoalResult{result}, false)
}

func osMkdir(path string) error { return os.MkdirAll(path, 0700) }

func mediaEverySecondTimes(duration float64) []float64 { return media.EverySecondTimes(duration) }

func fusedDrafts(candidates []domain.Candidate, originals []domain.Draft, opts domain.AnalysisOptions) []domain.Draft {
	out := make([]domain.Draft, 0, len(candidates))
	for _, c := range candidates {
		best := -1
		bestOverlap := 0.0
		for i := range originals {
			if len(originals[i].Scenes) == 0 {
				continue
			}
			s := originals[i].Scenes[0]
			overlap := math.Max(0, math.Min(c.End, s.End)-math.Max(c.Start, s.Start))
			if overlap > bestOverlap {
				best, bestOverlap = i, overlap
			}
		}
		var d domain.Draft
		if best >= 0 {
			d = originals[best]
		} else {
			d = domain.NewDraft(c.Label, nil)
		}
		d.Title = c.Label
		d.Scenes = []domain.Scene{c.Scene}
		d.Subtitles = opts.BurnSubtitles
		d.Goal = "highlight"
		out = append(out, d)
	}
	return out
}
