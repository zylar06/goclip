package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
)

func TestInitialPromoOneSceneHasThreeDistinctOpeningsAndReplay(t *testing.T) {
	model := textModel()
	model.replies["promo"] = `[
{"candidate_id":"source-0","title":"Look closer","hook":"What stands out in this scene?"},
{"candidate_id":"source-0","title":"The visible detail","hook":"Start with the detail on screen."},
{"candidate_id":"source-0","title":"A different perspective","hook":"Watch this moment from another angle."}]`
	client, dir := model.client(t), t.TempDir()
	opts, candidates := visualOptions(), promoCandidates()[:1]
	drafts, err := MakePromos(context.Background(), client, candidates, opts, dir, nil)
	if err != nil || len(drafts) != 3 {
		t.Fatalf("one scene should support three initial openings: %+v %v", drafts, err)
	}
	ids, hooks := map[string]bool{}, map[string]bool{}
	for _, d := range drafts {
		if len(d.Scenes) != 1 || d.Scenes[0] != candidates[0].Scene || ids[d.ID] || hooks[d.Hook] || d.Subtitles {
			t.Fatalf("variant duplicated opening/identity or changed scene: %+v", d)
		}
		ids[d.ID], hooks[d.Hook] = true, true
	}
	replay, err := MakePromos(context.Background(), client, candidates, opts, dir, nil)
	if err != nil || model.count() != 1 || !reflect.DeepEqual(drafts, replay) {
		t.Fatalf("promo replay repeated a request or changed variants: %v", err)
	}
}

func TestInitialPromosAllowOneToThreeArbitraryCandidateReferences(t *testing.T) {
	for count := 1; count <= 3; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			candidates := promoCandidates()
			// Use references beyond the former top-three truncation and allow
			// arbitrary order/subsets rather than one output per input.
			indices := []int{5, 3, 0}
			var wire []promoWire
			for _, index := range indices[:count] {
				wire = append(wire, promoWire{CandidateID: candidates[index].ID, Title: fmt.Sprintf("Opening %d", index)})
			}
			raw, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			model := textModel()
			model.replies["promo"] = string(raw)
			drafts, err := MakePromos(context.Background(), model.client(t), candidates, textOptions(), "", nil)
			if err != nil || len(drafts) != count || model.count() != 1 {
				t.Fatalf("subset promo mapping failed: count=%d err=%v", count, err)
			}
			for i, d := range drafts {
				if d.Scenes[0] != candidates[indices[i]].Scene {
					t.Fatal("candidate_id mapping changed the verified scene")
				}
			}
		})
	}
}

func TestPromoRejectsDuplicateOpeningsAndInvalidWireWithoutRepair(t *testing.T) {
	for _, reply := range []string{
		`[]`, `null`,
		`[{"candidate_id":"unknown","title":"Opening","hook":""}]`,
		`[{"id":"source-0","title":"Old schema","hook":""}]`,
		`[{"candidate_id":"source-0","title":"Opening","hook":"","start":5}]`,
		`[{"candidate_id":"source-0","title":"One","hook":"Same hook"},{"candidate_id":"source-0","title":"Two","hook":" same   HOOK "}]`,
		`[{"candidate_id":"source-0","title":" Same opening ","hook":""},{"candidate_id":"source-0","title":"same OPENING","hook":""}]`,
		`[{"candidate_id":"source-0","title":"One","hook":"Copied"},{"candidate_id":"source-1","title":"Two","hook":"Copied"}]`,
		`[{"candidate_id":"source-0","title":"1"},{"candidate_id":"source-0","title":"2"},{"candidate_id":"source-0","title":"3"},{"candidate_id":"source-0","title":"4"}]`,
	} {
		t.Run(reply, func(t *testing.T) {
			model := textModel()
			model.replies["promo"] = reply
			dir := t.TempDir()
			_, err := MakePromos(context.Background(), model.client(t), promoCandidates(), textOptions(), dir, nil)
			assertCode(t, err, CodeInvalidResponse)
			files, readErr := os.ReadDir(dir)
			if readErr != nil || len(files) != 0 || model.count() != 1 {
				t.Fatalf("bad initial promo was retried/repaired/saved: calls=%d err=%v", model.count(), readErr)
			}
		})
	}
}

func TestProfileRestoresAdvisoryTierMaxima(t *testing.T) {
	for _, tc := range []struct {
		source, maximum, defaultTarget float64
	}{
		{20, 20, 20}, {300, 150, 60}, {479, 150, 60},
		{480, 300, 120}, {1799, 300, 120}, {1800, 480, 240}, {7200, 480, 240},
	} {
		for _, target := range []int{0, 15, 30, 120} {
			p := profileFor(tc.source, target)
			if p.Max != tc.maximum || p.HardMax > 1800 || p.HardMax > tc.source {
				t.Fatalf("lost tier/safety distinction: source=%v target=%d profile=%+v", tc.source, target, p)
			}
			if target == 0 && p.Target != tc.defaultTarget {
				t.Fatalf("automatic target lost tier guidance: %+v", p)
			}
			if !strings.Contains(p.Guidance, "Prefer segments") ||
				!strings.Contains(p.Guidance, "not a desired length") ||
				!strings.Contains(p.Guidance, "never blindly truncate") {
				t.Fatalf("profile encourages hard cuts or excessive length: %s", p.Guidance)
			}
		}
	}
}

func TestOverTierSemanticUnitPreservedWarnedAndCheckpointed(t *testing.T) {
	var cues []domain.Cue
	for i := 0; i < 30; i++ {
		cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: "A complete supported thought."})
	}
	model := textModel()
	model.replies["outline"] = `[{"title":"Complete thought"}]`
	model.replies["timeline"] = `[{"topic_id":"topic-1","start":0,"end":200}]`
	model.replies["scoring"], model.replies["titles"] = `[]`, `[]`
	opts, client, dir := textOptions(), model.client(t), t.TempDir()
	opts.Duration = 30
	for run := 0; run < 2; run++ {
		stages, progress := progressRecorder(t)
		drafts, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, progress)
		if err != nil || len(drafts) != 1 || drafts[0].Scenes[0].End != 200 || model.count() != 4 {
			t.Fatalf("over-tier semantic span lost or replay spent: %v", err)
		}
		found := 0
		for _, stage := range *stages {
			if stage == "02-timeline-over-tier" {
				found++
			}
		}
		if found != 1 {
			t.Fatal("over-tier result was not surfaced once, including replay")
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "02-timeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	var result timelineResult
	if json.Unmarshal(raw, &cp) != nil || json.Unmarshal(cp.Data, &result) != nil {
		t.Fatal("cannot read timeline checkpoint")
	}
	if result.Report.OverTier != 1 || result.Report.Trimmed != 0 {
		t.Fatalf("missing over-tier audit or blind trim: %+v", result.Report)
	}
	result.Report.OverTier = 0
	if err := validateTimeline(result, cues, profileFor(300, 30)); err == nil {
		t.Fatal("replay validator ignored inconsistent over-tier report")
	}
}

func TestLongSemanticUnitsStillRequireSafetyAndCoverage(t *testing.T) {
	var cues []domain.Cue
	for i := 0; i < 200; i++ {
		cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: "Source evidence."})
	}
	item := domain.Candidate{Scene: domain.Scene{Label: "Too long", Start: 0, End: 1900}}
	_, err := refineTimeline([]domain.Candidate{item}, cues, profileFor(2000, 0))
	ae := assertCode(t, err, CodeInvalidResponse)
	if !strings.Contains(ae.Message, "hard safety budget") || item.End != 1900 {
		t.Fatal("unsafe long passage was silently cut")
	}
	sparse := []domain.Cue{{Start: 0, End: 10, Text: "First"}, {Start: 200, End: 210, Text: "Last"}}
	item.End = 210
	_, err = refineTimeline([]domain.Candidate{item}, sparse, profileFor(300, 0))
	assertCode(t, err, CodeInvalidResponse)
}

func TestPromoPromptVersionInvalidatesLegacyCheckpointWithoutSpending(t *testing.T) {
	model := textModel()
	client, candidates, dir := model.client(t), promoCandidates()[:1], t.TempDir()
	opts, err := normalizeOptions(textOptions())
	if err != nil {
		t.Fatal(err)
	}
	// This is the old fingerprint, which omitted a promo-specific version.
	r, err := newRunner(context.Background(), client, struct {
		Candidates []domain.Candidate     `json:"candidates"`
		Options    domain.AnalysisOptions `json:"options"`
	}{candidates, opts}, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = stage(context.Background(), r, "01-promo-titles", 0, 90, func() ([]titleItem, error) {
		return []titleItem{{ID: candidates[0].ID, Title: "Legacy"}}, nil
	}, func(items *[]titleItem) error { return validateTitles(*items, candidates) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = MakePromos(context.Background(), client, candidates, opts, dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 0 {
		t.Fatal("legacy single-title checkpoint triggered a paid replacement")
	}
}
