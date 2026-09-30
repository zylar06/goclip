package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/engine"
)

type engineDraftArtifact struct {
	CropPath string        `json:"crop_path"`
	Words    []engine.Word `json:"words"`
	Start    float64       `json:"start"`
	End      float64       `json:"end"`
	Ratio    string        `json:"ratio"`
}

// engineCandidates maps validated engine output into Go records and preserves
// per-draft crop/subtitle data in the project workspace for later export.
func (w *Worker) engineCandidates(ctx context.Context, t domain.Task, dir string, options domain.AnalysisOptions, progress domain.ProgressFunc) ([]domain.Draft, []domain.Candidate, error) {
	if w.Engine == nil {
		return nil, nil, errors.New("python engine is not configured")
	}
	model, err := w.Store.Model("text")
	if err != nil {
		return nil, nil, err
	}
	source, err := w.Store.Asset(t.ProjectID, "source")
	if err != nil {
		return nil, nil, err
	}
	project, err := w.Store.Project(t.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	workspace := filepath.Join(dir, "engine", t.ID)
	projectDir, err := w.Store.ProjectDir(t.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	ratio := engineRatio(options.Aspect)
	result, err := w.Engine.Run(ctx, engine.Request{
		JobID: t.ID, Source: source, Workspace: workspace, Duration: project.Duration,
		Settings: map[string]any{
			"base_url": model.BaseURL, "model": model.Model, "api_key": model.APIKey,
			"min_duration_s": 20, "max_duration_s": 90, "max_clips": 10,
			"ratio": ratio,
		},
	}, func(stage string, percent float64) error {
		return progress("engine-"+stage, &percent)
	})
	if err != nil {
		return nil, nil, err
	}
	if len(result.Candidates) == 0 {
		return nil, nil, errors.New("python engine produced no highlight candidates")
	}
	candidates := make([]domain.Candidate, 0, len(result.Candidates))
	drafts := make([]domain.Draft, 0, len(result.Candidates))
	for _, item := range result.Candidates {
		if item.ID == "" || item.Start < 0 || item.End <= item.Start || item.End > project.Duration+.001 {
			return nil, nil, errors.New("python engine returned an invalid highlight boundary")
		}
		label := item.Title
		if label == "" {
			label = "片段"
		}
		scene := domain.Scene{ID: item.ID, Label: label, Start: item.Start, End: item.End, Evidence: item.Reason}
		candidates = append(candidates, domain.Candidate{Scene: scene, Score: item.Score / 100, Kind: "other"})
		draft := domain.NewDraft(label, []domain.Scene{scene})
		draft.Hook, draft.Origin, draft.Goal, draft.Aspect, draft.Subtitles = item.Hook, "python-engine", "content", options.Aspect, true
		cropPath, err := filepath.Abs(item.CropPath)
		if err != nil || !isEnginePath(workspace, cropPath) {
			return nil, nil, errors.New("python engine returned an unsafe crop path")
		}
		artifact := engineDraftArtifact{CropPath: cropPath, Words: result.Words, Start: item.Start, End: item.End, Ratio: ratio}
		artifactPath := filepath.Join(projectDir, "engine", "drafts", draft.ID+".json")
		if err = os.MkdirAll(filepath.Dir(artifactPath), 0700); err != nil {
			return nil, nil, err
		}
		data, err := json.Marshal(artifact)
		if err != nil {
			return nil, nil, err
		}
		if err = os.WriteFile(artifactPath, data, 0600); err != nil {
			return nil, nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, candidates, nil
}

func engineRatio(aspect string) string {
	switch aspect {
	case "portrait":
		return "9:16"
	case "landscape":
		return "16:9"
	default:
		return "16:9"
	}
}

// attachVisualReframes gives Go/Qwen-VL selected drafts the same local
// MediaPipe face-tracked crop path used by the Python highlight engine.
func (w *Worker) attachVisualReframes(ctx context.Context, dir, source string, drafts []domain.Draft, aspect string, progress domain.ProgressFunc) error {
	if w.Engine == nil || aspect != "portrait" {
		return nil
	}
	for i := range drafts {
		d := &drafts[i]
		workspace := filepath.Join(dir, "engine", "visual", d.ID)
		result, err := w.Engine.Run(ctx, engine.Request{Operation: "reframe", JobID: d.ID, Source: source, Workspace: workspace,
			Duration: d.Scenes[0].End - d.Scenes[0].Start, Start: d.Scenes[0].Start, End: d.Scenes[0].End,
			Settings: map[string]any{"ratio": "9:16"}}, func(stage string, percent float64) error {
			return progress("engine-"+stage, &percent)
		})
		if err != nil {
			return err
		}
		cropPath, err := filepath.Abs(result.Output)
		if err != nil || !isEnginePath(dir, cropPath) {
			return errors.New("python engine returned an unsafe visual crop path")
		}
		artifact := engineDraftArtifact{CropPath: cropPath, Start: d.Scenes[0].Start, End: d.Scenes[0].End, Ratio: "9:16"}
		artifactPath := filepath.Join(dir, "engine", "drafts", d.ID+".json")
		if err = os.MkdirAll(filepath.Dir(artifactPath), 0700); err != nil {
			return err
		}
		data, err := json.Marshal(artifact)
		if err != nil {
			return err
		}
		if err = os.WriteFile(artifactPath, data, 0600); err != nil {
			return err
		}
		d.Origin = "python-engine"
		d.Subtitles = false
	}
	return nil
}

func isEnginePath(workspace, path string) bool {
	rel, err := filepath.Rel(workspace, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && len(rel) > 0
}

func (w *Worker) exportWithEngine(ctx context.Context, t domain.Task, dir, source, output string, draft domain.Draft, progress domain.ProgressFunc) error {
	artifactPath := filepath.Join(dir, "engine", "drafts", draft.ID+".json")
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return fmt.Errorf("python-engine draft data unavailable; rerun analysis: %w", err)
	}
	var artifact engineDraftArtifact
	if err = json.Unmarshal(data, &artifact); err != nil || !isEnginePath(dir, artifact.CropPath) {
		return errors.New("invalid python-engine draft data")
	}
	if len(draft.Scenes) != 1 || draft.Scenes[0].Start != artifact.Start || draft.Scenes[0].End != artifact.End || engineRatio(draft.Aspect) != artifact.Ratio {
		return errors.New("automatic framing must be regenerated after changing the cut or aspect ratio")
	}
	workspace := dir
	result, err := w.Engine.Run(ctx, engine.Request{Operation: "export", JobID: t.ID, Source: source, Workspace: workspace,
		Duration: draft.Scenes[0].End - draft.Scenes[0].Start, Start: draft.Scenes[0].Start, End: draft.Scenes[0].End, Settings: map[string]any{},
		// The Go editor's "original" aspect is intentionally preserved in the
		// draft, but the Python engine accepts only concrete output ratios. Use
		// the ratio captured when automatic framing was generated.
		Export: &engine.ExportRequest{Destination: output, CropPath: artifact.CropPath, CaptionStyle: "bold_pop", Ratio: artifact.Ratio, Words: artifact.Words},
	}, func(stage string, percent float64) error { return progress("engine-"+stage, &percent) })
	if err != nil {
		return err
	}
	if result.Output != output {
		return errors.New("python engine returned an unexpected export path")
	}
	_, err = w.Media.Probe(ctx, output)
	return err
}

func moveEngineArtifact(dir, fromID, toID string) error {
	if fromID == toID {
		return nil
	}
	base := filepath.Join(dir, "engine", "drafts")
	from, to := filepath.Join(base, fromID+".json"), filepath.Join(base, toID+".json")
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("move automatic framing data: %w", err)
	}
	return nil
}

func (w *Worker) analyzeWithEngine(ctx context.Context, t domain.Task, dir string, options domain.AnalysisOptions, progress domain.ProgressFunc) error {
	drafts, candidates, err := w.engineCandidates(ctx, t, dir, options, progress)
	if err != nil {
		return err
	}
	return w.Store.CompleteAnalysis(t.ID, t.ProjectID, drafts, candidates)
}
