package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"autoclip-go/internal/domain"
)

type scriptedModel struct {
	mu      sync.Mutex
	replies map[string]string
	fail    string
	calls   []string
}

func (m *scriptedModel) client(t *testing.T) *Client {
	t.Helper()
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		prompt, parts := decodeRequest(t, r)
		first := strings.SplitN(prompt, "\n", 2)[0]
		stage := strings.TrimPrefix(first, "AUTOCLIP_STAGE: ")
		if stage == "visual" || stage == "refine" {
			count := 0
			for _, p := range parts {
				if p.Type == "image_url" {
					count++
				}
			}
			if count == 0 {
				t.Error("visual analysis was text-only")
			}
			assertImages(t, parts, count)
		} else if len(parts) != 0 {
			t.Error("text stage unexpectedly sent frames")
		}
		m.mu.Lock()
		m.calls = append(m.calls, stage)
		reply, fail := m.replies[stage], m.fail == stage
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := fmt.Fprint(w, testKey+serverSecret); err != nil {
				t.Error(err)
			}
			return
		}
		if reply == "" {
			t.Errorf("no mock response for stage %s", stage)
			w.WriteHeader(500)
			return
		}
		answer(t, w, reply)
	})
	return client
}

func (m *scriptedModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *scriptedModel) setFailure(stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fail = stage
}

func textModel() *scriptedModel {
	return &scriptedModel{replies: map[string]string{
		"outline":  `[{"title":"Topic A","subtopics":["point A"]},{"title":"Topic B","subtopics":["point B"]}]`,
		"timeline": `[{"topic_id":"topic-1","start":0.5,"end":39.7},{"topic_id":"topic-2","start":"01:00","end":"00:01:40,000"}]`,
		"scoring":  `[{"id":"text-2","score":0.85,"reason":"second excerpt"},{"id":"text-1","score":0.9,"reason":"first excerpt"}]`,
		"titles":   `[{"id":"text-2","title":"Second","hook":"Second hook"},{"id":"text-1","title":"First","hook":"First hook"}]`,
	}}
}

func fixtureCues() []domain.Cue {
	var cues []domain.Cue
	for i := 0; i < 12; i++ {
		cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: fmt.Sprintf("Source sentence %d.", i)})
	}
	return cues
}

func textOptions() domain.AnalysisOptions {
	return domain.AnalysisOptions{Mode: "subtitle", Duration: 40, Aspect: "portrait", Confirmed: true, Goals: []string{"content"}}
}

func TestTextPipelineAndCheckpoints(t *testing.T) {
	model := textModel()
	client, dir := model.client(t), t.TempDir()
	cues := fixtureCues()
	original := append([]domain.Cue(nil), cues...)
	var stages []string
	last := -1.0
	progress := func(name string, percent *float64) error {
		if percent == nil || *percent < last || *percent < 0 || *percent > 100 {
			t.Error("nonmonotonic progress", percent)
		}
		last = *percent
		stages = append(stages, name)
		return nil
	}
	drafts, candidates, err := AnalyzeText(context.Background(), client, cues, textOptions(), dir, progress)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 4 || len(drafts) != 2 || len(candidates) != 2 || len(stages) != 14 || last != 100 {
		t.Fatalf("wrong pipeline output: calls=%d drafts=%d candidates=%d progress=%v", model.count(), len(drafts), len(candidates), stages)
	}
	if !reflect.DeepEqual(cues, original) {
		t.Fatal("mutated input cues")
	}
	if candidates[0].Start != 0 || candidates[0].End != 40 || candidates[0].Score != .9 ||
		candidates[1].Start != 60 || candidates[1].End != 100 || candidates[1].Score != .85 {
		t.Fatalf("wrong timeline/ID score alignment: %+v", candidates)
	}
	if candidates[0].Evidence != excerpt(cues, 0, 40) {
		t.Fatal("evidence not grounded")
	}
	if drafts[0].Title != "First" || drafts[0].Scenes[0].ID != "text-1" || drafts[1].Scenes[0].ID != "text-2" {
		t.Fatal("title association or draft order wrong")
	}
	for _, d := range drafts {
		if err := d.Validate(120); err != nil {
			t.Fatal(err)
		}
		if d.Layout != "crop" || d.Aspect != "portrait" || d.Subtitles {
			t.Fatal("lost preferences")
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		t.Fatalf("want seven checkpoints, got %d", len(files))
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var cp checkpoint
		if err := decodeJSON(string(data), &cp); err != nil {
			t.Fatal(err)
		}
		if cp.DataHash != digest(cp.Data) || cp.Version != pipelineVersion || strings.Contains(string(data), testKey) {
			t.Fatal("invalid checkpoint or key leakage")
		}
	}
	replayed, replayCandidates, err := AnalyzeText(context.Background(), client, cues, textOptions(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 4 || !reflect.DeepEqual(drafts, replayed) || !reflect.DeepEqual(candidates, replayCandidates) {
		t.Fatal("checkpoint replay called model or changed stable result")
	}
}

func TestExplicitRetryResumesVerifiedStages(t *testing.T) {
	model := textModel()
	model.fail = "titles"
	client, dir := model.client(t), t.TempDir()
	_, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), dir, nil)
	ae := assertCode(t, err, CodeAuth)
	if ae.Stage != "04-titles" || model.count() != 4 {
		t.Fatal("wrong failing stage or auto retry")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Fatal("failed stage must not checkpoint")
	}
	model.setFailure("")
	drafts, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 5 || len(drafts) != 2 {
		t.Fatal("retry did not reuse first three verified stages")
	}
}

func TestTextRejectsInvalidStageJSONWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		stage, body string
		calls       int
	}{
		{"outline", `[]`, 1},
		{"outline", `[{"title":"A","subtopics":["x"],"extra":true}]`, 1},
		{"timeline", `[{"topic_id":"topic-1","end":40}]`, 2},
		{"timeline", `[{"topic_id":"invented","start":0,"end":40}]`, 2},
		{"timeline", `[{"topic_id":"topic-1","start":40,"end":0}]`, 2},
	} {
		t.Run(tc.stage+"_"+tc.body, func(t *testing.T) {
			model := textModel()
			model.replies[tc.stage] = tc.body
			_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
			assertCode(t, err, CodeInvalidResponse)
			if model.count() != tc.calls {
				t.Fatal("invalid stage triggered extra calls")
			}
		})
	}
}

func TestCheckpointCorruptionAndStalenessNeverSpend(t *testing.T) {
	for _, mutation := range []string{"malformed", "checksum", "semantic", "previous", "options", "cues", "version"} {
		t.Run(mutation, func(t *testing.T) {
			model := textModel()
			client, dir := model.client(t), t.TempDir()
			cues, opts := fixtureCues(), textOptions()
			if _, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, nil); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "02-timeline.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cp checkpoint
			if err := json.Unmarshal(raw, &cp); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "malformed":
				mustWrite(t, path, []byte("private corrupt "+serverSecret))
			case "checksum":
				cp.DataHash = "wrong"
			case "semantic":
				var value timelineResult
				if err := json.Unmarshal(cp.Data, &value); err != nil {
					t.Fatal(err)
				}
				value.Candidates[0].Evidence = "fabricated"
				cp.Data, err = json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				cp.DataHash = digest(cp.Data)
			case "previous":
				cp.Previous = "wrong"
			case "options":
				opts.Duration++
			case "cues":
				cues[0].Text = "changed evidence"
			case "version":
				cp.Version = "old"
			}
			if mutation != "malformed" {
				raw, err = json.Marshal(cp)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, path, raw)
			}
			_, _, err = AnalyzeText(context.Background(), client, cues, opts, dir, nil)
			assertCode(t, err, CodeInvalidResponse)
			if model.count() != 4 {
				t.Fatal("bad/stale checkpoint silently spent money")
			}
		})
	}
}

func TestCancellationAndProgressFailure(t *testing.T) {
	t.Run("cancel_after_scoring", func(t *testing.T) {
		model := textModel()
		client, dir := model.client(t), t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _, err := AnalyzeText(ctx, client, fixtureCues(), textOptions(), dir, func(stage string, percent *float64) error {
			if stage == "03-scoring" && *percent == 60 {
				cancel()
			}
			return nil
		})
		assertCode(t, err, CodeTimeout)
		if !errors.Is(err, context.Canceled) || model.count() != 3 {
			t.Fatal("cancel did not stop downstream stages")
		}
		if _, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), dir, nil); err != nil {
			t.Fatal(err)
		}
		if model.count() != 4 {
			t.Fatal("did not preserve stage completed before cancellation")
		}
	})
	t.Run("callback_error", func(t *testing.T) {
		model := textModel()
		_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), "", func(_ string, _ *float64) error {
			return errors.New(testKey + " " + serverSecret)
		})
		assertCode(t, err, CodeInvalidResponse)
		if model.count() != 0 {
			t.Fatal("progress failure incurred a request")
		}
	})
}

func TestCheckpointFilesystemFailureAndCleanup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "01-outline.json"), 0700); err != nil {
		t.Fatal(err)
	}
	model := textModel()
	_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 0 {
		t.Fatal("invalid checkpoint path cost a request")
	}
	target := filepath.Join(t.TempDir(), "directory")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(context.Background(), target, []byte(`{}`)); err == nil {
		t.Fatal("expected rename failure")
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatal("failed atomic write left temporary files")
	}
}

func TestTimelineQualitySafeguards(t *testing.T) {
	cues := fixtureCues()
	makeCandidate := func(start, end float64) domain.Candidate {
		return domain.Candidate{Scene: domain.Scene{Label: "Grounded", Start: start, End: end}}
	}
	for _, tc := range []struct {
		name    string
		items   []domain.Candidate
		profile durationProfile
		want    [][2]float64
	}{
		{"snap", []domain.Candidate{makeCandidate(.5, 39.5)}, durationProfile{Min: 20, Target: 40, Max: 40, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 40}}},
		{"overlap_merge", []domain.Candidate{makeCandidate(0, 40), makeCandidate(20, 60)}, durationProfile{Min: 20, Target: 90, Max: 90, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 60}}},
		{"small_overlap_shift", []domain.Candidate{makeCandidate(0, 40), makeCandidate(30, 80)}, durationProfile{Min: 20, Target: 90, Max: 90, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 40}, {40, 80}}},
		{"short_prefix_merge", []domain.Candidate{makeCandidate(0, 10), makeCandidate(10, 20), makeCandidate(20, 30)}, durationProfile{Min: 20, Target: 60, Max: 60, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 20}, {20, 40}}},
		{"preserve_over_tier", []domain.Candidate{makeCandidate(0, 100)}, durationProfile{Min: 20, Target: 30, Max: 30, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 100}}},
		{"extend", []domain.Candidate{makeCandidate(0, 10)}, durationProfile{Min: 20, Target: 40, Max: 40, MinKeep: 2, MaxKeep: 6}, [][2]float64{{0, 20}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := refineTimeline(tc.items, cues, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateTimeline(result, cues, tc.profile); err != nil {
				t.Fatal(err)
			}
			if len(result.Candidates) != len(tc.want) {
				t.Fatalf("wrong count: %+v", result)
			}
			for i, c := range result.Candidates {
				if c.Start != tc.want[i][0] || c.End != tc.want[i][1] {
					t.Fatalf("wrong bounds: %+v", result)
				}
				if c.Evidence != excerpt(cues, c.Start, c.End) {
					t.Fatal("evidence stale after merge/trim")
				}
			}
		})
	}
	for _, c := range []domain.Candidate{makeCandidate(40, 0), makeCandidate(200, 300), makeCandidate(math.NaN(), 30), makeCandidate(-100, -1)} {
		_, err := refineTimeline([]domain.Candidate{c}, cues, profileFor(120, 30))
		assertCode(t, err, CodeInvalidResponse)
	}
	sparse := []domain.Cue{{Start: 0, End: 1, Text: "first"}, {Start: 59, End: 60, Text: "last"}}
	_, err := refineTimeline([]domain.Candidate{makeCandidate(0, 60)}, sparse, profileFor(60, 60))
	assertCode(t, err, CodeInvalidResponse)
	tiny := []domain.Cue{{Start: 0, End: 3, Text: "brief"}}
	result, err := refineTimeline([]domain.Candidate{makeCandidate(0, 3)}, tiny, profileFor(3, 30))
	if err != nil || len(result.Candidates) != 1 {
		t.Fatal("tiny source lost", err)
	}
}

func TestSelectionAndInputValidation(t *testing.T) {
	var candidates []domain.Candidate
	for i := 0; i < 10; i++ {
		candidates = append(candidates, domain.Candidate{Scene: domain.Scene{ID: fmt.Sprint(i), Start: float64(i)}, Score: .1})
	}
	selected := selectCandidates(candidates, profileFor(100, 30))
	if len(selected) != 2 {
		t.Fatal("valid low scores must fill minimum")
	}
	for i := range candidates {
		candidates[i].Score = .99
	}
	if len(selectCandidates(candidates, profileFor(100, 30))) != 6 {
		t.Fatal("selection cap not enforced")
	}
	for _, cues := range [][]domain.Cue{nil, {{Start: 1, End: 0, Text: "x"}}, {{Start: 0, End: 1, Text: ""}}, {{Start: 0, End: math.Inf(1), Text: "x"}}} {
		_, _, err := normalizeCues(cues)
		assertCode(t, err, CodeInvalidResponse)
	}
	for _, opts := range []domain.AnalysisOptions{
		{Duration: -1}, {Duration: 1801}, {Aspect: "bad"}, {Mode: "bad"},
		{Goals: []string{"bad"}}, {Goals: []string{"content", "content"}},
	} {
		_, err := normalizeOptions(opts)
		assertCode(t, err, CodeInvalidResponse)
	}
	large := make([]domain.Cue, 100)
	for i := range large {
		large[i] = domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: strings.Repeat("a", 2000)}
	}
	chunks := chunkCues(large)
	if len(chunks) < 3 {
		t.Fatal("long text not chunked")
	}
	consumed := 0
	for _, chunk := range chunks {
		consumed += len(chunk)
	}
	if consumed != len(large) {
		t.Fatal("chunking lost evidence")
	}
}

func TestStrictJSON(t *testing.T) {
	type schema struct {
		Value string `json:"value"`
	}
	for _, raw := range []string{`{"value":"ok"}`, "```json\n{\"value\":\"ok\"}\n```"} {
		var dst schema
		if err := decodeJSON(raw, &dst); err != nil || dst.Value != "ok" {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{
		"", `{"value":"x","value":"y"}`, `{"value":3}`, `{"value":"ok","extra":1}`, `{"value":"ok"} {}`,
		"prefix {\"value\":\"x\"}", "```json\n{}", string([]byte{0xff}),
		strings.Repeat("[", 66) + strings.Repeat("]", 66), `{"value":"` + serverSecret,
	} {
		var dst schema
		assertCode(t, decodeJSON(raw, &dst), CodeInvalidResponse)
	}
	for _, raw := range []string{`"00:00:01,500"`, `"01:02.5"`, `1.5`} {
		var value timestamp
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`"00:60:01"`, `"99:00"`, `"00:00:1e2"`, `"00:-1:10"`, `null`, `"not-time"`, `1e999`} {
		var value timestamp
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatal("accepted malformed timestamp", raw)
		}
	}
}

// The requested duration caps the profile's Max, and Min used to be clamped to
// that same value. The window then collapsed to a single point, validateTimeline
// demanded a span of exactly Max seconds, and refineTimeline could only extend to
// a cue edge strictly below it — so every candidate was dropped and the stage
// failed after the outline and timeline calls had already been billed. The
// default target of 30 made this certain for every source past eight minutes.
func TestProfileAlwaysLeavesAUsableDurationWindow(t *testing.T) {
	for _, duration := range []float64{3, 60, 300, 479, 480, 600, 1799, 1800, 3600, 7200} {
		for _, target := range []int{0, 10, 15, 30, 60, 120} {
			p := profileFor(duration, target)
			// A point-width window cannot be satisfied by cue-aligned bounds.
			if p.Min >= p.Max {
				t.Errorf("duration=%v target=%d: window collapsed to [%v,%v]", duration, target, p.Min, p.Max)
			}
			if p.Min <= 0 || !finite(p.Min) || !finite(p.Max) {
				t.Errorf("duration=%v target=%d: nonpositive or nonfinite bound [%v,%v]", duration, target, p.Min, p.Max)
			}
			// Subtitle targets are advisory; only source bounds are mandatory.
			if p.Max > duration+1e-9 {
				t.Errorf("duration=%v target=%d: Max %v exceeds the source", duration, target, p.Max)
			}
			if p.Target > duration+1e-9 || (target > 0 && p.Target > float64(target)+1e-9) {
				t.Errorf("duration=%v target=%d: Target %v exceeds the request or the source", duration, target, p.Target)
			}
		}
	}
}

// End-to-end proof for the collapse: a ten-minute source at the default target
// used to fail the timeline stage outright. Real cue-aligned bounds must survive.
func TestTenMinuteSourceSurvivesTimelineAtDefaultTarget(t *testing.T) {
	var cues []domain.Cue
	for i := 0; i < 120; i++ { // 600 s of 5-second cues.
		cues = append(cues, domain.Cue{Start: float64(i * 5), End: float64((i + 1) * 5), Text: fmt.Sprintf("Sentence %d.", i)})
	}
	p := profileFor(600, 30)
	candidate := domain.Candidate{Scene: domain.Scene{ID: "text-1", Label: "Topic", Start: 0, End: 25}, Kind: "text"}
	result, err := refineTimeline([]domain.Candidate{candidate}, cues, p)
	if err != nil {
		t.Fatalf("a 10-minute source at the default target must produce a timeline: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected the candidate to survive, got %d", len(result.Candidates))
	}
	if err := validateTimeline(result, cues, p); err != nil {
		t.Fatalf("refined timeline must pass its own validation: %v", err)
	}
}

// Upstream quality.py _to_score rescales a 0-10 or 0-100 answer and
// align_scores back-fills anything the model skipped, so an imperfect scoring
// response costs one candidate's ranking rather than the whole billed run. The
// Go port used to reject all three of these outright.
func TestScoringToleratesScaleOmissionAndMissingReason(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		want        map[string]float64
	}{
		{"0_to_100_scale", `[{"id":"text-1","score":90,"reason":"a"},{"id":"text-2","score":85,"reason":"b"}]`,
			map[string]float64{"text-1": .9, "text-2": .85}},
		{"0_to_10_scale", `[{"id":"text-1","score":9,"reason":"a"},{"id":"text-2","score":8.5,"reason":"b"}]`,
			map[string]float64{"text-1": .9, "text-2": .85}},
		// A skipped candidate, a null score and a missing reason each fall back.
		{"omitted_candidate", `[{"id":"text-1","score":0.9,"reason":"a"}]`,
			map[string]float64{"text-1": .9, "text-2": .5}},
		{"null_score", `[{"id":"text-1","score":0.9,"reason":"a"},{"id":"text-2","score":null,"reason":"b"}]`,
			map[string]float64{"text-1": .9, "text-2": .5}},
		{"missing_reason", `[{"id":"text-1","score":0.9,"reason":"a"},{"id":"text-2","score":0.85,"reason":""}]`,
			map[string]float64{"text-1": .9, "text-2": .85}},
		{"empty_array", `[]`, map[string]float64{"text-1": .5, "text-2": .5}},
		// Fabricated IDs are dropped rather than trusted; the real candidate
		// still gets its fallback so the run continues.
		{"unknown_id", `[{"id":"invented","score":0.9,"reason":"a"},{"id":"text-1","score":0.8,"reason":"b"}]`,
			map[string]float64{"text-1": .8, "text-2": .5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := textModel()
			model.replies["scoring"] = tc.reply
			_, candidates, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
			if err != nil {
				t.Fatalf("an imperfect scoring response must not discard the run: %v", err)
			}
			for _, c := range candidates {
				want, ok := tc.want[c.ID]
				if !ok {
					t.Fatalf("unexpected candidate %q", c.ID)
				}
				if math.Abs(c.Score-want) > 1e-9 {
					t.Errorf("%s: score %v, want %v", c.ID, c.Score, want)
				}
			}
			if len(candidates) != len(tc.want) {
				t.Fatalf("expected %d candidates, got %d", len(tc.want), len(candidates))
			}
		})
	}
}

// A duplicated ID is ambiguous rather than merely absent, so both copies are
// discarded and the candidate takes the neutral fallback instead of one of two
// conflicting scores being picked arbitrarily.
func TestScoringDropsAmbiguousDuplicateIDs(t *testing.T) {
	model := textModel()
	model.replies["scoring"] = `[{"id":"text-1","score":0.9,"reason":"a"},{"id":"text-1","score":0.2,"reason":"b"},{"id":"text-2","score":0.8,"reason":"c"}]`
	_, candidates, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a duplicate must not discard the run: %v", err)
	}
	for _, c := range candidates {
		if c.ID == "text-1" && math.Abs(c.Score-.5) > 1e-9 {
			t.Fatalf("conflicting duplicate should fall back to 0.5, got %v", c.Score)
		}
	}
}

// Rescaling must not invent a score from a value that is not a number at all,
// nor accept one beyond any plausible scale.
func TestNormalizeScoreRejectsNonNumericAndOutOfScale(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, 101, 1e9} {
		if _, ok := normalizeScore(bad); ok {
			t.Errorf("normalizeScore accepted %v", bad)
		}
	}
	for _, tc := range []struct{ in, want float64 }{{0, 0}, {1, 1}, {0.85, .85}, {8.5, .85}, {85, .85}, {100, 1}, {10, 1}} {
		got, ok := normalizeScore(tc.in)
		if !ok || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("normalizeScore(%v) = %v,%v; want %v", tc.in, got, ok, tc.want)
		}
	}
}

// Upstream step1_outline.py keeps a topic with no bullet points (its parser
// produces an empty subtopics list without complaint) and _merge_outlines keeps
// the first of a duplicate title, so neither discards the run. A chunk with
// nothing to outline is skipped and accounted for; only a globally empty outline
// fails. A structurally malformed chunk still stops before the next paid call.
func TestOutlineToleratesEmptySubtopicsDuplicatesAndSilentChunks(t *testing.T) {
	t.Run("empty_subtopics", func(t *testing.T) {
		model := textModel()
		model.replies["outline"] = `[{"title":"Topic A","subtopics":[]},{"title":"Topic B","subtopics":["point"]}]`
		if _, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil); err != nil {
			t.Fatalf("a topic without bullet points must be usable: %v", err)
		}
	})
	t.Run("duplicate_title_keeps_first", func(t *testing.T) {
		model := textModel()
		model.replies["outline"] = `[{"title":"Same","subtopics":["first"]},{"title":"Same","subtopics":["second"]},{"title":"Other","subtopics":["x"]}]`
		if _, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil); err != nil {
			t.Fatalf("a duplicate title must not discard the run: %v", err)
		}
	})
	t.Run("all_chunks_empty_still_fails", func(t *testing.T) {
		model := textModel()
		model.replies["outline"] = `[]`
		_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
		var e *Error
		if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
			t.Fatalf("an outline with no usable topics anywhere must still fail, got %v", err)
		}
	})
	t.Run("malformed_chunk_still_stops_early", func(t *testing.T) {
		model := textModel()
		model.replies["outline"] = `[{"title":"","subtopics":["x"]}]`
		cues := []domain.Cue{{Start: 0, End: 30, Text: "first"}, {Start: 2000, End: 2030, Text: "second"}}
		_, _, err := AnalyzeText(context.Background(), model.client(t), cues, textOptions(), "", nil)
		assertCode(t, err, CodeInvalidResponse)
		if model.count() != 1 {
			t.Fatal("a malformed chunk must not spend on later chunks")
		}
	})
}

// A chunk with nothing to outline is skipped, the run continues on the rest, and
// the degradation is reported through progress rather than silently narrowing
// the outline. Upstream step1_outline.py behaves the same way and only raises
// when every chunk failed.
func TestOutlineSkipsOneSilentChunkAndReportsIt(t *testing.T) {
	cues := make([]domain.Cue, 2)
	for i := range cues {
		cues[i] = domain.Cue{Start: float64(i * 1800), End: float64(i*1800 + 30), Text: fmt.Sprintf("Chunk %d speech", i)}
	}
	outlineCalls := 0
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		prompt, _ := decodeRequest(t, r)
		stage := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		switch stage {
		case "outline":
			outlineCalls++
			if outlineCalls == 1 {
				answer(t, w, `[]`) // This chunk had nothing to outline.
				return
			}
			answer(t, w, `[{"title":"Real topic","subtopics":["point"]}]`)
		case "timeline":
			var input struct {
				Cues   []domain.Cue `json:"cues"`
				Topics []topic      `json:"topics"`
			}
			raw := strings.SplitN(prompt, "INPUT_JSON:\n", 2)
			if len(raw) != 2 || json.Unmarshal([]byte(raw[1]), &input) != nil || len(input.Topics) == 0 || len(input.Cues) == 0 {
				t.Error("timeline prompt lost its evidence")
				w.WriteHeader(500)
				return
			}
			answer(t, w, fmt.Sprintf(`[{"topic_id":%q,"start":%v,"end":%v}]`, input.Topics[0].ID, input.Cues[0].Start, input.Cues[0].End))
		case "scoring":
			answer(t, w, `[{"id":"text-1","score":0.9,"reason":"one"}]`)
		case "titles":
			answer(t, w, `[{"id":"text-1","title":"One","hook":""}]`)
		default:
			t.Error("unexpected stage", stage)
		}
	})
	var stages []string
	opts := textOptions()
	opts.Duration = 30
	drafts, _, err := AnalyzeText(context.Background(), client, cues, opts, t.TempDir(), func(name string, _ *float64) error {
		stages = append(stages, name)
		return nil
	})
	if err != nil {
		t.Fatalf("one silent chunk must not discard the run: %v", err)
	}
	if len(drafts) == 0 {
		t.Fatal("expected drafts from the surviving chunk")
	}
	if outlineCalls != 2 {
		t.Fatalf("expected both chunks attempted, got %d outline calls", outlineCalls)
	}
	partial := false
	for _, s := range stages {
		if strings.HasPrefix(s, "01-outline-partial-") {
			partial = true
		}
	}
	if !partial {
		t.Fatalf("a skipped chunk must be reported in progress, got %v", stages)
	}
}

// Upstream step2_timeline.py clamps a bound back into its chunk and skips an
// item it cannot use, rather than failing the whole stage. The port used to
// reject each of these outright, discarding an already billed outline.
func TestTimelineClampsAndSkipsInsteadOfFailing(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		// A bound far past the chunk end is clamped to the last cue.
		{"overlong_end", `[{"topic_id":"topic-1","start":0,"end":9999},{"topic_id":"topic-2","start":60,"end":100}]`},
		// A negative start is clamped to the chunk start.
		{"negative_start", `[{"topic_id":"topic-1","start":-500,"end":40},{"topic_id":"topic-2","start":60,"end":100}]`},
		// A duplicate topic keeps the first placement and skips the second.
		{"duplicate_topic", `[{"topic_id":"topic-1","start":0,"end":40},{"topic_id":"topic-1","start":60,"end":100}]`},
		// A hallucinated ID is skipped; the real one still places.
		{"unknown_topic", `[{"topic_id":"invented","start":0,"end":40},{"topic_id":"topic-1","start":0,"end":40}]`},
		// A reversed interval is skipped rather than fatal.
		{"reversed", `[{"topic_id":"topic-1","start":40,"end":0},{"topic_id":"topic-2","start":60,"end":100}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := textModel()
			model.replies["timeline"] = tc.reply
			_, candidates, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
			if err != nil {
				t.Fatalf("an imperfect timeline item must not discard the run: %v", err)
			}
			if len(candidates) == 0 {
				t.Fatal("expected at least one placed candidate")
			}
			// Whatever survives must still be grounded in real cue boundaries and
			// inside the source; tolerance must not become fabrication.
			for _, c := range candidates {
				if c.Start < 0 || c.End > 120+1e-6 || c.End <= c.Start {
					t.Errorf("candidate %q escaped the source: [%v,%v]", c.ID, c.Start, c.End)
				}
			}
		})
	}
}

// A timeline where nothing at all is placeable must still fail rather than
// invent candidates.
func TestTimelineWithNothingPlaceableStillFails(t *testing.T) {
	model := textModel()
	model.replies["timeline"] = `[{"topic_id":"invented-1","start":0,"end":40}]`
	_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("an entirely unplaceable timeline must fail, got %v", err)
	}
}

// Upstream _snap_start/_snap_end always land on a real cue. Returning the raw
// second instead made cueBoundaries fail and aborted the timeline stage whenever
// a bound fell in a silent gap wider than the snap window.
func TestBoundaryAlwaysLandsOnACueEdge(t *testing.T) {
	// A deliberately sparse track: a long silent gap between 5s and 200s.
	cues := []domain.Cue{
		{Start: 0, End: 5, Text: "opening"},
		{Start: 200, End: 205, Text: "after a long pause"},
	}
	for _, sec := range []float64{-10, 0, 2.5, 50, 100, 150, 202, 300, 1e6} {
		for _, isStart := range []bool{true, false} {
			got := boundary(cues, sec, isStart)
			onEdge := false
			for _, cue := range cues {
				if math.Abs(cue.Start-got) < 1e-9 || math.Abs(cue.End-got) < 1e-9 {
					onEdge = true
				}
			}
			if !onEdge {
				t.Errorf("boundary(%v, start=%v) = %v, which is not a cue edge", sec, isStart, got)
			}
		}
	}
	// With no cues at all there is nothing to snap to; the input is returned
	// unchanged rather than indexing an empty slice.
	if got := boundary(nil, 42, true); got != 42 {
		t.Errorf("boundary with no cues = %v, want the input unchanged", got)
	}
}

// Upstream step4_title.py falls back to the clip's own outline text when the
// model skips or mistitles one, rather than discarding clips that are already
// verified, scored and subtitle-grounded. The port used to reject all of these.
func TestTitlesFallBackToTheVerifiedLabel(t *testing.T) {
	for _, tc := range []struct{ name, reply string }{
		{"duplicate_id", `[{"id":"text-1","title":"x","hook":""},{"id":"text-1","title":"y","hook":""}]`},
		{"empty_title", `[{"id":"text-1","title":"","hook":""},{"id":"text-2","title":"y","hook":""}]`},
		{"missing_candidate", `[{"id":"text-1","title":"Only one","hook":""}]`},
		{"unknown_id", `[{"id":"invented","title":"x","hook":""},{"id":"text-1","title":"Real","hook":""}]`},
		{"empty_array", `[]`},
		{"oversized_hook", `[{"id":"text-1","title":"Fine","hook":"` + strings.Repeat("h", 200) + `"},{"id":"text-2","title":"Also fine","hook":""}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := textModel()
			model.replies["titles"] = tc.reply
			drafts, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), textOptions(), t.TempDir(), nil)
			if err != nil {
				t.Fatalf("an imperfect titles response must not discard the run: %v", err)
			}
			if len(drafts) == 0 {
				t.Fatal("expected drafts to survive")
			}
			// Every draft must still carry a real, bounded title.
			for _, d := range drafts {
				if strings.TrimSpace(d.Title) == "" || len([]rune(d.Title)) > 200 {
					t.Errorf("draft %q has an unusable title %q", d.ID, d.Title)
				}
				if len([]rune(d.Hook)) > 120 {
					t.Errorf("draft %q has an oversized hook", d.ID)
				}
			}
		})
	}
}

// Upstream selects prompts from backend/prompt/<category>/ and falls back to the
// shared file per stage when the category lacks one (get_prompt_files). The Go
// port had no category concept, so every source used the generic prompts.
func TestCategoryPromptsResolveWithFallback(t *testing.T) {
	base, err := promptFile("", "outline")
	if err != nil {
		t.Fatal(err)
	}
	for _, category := range Categories {
		got, err := promptFile(category, "outline")
		if err != nil {
			t.Fatalf("%s: outline prompt missing: %v", category, err)
		}
		if string(got) == string(base) {
			t.Errorf("%s: outline prompt is identical to the shared one", category)
		}
		if tl, err := promptFile(category, "timeline"); err != nil || len(tl) == 0 {
			t.Errorf("%s: timeline prompt missing: %v", category, err)
		}
		// Stages with no category-specific file fall back rather than failing.
		shared, err := promptFile("", "scoring")
		if err != nil {
			t.Fatal(err)
		}
		fellBack, err := promptFile(category, "scoring")
		if err != nil || string(fellBack) != string(shared) {
			t.Errorf("%s: scoring should fall back to the shared prompt", category)
		}
	}
	// An empty or unknown category uses the shared prompt and never escapes the
	// embedded directory, even with traversal in the name.
	for _, category := range []string{"", "unknown", "../..", "knowledge/../.."} {
		got, err := promptFile(category, "outline")
		if err != nil || string(got) != string(base) {
			t.Errorf("category %q must resolve to the shared prompt, got err=%v", category, err)
		}
	}
}

// The category reaches the model: a knowledge analysis must be prompted with the
// knowledge outline text, not the generic one.
func TestCategoryReachesThePrompt(t *testing.T) {
	want, err := promptFile("knowledge", "outline")
	if err != nil {
		t.Fatal(err)
	}
	var sawKnowledgePrompt bool
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		prompt, _ := decodeRequest(t, r)
		stage := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		switch stage {
		case "outline":
			if strings.Contains(prompt, string(want)) {
				sawKnowledgePrompt = true
			}
			answer(t, w, `[{"title":"Topic A","subtopics":["point"]}]`)
		case "timeline":
			answer(t, w, `[{"topic_id":"topic-1","start":0,"end":40}]`)
		case "scoring":
			answer(t, w, `[{"id":"text-1","score":0.9,"reason":"x"}]`)
		case "titles":
			answer(t, w, `[{"id":"text-1","title":"T","hook":""}]`)
		default:
			t.Error("unexpected stage", stage)
		}
	})
	opts := textOptions()
	opts.Category = "knowledge"
	if _, _, err := AnalyzeText(context.Background(), client, fixtureCues(), opts, t.TempDir(), nil); err != nil {
		t.Fatalf("knowledge analysis failed: %v", err)
	}
	if !sawKnowledgePrompt {
		t.Fatal("the knowledge outline prompt never reached the model")
	}
}

// An unknown category is rejected before any paid call rather than silently
// falling back, so a typo does not quietly produce generic results.
func TestUnknownCategoryIsRejectedBeforeSpending(t *testing.T) {
	model := textModel()
	opts := textOptions()
	opts.Category = "not-a-category"
	_, _, err := AnalyzeText(context.Background(), model.client(t), fixtureCues(), opts, t.TempDir(), nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		t.Fatalf("an unknown category must be rejected, got %v", err)
	}
	if model.count() != 0 {
		t.Fatalf("rejected options must not spend, got %d calls", model.count())
	}
}

// The profile now carries upstream's prompt_hint: the model is told this
// source's topic count and clip bounds, and that they outrank the prompt body.
func TestProfileCarriesTopicAndPriorityGuidance(t *testing.T) {
	for _, tc := range []struct {
		duration      float64
		tier          string
		wantTopicsLow int
	}{
		{300, "short", 3}, {600, "medium", 4}, {3600, "long", 6},
	} {
		p := profileFor(tc.duration, 60)
		if p.Tier != tc.tier {
			t.Errorf("duration %v: tier %q, want %q", tc.duration, p.Tier, tc.tier)
		}
		if p.TopicsLow < tc.wantTopicsLow || p.TopicsHigh <= p.TopicsLow {
			t.Errorf("duration %v: topic range %d-%d is not usable", tc.duration, p.TopicsLow, p.TopicsHigh)
		}
		if !strings.Contains(p.Guidance, "outrank") {
			t.Errorf("duration %v: guidance does not assert priority: %q", tc.duration, p.Guidance)
		}
		if !strings.Contains(p.Guidance, "subtitle-cue") {
			t.Errorf("duration %v: guidance omits the cue-boundary rule", tc.duration)
		}
	}
}
