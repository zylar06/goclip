package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
)

func skippedReviewModel() *scriptedModel {
	return &scriptedModel{replies: map[string]string{
		"visual": twoPlayableScan,
		"refine": `{"events":[{"id":"wrong","label":"Other","start":21,"end":34,"evidence":"unrelated","kind":"gameplay","score":0.9,"frame_times":[30]}]}`,
		"titles": `[{"id":"weak","title":"Weaker","hook":""}]`,
	}}
}

func improveReview(model *scriptedModel) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.replies["refine"] = strings.ReplaceAll(model.replies["refine"], `"wrong"`, `"strong"`)
	model.fail = ""
}

func TestVisualDurableSkipReplayNeverRepeatsReview(t *testing.T) {
	for _, dense := range []bool{false, true} {
		t.Run(map[bool]string{false: "supplied-frames", true: "fresh-dense-frames"}[dense], func(t *testing.T) {
			model := skippedReviewModel()
			client, dir, frames := model.client(t), t.TempDir(), fixtureFrames(t)
			var sample func(context.Context, []float64) ([]domain.Frame, error)
			samples := 0
			if dense {
				path := tinyImage(t, "png")
				sample = func(_ context.Context, times []float64) ([]domain.Frame, error) {
					samples++
					out := make([]domain.Frame, len(times))
					for i, tm := range times {
						out[i] = domain.Frame{Time: tm, Path: path}
					}
					return out, nil
				}
			}
			first, candidates, err := AnalyzeVisualWithSampler(context.Background(), client, frames, 60, visualOptions(), dir, nil, sample)
			if err != nil || len(first) != 1 || first[0].Scenes[0].ID != "weak" {
				t.Fatal(first, err)
			}
			before := model.count()
			improveReview(model) // A retry would now succeed, invalidating the old downstream chain.
			stages, progress := progressRecorder(t)
			replayed, replayCandidates, err := AnalyzeVisualWithSampler(context.Background(), client, frames, 60, visualOptions(), dir, progress, sample)
			if err != nil || model.count() != before || !reflect.DeepEqual(first, replayed) || !reflect.DeepEqual(candidates, replayCandidates) {
				t.Fatalf("durable skip replay changed results or billed again: calls=%d -> %d err=%v", before, model.count(), err)
			}
			found := false
			for _, name := range *stages {
				found = found || name == refineSkippedStage
			}
			if !found || (dense && samples != 2) {
				t.Fatal("replay lost the skip notification or dense fingerprint verification", *stages, samples)
			}
		})
	}
}

func TestVisualDurableSkipSurvivesDownstreamFailure(t *testing.T) {
	for _, stop := range []string{"titles", "skip-callback"} {
		t.Run(stop, func(t *testing.T) {
			model := skippedReviewModel()
			client, dir, frames := model.client(t), t.TempDir(), fixtureFrames(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var progress domain.ProgressFunc
			wantCalls := 3
			if stop == "titles" {
				model.setFailure("titles")
			} else {
				wantCalls = 2
				progress = func(name string, _ *float64) error {
					if name == refineSkippedStage {
						cancel()
						return context.Canceled
					}
					return nil
				}
			}
			_, _, err := AnalyzeVisual(ctx, client, frames, 60, visualOptions(), dir, progress)
			if err == nil || model.count() != wantCalls {
				t.Fatal("expected downstream failure", model.count(), err)
			}
			if stop == "skip-callback" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost", err)
			}
			improveReview(model)
			drafts, _, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
			if err != nil || len(drafts) != 1 || drafts[0].Scenes[0].ID != "weak" || model.count() != wantCalls+1 {
				t.Fatalf("retry must request only unfinished titles, preserving skip: drafts=%v calls=%d err=%v", drafts, model.count(), err)
			}
		})
	}
}

func TestVisualDurableSkipCorruptionFailsBeforeBilling(t *testing.T) {
	for _, data := range []string{
		`{`,
		`{"status":"unknown","event_id":"strong"}`,
		`{"status":"skipped","event_id":"different"}`,
		`{"status":"skipped","event_id":"strong","result":{"events":[]}}`,
		`{"status":"accepted","event_id":"strong"}`,
	} {
		t.Run(data, func(t *testing.T) {
			model := skippedReviewModel()
			client, dir, frames := model.client(t), t.TempDir(), fixtureFrames(t)
			if _, _, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, refineStage+".json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("skip decision must be checkpointed", err)
			}
			if data == "{" {
				mustWrite(t, path, []byte(data))
			} else {
				var cp checkpoint
				if err := json.Unmarshal(raw, &cp); err != nil {
					t.Fatal(err)
				}
				cp.Data = json.RawMessage(data)
				cp.DataHash = digest(cp.Data) // Exercise semantic checks, not only the checksum.
				raw, err = json.Marshal(cp)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, path, raw)
			}
			_, _, err = AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
			ae := assertCode(t, err, CodeInvalidResponse)
			if ae.Stage != refineStage || rejectedRefinement(err) || model.count() != 3 {
				t.Fatal("corrupt skip was retried or treated as model degradation", err, model.count())
			}
		})
	}
}

func TestVisualDurableSkipDoesNotPersistOtherFailures(t *testing.T) {
	for _, failure := range []string{"auth", "no-highlights", "no-fallback"} {
		t.Run(failure, func(t *testing.T) {
			model := skippedReviewModel()
			wantCode := CodeInvalidResponse
			switch failure {
			case "auth":
				model.setFailure("refine")
				wantCode = CodeAuth
			case "no-highlights":
				model.replies["refine"] = `{"events":[]}`
				wantCode = CodeNoHighlights
			case "no-fallback":
				model.replies["visual"] = scanReply
			}
			dir := t.TempDir()
			_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), dir, nil)
			assertCode(t, err, wantCode)
			if _, err := os.Stat(filepath.Join(dir, refineStage+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failure became a durable success/skip", err)
			}
			if model.count() != 2 {
				t.Fatal("failure triggered extra requests", model.count())
			}
		})
	}
}

func TestVisualVersionFiveCheckpointFailsBeforeBilling(t *testing.T) {
	model := visualModel()
	client, frames, dir := model.client(t), fixtureFrames(t), t.TempDir()
	opts, err := normalizeOptions(visualOptions())
	if err != nil {
		t.Fatal(err)
	}
	_, hashes, err := prepareFrames(context.Background(), frames, 60)
	if err != nil {
		t.Fatal(err)
	}
	old, err := newRunner(context.Background(), client, struct {
		PromptVersion string                 `json:"prompt_version"`
		Frames        []frameFingerprint     `json:"frames"`
		Duration      float64                `json:"duration"`
		Options       domain.AnalysisOptions `json:"options"`
		DenseReview   bool                   `json:"dense_review"`
	}{"general-visual-5", hashes, 60, opts, false}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var scan visualResult
	if err := json.Unmarshal([]byte(scanReply), &scan); err != nil {
		t.Fatal(err)
	}
	if _, err := stage(context.Background(), old, "01-visual-events", 0, 40, func() (visualResult, error) {
		return scan, nil
	}, func(v *visualResult) error { return validateVisual(v, frames, 0, 60, 30, false) }); err != nil {
		t.Fatal(err)
	}
	_, _, err = AnalyzeVisual(context.Background(), client, frames, 60, opts, dir, nil)
	if err == nil || model.count() != 0 {
		t.Fatalf("old refine chain must be rejected before new spending: calls=%d err=%v", model.count(), err)
	}
	assertCode(t, err, CodeInvalidResponse)
}
