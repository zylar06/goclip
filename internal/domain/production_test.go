package domain

import "testing"

func TestAnalysisAndSubtitleRenderingAreIndependent(t *testing.T) {
	d := NewDraft("original", []Scene{{ID: ID(), Start: 0, End: 40}})
	if d.Subtitles {
		t.Fatal("new draft burns a second subtitle layer by default")
	}
	p := Project{SubtitleStatus: "available"}
	plan := LocalPlan(p, 1)
	if plan.Options.Mode != "auto" || plan.Options.AllowVisual || plan.Options.BurnSubtitles || plan.Options.Duration != 0 || plan.Options.Confirmed {
		t.Fatal(plan)
	}
	if len(plan.SuggestedGoals) != 1 || plan.SuggestedGoals[0] != "content" {
		t.Fatal(plan)
	}
}

func TestNoAudioDoesNotPretendToKnowSpeech(t *testing.T) {
	no := false
	plan := LocalPlan(Project{HasAudio: &no}, 1)
	if len(plan.SuggestedGoals) != 0 || plan.Options.Confirmed {
		t.Fatal(plan)
	}
}

func TestDraftAndRendererShareMillisecondBoundary(t *testing.T) {
	d := NewDraft("edge", []Scene{{ID: ID(), Start: 0, End: 10.002}})
	if d.Validate(10) == nil {
		t.Fatal("draft accepted an unrenderable source bound")
	}
	d.Scenes[0].End = 10
	if err := d.Validate(10); err != nil {
		t.Fatal(err)
	}
}
