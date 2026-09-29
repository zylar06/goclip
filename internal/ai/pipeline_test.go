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
		"outline":    `[{"title":"Topic A","subtopics":["point A"]},{"title":"Topic B","subtopics":["point B"]}]`,
		"timeline":   `[{"topic_id":"topic-1","start":0.5,"end":39.7},{"topic_id":"topic-2","start":"01:00","end":"00:01:40,000"}]`,
		"scoring":    `[{"id":"text-2","score":0.85,"reason":"second excerpt"},{"id":"text-1","score":0.9,"reason":"first excerpt"}]`,
		"titles":     `[{"id":"text-2","title":"Second","hook":"Second hook"},{"id":"text-1","title":"First","hook":"First hook"}]`,
		"clustering": `[{"title":"Related topics","hook":"Collection hook","candidate_ids":["text-2","text-1"]}]`,
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
	return domain.AnalysisOptions{Mode: "subtitle", Duration: 40, Language: "source", Aspect: "portrait", Confirmed: true, Goals: []string{"content"}}
}

func TestSixStageTextPipelineAndCheckpoints(t *testing.T) {
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
	if model.count() != 5 || len(drafts) != 3 || len(candidates) != 2 || len(stages) != 12 || last != 100 {
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
	if drafts[0].Title != "First" || drafts[2].Scenes[0].ID != "text-2" || drafts[2].Scenes[1].ID != "text-1" {
		t.Fatal("title association or collection order wrong")
	}
	for _, d := range drafts {
		if err := d.Validate(120); err != nil {
			t.Fatal(err)
		}
		if d.Layout != "crop" || d.Aspect != "portrait" || !d.Subtitles {
			t.Fatal("lost preferences")
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("want six checkpoints, got %d", len(files))
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
	if model.count() != 5 || !reflect.DeepEqual(drafts, replayed) || !reflect.DeepEqual(candidates, replayCandidates) {
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
	if len(files) != 3 {
		t.Fatal("failed stage must not checkpoint")
	}
	model.setFailure("")
	drafts, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.count() != 6 || len(drafts) != 3 {
		t.Fatal("retry did not reuse first three verified stages")
	}
}

func TestTextRejectsInvalidStageJSONWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		stage, body string
		calls       int
	}{
		{"outline", `[]`, 1},
		{"outline", `[{"title":"A","subtopics":[]}]`, 1},
		{"outline", `[{"title":"A","subtopics":["x"],"extra":true}]`, 1},
		{"timeline", `[{"topic_id":"topic-1","end":40}]`, 2},
		{"timeline", `[{"topic_id":"invented","start":0,"end":40}]`, 2},
		{"timeline", `[{"topic_id":"topic-1","start":40,"end":0}]`, 2},
		{"timeline", `[{"topic_id":"topic-1","start":0,"end":9999}]`, 2},
		{"timeline", `[{"topic_id":"topic-1","start":0,"end":40},{"topic_id":"topic-1","start":60,"end":100}]`, 2},
		{"scoring", `[{"id":"text-1","score":1.5,"reason":"x"},{"id":"text-2","score":0.5,"reason":"x"}]`, 3},
		{"scoring", `[{"id":"text-1","reason":"x"},{"id":"text-2","score":0.5,"reason":"x"}]`, 3},
		{"scoring", `[]`, 3},
		{"titles", `[{"id":"text-1","title":"x","hook":""},{"id":"text-1","title":"y","hook":""}]`, 4},
		{"titles", `[{"id":"text-1","title":"","hook":""},{"id":"text-2","title":"y","hook":""}]`, 4},
		{"clustering", `[{"title":"x","hook":"","candidate_ids":["text-1","not-real"]}]`, 5},
		{"clustering", `[{"title":"x","hook":"","candidate_ids":["text-1","text-1"]}]`, 5},
		{"clustering", `null`, 5},
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
			if model.count() != 5 {
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
		if model.count() != 5 {
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
		{"snap", []domain.Candidate{makeCandidate(.5, 39.5)}, durationProfile{20, 40, 40, 2, 6}, [][2]float64{{0, 40}}},
		{"overlap_merge", []domain.Candidate{makeCandidate(0, 40), makeCandidate(20, 60)}, durationProfile{20, 90, 90, 2, 6}, [][2]float64{{0, 60}}},
		{"small_overlap_shift", []domain.Candidate{makeCandidate(0, 40), makeCandidate(30, 80)}, durationProfile{20, 90, 90, 2, 6}, [][2]float64{{0, 40}, {40, 80}}},
		{"short_prefix_merge", []domain.Candidate{makeCandidate(0, 10), makeCandidate(10, 20), makeCandidate(20, 30)}, durationProfile{20, 60, 60, 2, 6}, [][2]float64{{0, 20}, {20, 40}}},
		{"trim", []domain.Candidate{makeCandidate(0, 100)}, durationProfile{20, 30, 30, 2, 6}, [][2]float64{{0, 30}}},
		{"extend", []domain.Candidate{makeCandidate(0, 10)}, durationProfile{20, 40, 40, 2, 6}, [][2]float64{{0, 20}}},
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
		{Duration: -1}, {Duration: 1801}, {Aspect: "bad"}, {Language: "bad"}, {Mode: "bad"},
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
