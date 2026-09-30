package ai

import (
	"testing"

	"autoclip-go/internal/domain"
)

func TestFuseCandidatesMergesJointEvidenceAndRanksIt(t *testing.T) {
	text := []domain.Candidate{{Scene: domain.Scene{ID: "text-1", Label: "进球发生", Start: 10, End: 22, Evidence: "字幕出现进球"}, Score: .8, Kind: "content"}}
	visual := []domain.Candidate{{Scene: domain.Scene{ID: "visual-1", Label: "射门得分", Start: 12, End: 24, Evidence: "球进入球门"}, Score: .9, Kind: "gameplay"}}
	got := FuseCandidates(text, visual)
	if len(got) != 1 || got[0].Kind != "joint" || got[0].Score <= .85 {
		t.Fatalf("expected one joint high score, got %+v", got)
	}
	if got[0].Start != 10 || got[0].End != 24 || got[0].Evidence == "" {
		t.Fatalf("joint scene lost bounds/evidence: %+v", got[0])
	}
}

func TestFuseCandidatesKeepsSingleSideEvidence(t *testing.T) {
	got := FuseCandidates(nil, []domain.Candidate{{Scene: domain.Scene{ID: "v", Start: 0, End: 4}, Score: .7}})
	if len(got) != 1 || got[0].Kind != "visual-only" || got[0].Score != .7 {
		t.Fatalf("expected visual-only candidate, got %+v", got)
	}
}
