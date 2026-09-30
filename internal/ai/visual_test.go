package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	return domain.AnalysisOptions{Mode: "visual", AllowVisual: true, Confirmed: true, Duration: 30, Goals: []string{"highlight"}, Aspect: "original"}
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
	// The candidate keeps its verified bounds for audit; the draft gains upstream's
	// bounded run-up and tail (lead 1.5s, tail 2s) so it does not open mid-action.
	if candidates[0].Kind != "menu" || candidates[1].End != 24 {
		t.Fatal("filter audit/refined bounds missing")
	}
	if drafts[0].Scenes[0].Start != 8.5 || drafts[0].Scenes[0].End != 26 {
		t.Fatalf("draft lost its context buffer: [%v,%v]", drafts[0].Scenes[0].Start, drafts[0].Scenes[0].End)
	}
	if drafts[0].Subtitles || drafts[0].Origin != "visual" {
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
		name, stage, reply, code string
		calls                    int
	}{
		{"empty", "visual", `{"events":[]}`, "no_highlights", 1},
		{"no_array", "visual", `{"events":null}`, CodeInvalidResponse, 1},
		{"missing_array", "visual", `{}`, CodeInvalidResponse, 1},
		{"bad_kind", "visual", strings.Replace(scanReply, `"gameplay"`, `"hallucinated"`, 1), CodeInvalidResponse, 1},
		{"unknown_frame", "visual", strings.Replace(scanReply, `"frame_times":[10,20]`, `"frame_times":[11]`, 1), CodeInvalidResponse, 1},
		{"outside", "visual", strings.Replace(scanReply, `"end":25`, `"end":100`, 1), CodeInvalidResponse, 1},
		{"menus_only", "visual", strings.Replace(scanReply, `"gameplay"`, `"loading"`, 1), "no_highlights", 1},
		{"wrong_id", "refine", strings.Replace(refineReply, `"event-1"`, `"other"`, 1), CodeInvalidResponse, 2},
		{"outside_window", "refine", strings.Replace(refineReply, `"start":10`, `"start":0`, 1), CodeInvalidResponse, 2},
		{"empty_review", "refine", `{"events":[]}`, "no_highlights", 2},
		{"filtered_review", "refine", strings.Replace(refineReply, `"gameplay"`, `"reward_screen"`, 1), "no_highlights", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := visualModel()
			model.replies[tc.stage] = tc.reply
			_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
			ae := assertCode(t, err, tc.code)
			if tc.code == "no_highlights" && (ae.Retryable || !strings.Contains(ae.Message, "subtitles")) {
				t.Fatal("no-highlight result must explain the subtitle option without inviting blind retries")
			}
			if model.count() != tc.calls {
				t.Fatal("visual failure retried or continued downstream")
			}
		})
	}
}

func TestVisualEducationalEvidenceAndPromptScope(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, parts := decodeRequest(t, r)
		stage := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		switch stage {
		case "visual", "refine":
			assertImages(t, parts, len(parts)/2)
			for _, guidance := range []string{"教学", "屏幕文字", "other", "音频"} {
				if !strings.Contains(prompt, guidance) {
					t.Errorf("%s prompt lacks general-video guidance %q", stage, guidance)
				}
			}
			answer(t, w, `{"events":[{"id":"lesson","label":"Visible equation comparison","start":10,"end":24,"evidence":"Different equations and a comparison caption are visible at 10s and 20s; spoken reasoning is unknown.","kind":"other","score":0.8,"frame_times":[10,20]}]}`)
		case "titles":
			answer(t, w, `[{"id":"lesson","title":"Comparing the displayed equations","hook":""}]`)
		default:
			t.Errorf("unexpected stage %s", stage)
		}
	})
	drafts, candidates, err := AnalyzeVisual(context.Background(), client, fixtureFrames(t), 60, visualOptions(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(drafts) != 1 || len(candidates) != 1 || candidates[0].Kind != "other" {
		t.Fatal("educational visual evidence must survive scan, review and draft creation")
	}
	// Evidence bounds [10,24] gain the bounded run-up and tail before drafting.
	if drafts[0].Scenes[0].Start != 8.5 || drafts[0].Scenes[0].End != 26 {
		t.Fatalf("educational draft lost evidence-based bounds: [%v,%v]", drafts[0].Scenes[0].Start, drafts[0].Scenes[0].End)
	}
}

// Upstream select_highlights truncates with raw_events[:12] rather than
// rejecting, since the prompt's "at most 12" is a soft limit models overshoot.
// The first twelve must be kept and the rest dropped.
func TestVisualTruncatesExcessEventsInsteadOfRejecting(t *testing.T) {
	frames := fixtureFrames(t)
	events := make([]visualEvent, 13)
	for i := range events {
		s, e := timestamp(0), timestamp(10)
		score := 0.5
		events[i] = visualEvent{
			ID: fmt.Sprintf("event-%d", i+1), Label: "Visible", Evidence: "Sample at 0s",
			Start: &s, End: &e, Kind: "other", Score: &score, FrameTimes: []float64{0},
		}
	}
	value := visualResult{Events: events}
	if err := validateVisual(&value, frames, 0, 60, 30, false); err != nil {
		t.Fatalf("a thirteenth event must not discard the scan: %v", err)
	}
	if len(value.Events) != 12 {
		t.Fatalf("expected truncation to 12 events, got %d", len(value.Events))
	}
	if value.Events[0].ID != "event-1" || value.Events[11].ID != "event-12" {
		t.Fatal("truncation must keep the first twelve in order")
	}
}

func TestVisualLegacyPromptCheckpointCannotReplay(t *testing.T) {
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
	legacy, err := newRunner(context.Background(), client, struct {
		Frames   []frameFingerprint     `json:"frames"`
		Duration float64                `json:"duration"`
		Options  domain.AnalysisOptions `json:"options"`
	}{hashes, 60, opts}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	var scan visualResult
	if err := json.Unmarshal([]byte(scanReply), &scan); err != nil {
		t.Fatal(err)
	}
	_, err = stage(context.Background(), legacy, "01-visual-events", 0, 40,
		func() (visualResult, error) { return scan, nil },
		func(value *visualResult) error { return validateVisual(value, frames, 0, 60, 30, false) })
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = AnalyzeVisual(context.Background(), client, frames, 60, opts, dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 0 {
		t.Fatal("stale prompt checkpoint must not trigger paid replacement calls")
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
	// Only the strongest playable candidate gets the dense paid review; the other
	// keeps its scanned bounds. Both still become independent drafts.
	if len(drafts) != 2 || len(candidates) != 2 || calls.Load() != 3 {
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

// Reproduces the reported failure on a 299.84-second upload: frames sampled at
// i*duration/count are irrational-looking floats, but Client.Complete shows the
// model only "Source timestamp %.3f seconds". A model that correctly echoes a
// supplied sample returned the rounded value, which exact matching rejected as
// "cites a frame that was not supplied", failing the whole analysis.
func TestVisualAcceptsRoundedFrameTimestamps(t *testing.T) {
	const duration, count = 299.84, 24.
	path := tinyImage(t, "png")
	// Frames are supplied at full sampling precision, as a caller that does not
	// pre-round them does: 1*299.84/24 is 12.493333..., not 12.493.
	var frames []domain.Frame
	for i := 0; i < int(count); i++ {
		frames = append(frames, domain.Frame{Time: float64(i) * duration / count, Path: path})
	}
	rawFirst, rawSecond := 1*duration/count, 2*duration/count
	if rawFirst == math.Round(rawFirst*1000)/1000 {
		t.Fatal("fixture must exercise a timestamp that is not exact at 3 decimals")
	}
	model := &scriptedModel{replies: map[string]string{
		"visual": `{"events":[{"id":"event-1","label":"Explained comparison","start":12.493,"end":25.0,` +
			`"evidence":"Two equations and a caption are visible in the 12.493s and 24.987s stills.","kind":"other",` +
			`"score":0.8,"frame_times":[12.493,24.987]}]}`,
		"refine": `{"events":[{"id":"event-1","label":"Explained comparison","start":12.493,"end":24.987,` +
			`"evidence":"Both samples support the segment; spoken reasoning is unknown.","kind":"other",` +
			`"score":0.8,"frame_times":[12.493,24.987]}]}`,
		"titles": `[{"id":"event-1","title":"The comparison that explains it","hook":"Which one is right?"}]`,
	}}
	drafts, candidates, err := AnalyzeVisual(context.Background(), model.client(t), frames, duration, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("rounded timestamps must be accepted: %v", err)
	}
	if len(drafts) != 1 || len(candidates) != 1 {
		t.Fatalf("expected one draft and one candidate, got %d/%d", len(drafts), len(candidates))
	}
	// Accepted times are snapped back to the exact supplied frames, so no
	// provider rounding leaks into scenes or later stages.
	if got := candidates[0].Start; math.Abs(got-rawFirst) > 1e-9 && got != 12.493 {
		t.Fatalf("scene start %v is neither the supplied frame nor its rounded form", got)
	}
	if got := candidates[0].End; math.Abs(got-rawSecond) > 1e-9 && got != 24.987 {
		t.Fatalf("scene end %v is neither the supplied frame nor its rounded form", got)
	}
}

// A timestamp far from every supplied frame is still a hallucination and must
// fail: the tolerance widens matching to the transmitted precision, it does not
// let the model invent samples it never received.
func TestVisualStillRejectsUnsuppliedFrameTimestamps(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = strings.Replace(scanReply, `"frame_times":[10,20]`, `"frame_times":[10,17.5]`, 1)
	_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("a timestamp with no nearby frame must stay invalid_response, got %v", err)
	}
}

func TestVisualAcceptsProviderSimplifiedFrameTimestamps(t *testing.T) {
	model := visualModel()
	// The request supplies 10.000 and 20.000, while some providers simplify
	// nearby displayed timestamps to fewer decimals. This must still resolve to
	// the actual supplied images, without accepting a genuinely distant frame.
	model.replies["visual"] = strings.Replace(scanReply, `"frame_times":[10,20]`, `"frame_times":[10.04,19.96]`, 1)
	model.replies["refine"] = strings.Replace(refineReply, `"frame_times":[10,20]`, `"frame_times":[10.04,19.96]`, 1)
	_, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("provider-rounded timestamps must snap to supplied frames: %v", err)
	}
	if len(candidates) != 2 || candidates[1].Start != 10 || candidates[1].End != 24 {
		t.Fatalf("expected snapped gameplay candidate, got %+v", candidates)
	}
}

// Two cited timestamps can legitimately snap to the same supplied frame when
// sampling is coarse, so they are deduplicated rather than treated as a fatal
// duplicate. An event must still cite at least one real supplied frame.
func TestVisualDeduplicatesTimestampsSnappingToOneFrame(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = strings.Replace(scanReply, `"frame_times":[10,20]`, `"frame_times":[20,20.0004]`, 1)
	model.replies["refine"] = strings.Replace(refineReply, `"frame_times":[10,20]`, `"frame_times":[20]`, 1)
	_, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("timestamps collapsing onto one frame must be deduplicated, not fatal: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected the event to survive deduplication")
	}
}

// Deduplication must not become fabrication: an event whose every citation is
// outside its own bounds has no supporting evidence left and is rejected.
func TestVisualRejectsEventWithNoSupportingFrameInBounds(t *testing.T) {
	frames := fixtureFrames(t)
	s, e := timestamp(10), timestamp(20)
	score := 0.9
	value := visualResult{Events: []visualEvent{{
		ID: "event-1", Label: "Visible", Evidence: "x", Start: &s, End: &e,
		Kind: "gameplay", Score: &score, FrameTimes: []float64{50}, // Outside [10,20].
	}}}
	err := validateVisual(&value, frames, 0, 60, 30, false)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeInvalidResponse {
		t.Fatalf("an event citing no in-bounds frame must be invalid_response, got %v", err)
	}
}

// Upstream declares watch_score as `int | None` and sorts a missing one last, and
// models carry over 0-100 habits. A null or rescalable score must not discard the
// scan; only a value beyond any plausible scale is fatal.
func TestVisualToleratesNullAndRescaledScores(t *testing.T) {
	for _, tc := range []struct{ name, replace, with string }{
		{"null_score", `"score":0.9,"frame_times":[10,20]`, `"score":null,"frame_times":[10,20]`},
		{"0_to_100", `"score":0.9,"frame_times":[10,20]`, `"score":90,"frame_times":[10,20]`},
		{"0_to_10", `"score":0.9,"frame_times":[10,20]`, `"score":9,"frame_times":[10,20]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := visualModel()
			model.replies["visual"] = strings.Replace(scanReply, tc.replace, tc.with, 1)
			if _, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil); err != nil {
				t.Fatalf("%s must not discard the scan: %v", tc.name, err)
			}
		})
	}
	// Beyond any plausible scale is still a fabrication.
	model := visualModel()
	model.replies["visual"] = strings.Replace(scanReply, `"score":0.9,"frame_times":[10,20]`, `"score":1e9,"frame_times":[10,20]`, 1)
	_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("an out-of-scale score must stay invalid_response, got %v", err)
	}
}

// Upstream defaults event_type to 'unknown' and notes that missing annotations
// remain usable, so an omitted kind must not discard the scan.
func TestVisualDefaultsMissingKindToUnknown(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = strings.Replace(scanReply, `"kind":"gameplay",`, "", 1)
	model.replies["refine"] = strings.Replace(refineReply, `"kind":"gameplay",`, "", 1)
	_, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a missing kind must default to unknown, not fail: %v", err)
	}
	found := false
	for _, c := range candidates {
		if c.ID == "event-1" && c.Kind == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected event-1 classified unknown, got %+v", candidates)
	}
	// An explicitly wrong kind is still a schema error, not silently defaulted.
	bad := visualModel()
	bad.replies["visual"] = strings.Replace(scanReply, `"kind":"gameplay"`, `"kind":"cinematic"`, 1)
	_, _, err = AnalyzeVisual(context.Background(), bad.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("an unrecognized kind must stay invalid_response, got %v", err)
	}
}

// A single combined condition once reported fifteen different defects with one
// message, so a real provider failure could not be attributed without spending
// another paid run. Each rejection must name the field it rejected.
func TestVisualValidationNamesTheFailedField(t *testing.T) {
	for _, tc := range []struct{ name, replace, with, want string }{
		{"score", `"score":0.9,"frame_times":[10,20]`, `"score":1e9,"frame_times":[10,20]`, "score"},
		{"kind", `"kind":"gameplay"`, `"kind":"cinematic"`, "kind"},
		// An empty label now falls back to the upstream default, so oversize is
		// what the label check must still reject — and still name.
		{"label", `"label":"Visible gameplay"`, `"label":"` + strings.Repeat("x", 121) + `"`, "label"},
		{"evidence", `"evidence":"Player and obstacle in the 10s and 20s stills; motion unconfirmed"`,
			`"evidence":"` + strings.Repeat("y", 1001) + `"`, "evidence"},
		{"window", `"start":10,"end":25`, `"start":10,"end":95`, "window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := visualModel()
			model.replies["visual"] = strings.Replace(scanReply, tc.replace, tc.with, 1)
			_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
			var e *Error
			if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
				t.Fatalf("expected invalid_response, got %v", err)
			}
			if !strings.Contains(strings.ToLower(e.Message), tc.want) {
				t.Fatalf("message %q does not identify the rejected field %q", e.Message, tc.want)
			}
		})
	}
}

// A live qwen3-vl-plus scan returned events longer than the requested 30-second
// clip length, which discarded the whole billed analysis. The requested duration
// is a target, not a correctness invariant, so an overlong event is trimmed
// around its cited evidence instead of failing the run.
func TestVisualTrimsOverlongEventsInsteadOfFailing(t *testing.T) {
	model := visualModel()
	// 0–50s exceeds the 30s request; samples at 10 and 20 must stay inside.
	model.replies["visual"] = strings.Replace(scanReply, `"start":10,"end":25`, `"start":0,"end":50`, 1)
	model.replies["refine"] = strings.Replace(refineReply, `"start":10,"end":24`, `"start":8,"end":26`, 1)
	drafts, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("overlong event must be trimmed, not rejected: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected one draft, got %d", len(drafts))
	}
	var event *domain.Candidate
	for i := range candidates {
		if candidates[i].ID == "event-1" {
			event = &candidates[i]
		}
	}
	if event == nil {
		t.Fatal("trimmed event missing from candidates")
	}
	if got := event.End - event.Start; got > 30+1e-6 {
		t.Fatalf("trimmed event still lasts %v, beyond the requested 30s", got)
	}
	// Trimming must not cut away the samples the evidence rests on.
	if event.Start > 10+1e-6 || event.End < 20-1e-6 {
		t.Fatalf("trim dropped cited evidence: [%v,%v] excludes samples 10 and 20", event.Start, event.End)
	}
}

// Trimming must never push an event outside the analyzed window, even when the
// cited samples sit hard against its edge.
func TestVisualTrimStaysInsideTheWindow(t *testing.T) {
	for _, tc := range []struct{ name, replace, with string }{
		{"at_start", `"start":10,"end":25`, `"start":0,"end":45`},
		{"at_end", `"start":10,"end":25`, `"start":15,"end":60`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := visualEvent{ID: "event-1", FrameTimes: []float64{20, 30}}
			s, en := timestamp(0), timestamp(60)
			e.Start, e.End = &s, &en
			trimVisualEvent(&e, 0, 60, 30)
			if float64(*e.Start) < 0 || float64(*e.End) > 60 {
				t.Fatalf("trim left the window: [%v,%v]", *e.Start, *e.End)
			}
			if float64(*e.End-*e.Start) > 30+1e-6 {
				t.Fatalf("trim did not shorten to the target: %v", *e.End-*e.Start)
			}
		})
	}
}

// The scan stage used to reject an event longer than the requested clip; it now
// trims. The refine stage still rejects an event that leaves its review window,
// which is a different guarantee and must not be relaxed by the trim.
func TestVisualRefineStillRejectsOutOfWindowEvents(t *testing.T) {
	model := visualModel()
	model.replies["refine"] = strings.Replace(refineReply, `"start":10,"end":24`, `"start":0,"end":24`, 1)
	_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("refinement outside its window must stay invalid_response, got %v", err)
	}
}

// Two playable candidates, so a refinement failure has somewhere to fall back to.
const twoPlayableScan = `{"events":[
{"id":"weak","label":"Weaker segment","start":0,"end":15,"evidence":"First sample; motion unconfirmed","kind":"gameplay","score":0.6,"frame_times":[0,10]},
{"id":"strong","label":"Stronger segment","start":20,"end":35,"evidence":"Later samples; motion unconfirmed","kind":"gameplay","score":0.95,"frame_times":[20,30]}]}`

// Upstream Scene (backend/services/studio/models.py) declares
// `label: str = Field(default='片段', max_length=120)` and
// `evidence: str = Field(default=empty, max_length=1000)`, so a usable segment
// with no prose still yields an editable draft. The port rejected both,
// discarding an already billed analysis over a missing description. Emptiness
// now falls back.
func TestVisualAcceptsEmptyLabelAndEvidence(t *testing.T) {
	model := visualModel()
	blank := func(reply string) string {
		reply = strings.Replace(reply, `"label":"Visible gameplay"`, `"label":"   "`, 1)
		reply = strings.Replace(reply, `"evidence":"Player and obstacle in the 10s and 20s stills; motion unconfirmed"`, `"evidence":""`, 1)
		return strings.Replace(reply, `"evidence":"10s and 20s support the event; exact motion unknown"`, `"evidence":""`, 1)
	}
	model.replies["visual"], model.replies["refine"] = blank(scanReply), blank(refineReply)
	drafts, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a usable segment without prose must still produce a draft: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("expected one draft, got %d", len(drafts))
	}
	var event *domain.Candidate
	for i := range candidates {
		if candidates[i].ID == "event-1" {
			event = &candidates[i]
		}
	}
	if event == nil {
		t.Fatal("event missing from candidates")
	}
	if event.Label != defaultVisualLabel {
		t.Fatalf("empty label must fall back to %q, got %q", defaultVisualLabel, event.Label)
	}
	if event.Evidence != "" {
		t.Fatalf("empty evidence must be accepted as-is, got %q", event.Evidence)
	}
	if drafts[0].Scenes[0].Label != defaultVisualLabel {
		t.Fatalf("draft scene lost the default label: %q", drafts[0].Scenes[0].Label)
	}
}

// Only emptiness falls back. Oversize text and invalid UTF-8 stay hard failures
// so a runaway or corrupt response cannot ride the fallback into a draft.
func TestVisualStillRejectsOversizeAndInvalidLabelText(t *testing.T) {
	frames := fixtureFrames(t)
	for _, tc := range []struct{ name, label, evidence, want string }{
		{"long_label", strings.Repeat("x", 121), "ok", "label"},
		{"long_evidence", "ok", strings.Repeat("y", 1001), "evidence"},
		{"invalid_label", "bad\xff", "ok", "label"},
		{"invalid_evidence", "ok", "bad\xff", "evidence"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, score := timestamp(10), timestamp(25), .9
			value := visualResult{Events: []visualEvent{{
				ID: "event-1", Label: tc.label, Evidence: tc.evidence, Start: &s, End: &e,
				Kind: "gameplay", Score: &score, FrameTimes: []float64{10, 20},
			}}}
			err := validateVisual(&value, frames, 0, 60, 30, false)
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeInvalidResponse {
				t.Fatalf("oversize/invalid text must stay invalid_response, got %v", err)
			}
			if !strings.Contains(strings.ToLower(ae.Message), tc.want) {
				t.Fatalf("message %q does not name the rejected field %q", ae.Message, tc.want)
			}
		})
	}
}

// Upstream refines exactly one event (`best = events[0]` in
// backend/services/studio/intelligence.py). The port ran the paid
// 02-visual-refine-NN stage for up to six events, multiplying cost and failure
// probability by six. Only the highest-scoring playable candidate is reviewed;
// the rest keep their scanned bounds and still become drafts.
func TestVisualRefinesOnlyTheTopCandidate(t *testing.T) {
	var refineCalls atomic.Int32
	var reviewed atomic.Value
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		prompt, _ := decodeRequest(t, r)
		switch strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ") {
		case "visual":
			answer(t, w, twoPlayableScan)
		case "refine":
			refineCalls.Add(1)
			var input struct {
				Event visualEvent `json:"event"`
			}
			raw := strings.SplitN(prompt, "INPUT_JSON:\n", 2)
			if len(raw) != 2 || json.Unmarshal([]byte(raw[1]), &input) != nil {
				t.Error("missing review event")
				return
			}
			reviewed.Store(input.Event.ID)
			if input.Event.ID != "strong" {
				// A correct pipeline never reaches here. Echo the event back
				// unchanged so an extra review fails the call-count assertion
				// below rather than an incidental bounds check.
				echo, err := json.Marshal(visualResult{Events: []visualEvent{input.Event}})
				if err != nil {
					t.Error(err)
					return
				}
				answer(t, w, string(echo))
				return
			}
			answer(t, w, `{"events":[{"id":"strong","label":"Stronger segment","start":21,"end":34,`+
				`"evidence":"Both samples support the segment","kind":"gameplay","score":0.95,"frame_times":[30]}]}`)
		case "titles":
			answer(t, w, `[{"id":"strong","title":"Stronger","hook":""},{"id":"weak","title":"Weaker","hook":""}]`)
		default:
			t.Error("unexpected stage")
		}
	})
	stages, progress := progressRecorder(t)
	drafts, candidates, err := AnalyzeVisual(context.Background(), client, fixtureFrames(t), 60, visualOptions(), t.TempDir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	if got := refineCalls.Load(); got != 1 {
		t.Fatalf("exactly one candidate may be refined, got %d paid review calls", got)
	}
	if got, _ := reviewed.Load().(string); got != "strong" {
		t.Fatalf("the highest-scoring playable candidate must be reviewed, got %q", got)
	}
	if len(drafts) != 2 || len(candidates) != 2 {
		t.Fatalf("unrefined playable candidates must still become drafts, got %d drafts / %d candidates", len(drafts), len(candidates))
	}
	byID := map[string]domain.Candidate{}
	for _, c := range candidates {
		byID[c.ID] = c
	}
	if byID["strong"].End != 34 {
		t.Fatalf("the reviewed candidate did not take its refined bounds: %v", byID["strong"].End)
	}
	if byID["weak"].Start != 0 || byID["weak"].End != 15 {
		t.Fatalf("an unreviewed candidate must keep its scanned bounds, got [%v,%v]", byID["weak"].Start, byID["weak"].End)
	}
	for _, name := range *stages {
		if strings.HasPrefix(name, refineStagePrefix) && name != refineStage {
			t.Fatalf("unexpected extra refine stage %q", name)
		}
	}
}

// Upstream drops a candidate whose dense review is unusable and continues.
// The port failed the whole already billed run instead. A validation failure
// from the refine stage is skipped and surfaced through progress; every other
// failure class still aborts.
func TestVisualSkipsUnusableRefinementInsteadOfFailing(t *testing.T) {
	model := &scriptedModel{replies: map[string]string{
		"visual": twoPlayableScan,
		// Wrong identity: a validation failure authored by the refine stage.
		"refine": `{"events":[{"id":"hallucinated","label":"Other","start":21,"end":34,` +
			`"evidence":"unrelated","kind":"gameplay","score":0.9,"frame_times":[30]}]}`,
		"titles": `[{"id":"weak","title":"Weaker","hook":""}]`,
	}}
	stages, progress := progressRecorder(t)
	dir, client := t.TempDir(), model.client(t)
	drafts, candidates, err := AnalyzeVisual(context.Background(), client, fixtureFrames(t), 60, visualOptions(), dir, progress)
	if err != nil {
		t.Fatalf("an unusable dense review must not discard the billed scan: %v", err)
	}
	if len(drafts) != 1 || drafts[0].Scenes[0].ID != "weak" {
		t.Fatalf("the remaining playable candidate must still become a draft, got %d drafts", len(drafts))
	}
	if len(candidates) != 2 {
		t.Fatalf("all scan candidates stay available for audit, got %d", len(candidates))
	}
	// The failed paid call is not repeated: one scan, one refine, one titles.
	if model.count() != 3 {
		t.Fatalf("a skipped refinement must not be retried, got %d model calls", model.count())
	}
	skipped := false
	for _, name := range *stages {
		if name == refineSkippedStage {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("a skipped refinement must be visible in progress, saw %v", *stages)
	}
	// A tolerated rejection is a durable decision. Explicit retry replays the
	// skip and its downstream chain without another paid review.
	before := model.count()
	replayed, _, err := AnalyzeVisual(context.Background(), client, fixtureFrames(t), 60, visualOptions(), dir, nil)
	if err != nil {
		t.Fatalf("explicit retry after a skipped refinement failed: %v", err)
	}
	if !reflect.DeepEqual(replayed, drafts) {
		t.Fatal("explicit retry did not reproduce the degraded result")
	}
	if got := model.count() - before; got != 0 {
		t.Fatalf("retry must reuse the durable skip, got %d paid calls", got)
	}
}

// The skip tolerates exactly one failure class. Auth, cancellation and
// progress-callback failures from the refine stage must still abort, and
// context identity must survive.
func TestVisualRefineSkipDoesNotSwallowOtherFailures(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		model := &scriptedModel{replies: map[string]string{"visual": twoPlayableScan, "refine": refineReply}}
		model.fail = "refine"
		_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
		ae := assertCode(t, err, CodeAuth)
		if ae.Stage != refineStage {
			t.Fatalf("auth failure lost its stage: %q", ae.Stage)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
			prompt, _ := decodeRequest(t, r)
			if strings.HasPrefix(prompt, "AUTOCLIP_STAGE: visual\n") {
				answer(t, w, twoPlayableScan)
				return
			}
			// Cancel during the refine call, then reply with an unusable review:
			// cancellation must win over the skip.
			cancel()
			answer(t, w, `{"events":[{"id":"hallucinated","label":"Other","start":21,"end":34,`+
				`"evidence":"unrelated","kind":"gameplay","score":0.9,"frame_times":[30]}]}`)
		})
		_, _, err := AnalyzeVisual(ctx, client, fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
		assertCode(t, err, CodeTimeout)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation identity must survive the refine skip: %v", err)
		}
	})
	t.Run("progress_callback", func(t *testing.T) {
		model := &scriptedModel{replies: map[string]string{
			"visual": twoPlayableScan,
			"refine": `{"events":[{"id":"hallucinated","label":"Other","start":21,"end":34,` +
				`"evidence":"unrelated","kind":"gameplay","score":0.9,"frame_times":[30]}]}`,
		}}
		stop := errors.New("caller stopped the task")
		progress := func(name string, _ *float64) error {
			if name == refineSkippedStage {
				return stop
			}
			return nil
		}
		_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), progress)
		if err == nil {
			t.Fatal("a progress-callback failure must abort, not be swallowed by the skip")
		}
		if model.count() != 2 {
			t.Fatalf("no further paid call may follow a stopped callback, got %d", model.count())
		}
	})
}

// progressRecorder collects stage names and asserts percentages stay monotonic
// within 0–100, which the refine-stage rework must not disturb.
func progressRecorder(t *testing.T) (*[]string, domain.ProgressFunc) {
	t.Helper()
	stages, last := &[]string{}, -1.0
	return stages, func(name string, percent *float64) error {
		if percent == nil || *percent < last || *percent < 0 || *percent > 100 {
			t.Errorf("progress %q is not monotonic within 0–100: %v", name, percent)
		} else {
			last = *percent
		}
		*stages = append(*stages, name)
		return nil
	}
}

// An entirely omitted score field, not just an explicit null, must also default
// rather than discard the scan.
func TestVisualToleratesOmittedScoreField(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = strings.Replace(scanReply, `"score":0.9,`, "", 1)
	model.replies["refine"] = strings.Replace(refineReply, `"score":0.85,`, "", 1)
	_, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("an omitted score must default, not fail: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates to survive an omitted score")
	}
}

// Upstream assemble_sequences gives every clip bounded run-up and tail so it does
// not open mid-action or cut on the last frame of evidence. The Go port had no
// equivalent, which was the largest pure clip-quality gap.
func TestAddContextMatchesUpstreamBounds(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		start, end, target, duration float64
		wantStart, wantEnd           float64
	}{
		// Slack available: lead is half the slack, capped at 1.5s; tail is 2s.
		{"typical", 10, 24, 30, 60, 8.5, 26},
		// A short event gets the full 1.5s lead, and the total stays within target.
		{"short_event", 20, 26, 30, 60, 18.5, 28},
		// No slack left: the event already fills the target, so nothing is added.
		{"no_slack", 10, 40, 30, 60, 10, 40},
		// Clamped at the source start, never negative.
		{"at_source_start", 0.5, 10, 30, 60, 0, 12},
		// Clamped at the source end.
		{"at_source_end", 50, 59.5, 30, 60, 48.5, 60},
		// The total never exceeds the requested duration.
		{"target_caps_total", 10, 35, 30, 200, 8.5, 37},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := []domain.Candidate{{Scene: domain.Scene{ID: "c1", Start: tc.start, End: tc.end}}}
			got := addContext(in, tc.target, tc.duration)
			if len(got) != 1 {
				t.Fatalf("expected one candidate, got %d", len(got))
			}
			if math.Abs(got[0].Start-tc.wantStart) > 1e-9 || math.Abs(got[0].End-tc.wantEnd) > 1e-9 {
				t.Fatalf("got [%v,%v], want [%v,%v]", got[0].Start, got[0].End, tc.wantStart, tc.wantEnd)
			}
			// Invariants that must hold for every case.
			if got[0].Start < 0 || got[0].End > tc.duration+1e-9 {
				t.Errorf("escaped the source: [%v,%v] of %v", got[0].Start, got[0].End, tc.duration)
			}
			if got[0].End-got[0].Start > tc.target+1e-9 {
				t.Errorf("exceeded the requested duration: %v > %v", got[0].End-got[0].Start, tc.target)
			}
			// Context may only widen a clip, never shorten the verified evidence.
			if got[0].Start > tc.start+1e-9 || got[0].End < tc.end-1e-9 {
				t.Errorf("context narrowed the verified span [%v,%v] to [%v,%v]", tc.start, tc.end, got[0].Start, got[0].End)
			}
		})
	}
	// Degenerate inputs are returned untouched rather than producing a bad span.
	if got := addContext(nil, 30, 60); got != nil {
		t.Error("nil candidates should pass through")
	}
	in := []domain.Candidate{{Scene: domain.Scene{ID: "c1", Start: 1, End: 2}}}
	for _, bad := range []float64{0, -1, math.NaN()} {
		got := addContext(in, 30, bad)
		if got[0].Start != 1 || got[0].End != 2 {
			t.Errorf("source duration %v should leave bounds untouched, got [%v,%v]", bad, got[0].Start, got[0].End)
		}
	}
}
