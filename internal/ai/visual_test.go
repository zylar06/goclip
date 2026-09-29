package ai

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"autoclip-go/internal/domain"
)

const scanReply = `{"events":[
{"id":"menu","label":"Opening menu","start":0,"end":5,"evidence":"Static menu at 0s","kind":"menu","score":0.99,"frame_times":[0]},
{"id":"event-1","label":"Visible gameplay","start":10,"end":25,"evidence":"Player and obstacle in the 10s and 20s stills; motion unconfirmed","kind":"gameplay","score":0.9,"frame_times":[10,20]}]}`

const refineReply = `{"events":[{"id":"event-1","label":"Visible gameplay","start":10,"end":24,"evidence":"10s and 20s support the event; exact motion unknown","kind":"gameplay","score":0.85,"frame_times":[10,20]}]}`

func visualModel() *scriptedModel {
	return &scriptedModel{replies: map[string]string{
		"visual": scanReply,
		"refine": refineReply,
		"titles": `[{"id":"event-1","title":"A visible challenge","hook":"Can you clear it?"}]`,
	}}
}

func visualOptions() domain.AnalysisOptions {
	return domain.AnalysisOptions{Mode: "visual", AllowVisual: true, Confirmed: true, Duration: 30, Goals: []string{"highlight"}, Aspect: "original", Language: "en"}
}

func fixtureFrames(t *testing.T) []domain.Frame {
	t.Helper()
	path := tinyImage(t, "png")
	var frames []domain.Frame
	for i := 0; i < 6; i++ {
		frames = append(frames, domain.Frame{Time: float64(i * 10), Path: path})
	}
	return frames
}

func TestVisualImagesEventsRefinementDraftsAndReplay(t *testing.T) {
	model := visualModel()
	client, frames, dir := model.client(t), fixtureFrames(t), t.TempDir()
	input := append([]domain.Frame(nil), frames...)
	drafts, candidates, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 3 || len(drafts) != 1 || len(candidates) != 2 {
		t.Fatal("wrong visual stages/output")
	}
	if candidates[0].Kind != "menu" || candidates[1].End != 24 || drafts[0].Scenes[0].End != 24 {
		t.Fatal("filter audit/refined bounds missing")
	}
	if drafts[0].Subtitles || drafts[0].Language != "en" || drafts[0].Origin != "visual" {
		t.Fatal("visual draft options incorrect")
	}
	if !reflect.DeepEqual(input, frames) {
		t.Fatal("mutated input frames")
	}
	replayed, replayCandidates, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 3 || !reflect.DeepEqual(replayed, drafts) || !reflect.DeepEqual(replayCandidates, candidates) {
		t.Fatal("visual replay did not reuse verified results")
	}
	raw, err := os.ReadFile(frames[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, frames[0].Path, append(raw, 0))
	_, _, err = AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 3 {
		t.Fatal("changed image content did not invalidate checkpoint without spending")
	}
}

func TestVisualOptInAndInvalidFrames(t *testing.T) {
	model := visualModel()
	client, frames := model.client(t), fixtureFrames(t)
	for _, mutation := range []string{"allow", "confirm", "mode"} {
		opts := visualOptions()
		switch mutation {
		case "allow":
			opts.AllowVisual = false
		case "confirm":
			opts.Confirmed = false
		case "mode":
			opts.Mode = "subtitle"
		}
		_, _, err := AnalyzeVisual(context.Background(), client, frames, 60, opts, "", nil)
		assertCode(t, err, CodeInvalidResponse)
	}
	for _, duration := range []float64{0, -1, 7201, math.NaN(), math.Inf(1)} {
		_, _, err := AnalyzeVisual(context.Background(), client, frames, duration, visualOptions(), "", nil)
		assertCode(t, err, CodeInvalidResponse)
	}
	for _, badFrames := range [][]domain.Frame{
		nil, {{Time: -1, Path: frames[0].Path}}, {{Time: 60, Path: frames[0].Path}},
		{frames[0], frames[0]}, {{Time: 0, Path: "missing-" + testKey}},
	} {
		_, _, err := AnalyzeVisual(context.Background(), client, badFrames, 60, visualOptions(), "", nil)
		assertCode(t, err, CodeInvalidResponse)
	}
	if model.count() != 0 {
		t.Fatal("invalid or unconsented visual input called a provider")
	}
}

func TestVisualRejectsUnsupportedEventsAndRefinement(t *testing.T) {
	for _, tc := range []struct {
		name, stage, reply string
		calls              int
	}{
		{"empty", "visual", `{"events":[]}`, 1},
		{"no_array", "visual", `{"events":null}`, 1},
		{"no_score", "visual", strings.Replace(scanReply, `"score":0.9,`, "", 1), 1},
		{"bad_kind", "visual", strings.Replace(scanReply, `"gameplay"`, `"hallucinated"`, 1), 1},
		{"unknown_frame", "visual", strings.Replace(scanReply, `"frame_times":[10,20]`, `"frame_times":[11]`, 1), 1},
		{"over_duration", "visual", strings.Replace(scanReply, `"end":25`, `"end":59`, 1), 1},
		{"outside", "visual", strings.Replace(scanReply, `"end":25`, `"end":100`, 1), 1},
		{"menus_only", "visual", strings.Replace(scanReply, `"gameplay"`, `"loading"`, 1), 1},
		{"wrong_id", "refine", strings.Replace(refineReply, `"event-1"`, `"other"`, 1), 2},
		{"outside_window", "refine", strings.Replace(refineReply, `"start":10`, `"start":0`, 1), 2},
		{"empty_review", "refine", `{"events":[]}`, 2},
		{"filtered_review", "refine", strings.Replace(refineReply, `"gameplay"`, `"reward_screen"`, 1), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := visualModel()
			model.replies[tc.stage] = tc.reply
			_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
			assertCode(t, err, CodeInvalidResponse)
			if model.count() != tc.calls {
				t.Fatal("visual failure retried or continued downstream")
			}
		})
	}
}

func TestVisualIndependentEventsAreNotMerged(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, _ := decodeRequest(t, r)
		stage := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		switch stage {
		case "visual":
			answer(t, w, `{"events":[
{"id":"a","label":"First","start":0,"end":15,"evidence":"First sample, unknown motion","kind":"gameplay","score":0.8,"frame_times":[0,10]},
{"id":"b","label":"Second","start":15,"end":30,"evidence":"Second sample, unknown motion","kind":"gameplay","score":0.9,"frame_times":[20,30]}]}`)
		case "refine":
			var input struct {
				Event visualEvent `json:"event"`
			}
			raw := strings.SplitN(prompt, "INPUT_JSON:\n", 2)
			if len(raw) != 2 || json.Unmarshal([]byte(raw[1]), &input) != nil {
				t.Error("missing review event")
				return
			}
			result, err := json.Marshal(visualResult{Events: []visualEvent{input.Event}})
			if err != nil {
				t.Error(err)
				return
			}
			answer(t, w, string(result))
		case "titles":
			answer(t, w, `[{"id":"a","title":"First","hook":""},{"id":"b","title":"Second","hook":""}]`)
		default:
			t.Error("unexpected stage", stage)
		}
	})
	drafts, candidates, err := AnalyzeVisual(context.Background(), client, fixtureFrames(t), 60, visualOptions(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 2 || len(candidates) != 2 || calls.Load() != 4 {
		t.Fatal("independent adjacent events merged or not reviewed separately")
	}
	if drafts[0].Scenes[0].ID != "b" || drafts[1].Scenes[0].ID != "a" {
		t.Fatal("watch-score ranking not preserved")
	}
}

func TestVisualExplicitRetryAndCancellation(t *testing.T) {
	model := visualModel()
	model.fail = "titles"
	client, frames, dir := model.client(t), fixtureFrames(t), t.TempDir()
	_, _, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
	assertCode(t, err, CodeAuth)
	model.setFailure("")
	if _, _, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if model.count() != 4 {
		t.Fatal("visual explicit retry repeated scan/refinement")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = AnalyzeVisual(ctx, client, frames, 60, visualOptions(), dir, nil)
	assertCode(t, err, CodeTimeout)
	if !errors.Is(err, context.Canceled) || model.count() != 4 {
		t.Fatal("visual cancellation failed")
	}
}

func TestFrameSamplingBounds(t *testing.T) {
	var frames []domain.Frame
	for i := 0; i < 600; i++ {
		frames = append(frames, domain.Frame{Time: float64(i)})
	}
	for _, count := range []int{25, 60} {
		sampled := sampleFrames(frames, count)
		if len(sampled) != count || sampled[0].Time != 0 || sampled[len(sampled)-1].Time != 599 {
			t.Fatal("sampling must cover source head and tail")
		}
		for i := 1; i < len(sampled); i++ {
			if sampled[i].Time <= sampled[i-1].Time {
				t.Fatal("sampling duplicated/reversed timestamps")
			}
		}
	}
}
