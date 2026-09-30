package store

import (
	"context"
	"errors"
	"testing"

	"autoclip-go/internal/domain"
)

func TestAmbiguousLegacyCandidateOwnershipPreservesEvidence(t *testing.T) {
	s, p, flow, job := productionSetup(t)
	d := domain.NewDraft("content", []domain.Scene{{ID: "text-1", Start: 0, End: 40}})
	old := []domain.Candidate{{Scene: domain.Scene{ID: "subtitle-text-1", Start: 0, End: 40}, Score: .9}}
	if err := s.ForTask(job).CompleteProductionAnalysis(job.ID, []domain.Draft{d}, old,
		[]domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}}, {Goal: "highlight", Status: "failed", Error: "temporary"}}, true); err != nil {
		t.Fatal(err)
	}
	ex, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ForTask(ex).CompleteExport(ex.ID, p.ID, d); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("DELETE FROM workflow_candidates WHERE workflow_id=?", flow.ID); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Plan(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = s.UpdatePlan(p.ID, domain.PlanUpdate{Revision: plan.Revision, Options: plan.Options})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ConfirmProduction(p.ID, plan.Revision)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := s.Tasks(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.WorkflowID == second.ID {
			if _, err = s.Cancel(task.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err = s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := domain.NewDraft("visual", []domain.Scene{{ID: "event-1", Start: 50, End: 60}})
	err = s.ForTask(job).CompleteProductionAnalysis(job.ID, []domain.Draft{h},
		[]domain.Candidate{{Scene: h.Scenes[0]}},
		[]domain.GoalResult{{Goal: "highlight", Status: "completed", DraftIDs: []string{h.ID}}}, false)
	if !errors.Is(err, ErrLegacyCandidateOwnership) {
		t.Fatal("ambiguous evidence silently adopted", err)
	}
	got, err := s.Candidates(p.ID)
	if err != nil || len(got) != 1 || got[0].ID != old[0].ID {
		t.Fatal("ambiguous old evidence was lost", got, err)
	}
	drafts, err := s.Drafts(p.ID)
	if err != nil || len(drafts) != 1 {
		t.Fatal("failed migration published partial drafts", drafts, err)
	}
}

func TestLegacyPartialWorkflowMigratesProvenCandidateOwnership(t *testing.T) {
	s, p, flow, job := productionSetup(t)
	d := domain.NewDraft("content", []domain.Scene{{ID: "text-1", Start: 0, End: 40}})
	old := []domain.Candidate{{Scene: domain.Scene{ID: "subtitle-text-1", Start: 0, End: 40}, Score: .9}}
	if err := s.ForTask(job).CompleteProductionAnalysis(job.ID, []domain.Draft{d}, old,
		[]domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}}, {Goal: "highlight", Status: "failed", Error: "temporary"}}, true); err != nil {
		t.Fatal(err)
	}
	ex, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ForTask(ex).CompleteExport(ex.ID, p.ID, d); err != nil {
		t.Fatal(err)
	}
	// Existing schema-2 partial workflows created before the new table have
	// only project candidates; Open's CREATE TABLE does not backfill this row.
	if _, err := s.DB.Exec("DELETE FROM workflow_candidates WHERE workflow_id=?", flow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := domain.NewDraft("visual", []domain.Scene{{ID: "event-1", Start: 50, End: 60}})
	next := []domain.Candidate{{Scene: domain.Scene{ID: "visual-event-1", Start: 50, End: 60}, Score: .8}}
	if err := s.ForTask(job).CompleteProductionAnalysis(job.ID, []domain.Draft{h}, next,
		[]domain.GoalResult{{Goal: "highlight", Status: "completed", DraftIDs: []string{h.ID}}}, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Candidates(p.ID)
	if err != nil || len(got) != 2 {
		t.Fatalf("pre-fix partial workflow lost successful candidate evidence on retry: candidates=%+v err=%v", got, err)
	}
}
