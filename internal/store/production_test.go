package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"autoclip-go/internal/domain"
)

func productionSetup(t *testing.T) (*Store, domain.Project, domain.Workflow, domain.Task) {
	t.Helper()
	s, p, imp := setup(t)
	if _, err := s.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(imp.ID, "completed", "", false); err != nil {
		t.Fatal(err)
	}
	plan, err := s.Plan(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan.Options.Goals = []string{"content", "highlight"}
	plan, err = s.UpdatePlan(p.ID, domain.PlanUpdate{Revision: plan.Revision, Options: plan.Options})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := s.ConfirmProduction(p.ID, plan.Revision)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, p, wf, job
}

func TestProductionConfirmationIsIdempotentAndVersioned(t *testing.T) {
	s, p, wf, job := productionSetup(t)
	again, err := s.ConfirmProduction(p.ID, wf.PlanRevision)
	if err != nil || again.ID != wf.ID {
		t.Fatal("duplicate confirmation", again, err)
	}
	tasks, err := s.Tasks(p.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatal(tasks, err)
	}
	if _, err := s.ConfirmProduction(p.ID, wf.PlanRevision-1); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := s.UpdatePlan(p.ID, domain.PlanUpdate{Revision: wf.PlanRevision}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if job.WorkflowID != wf.ID {
		t.Fatal("missing parent link")
	}
}

func TestProductionPublishesDraftsAndSchedulesContentOnlyAtomically(t *testing.T) {
	s, p, wf, job := productionSetup(t)
	d := domain.NewDraft("content", []domain.Scene{{ID: domain.ID(), Start: 0, End: 45}})
	d.Goal = "content"
	h := domain.NewDraft("highlight", []domain.Scene{{ID: domain.ID(), Start: 50, End: 65}})
	h.Goal = "highlight"
	results := []domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}},
		{Goal: "highlight", Status: "completed", DraftIDs: []string{h.ID}}}
	if err := s.CompleteProductionAnalysis(job.ID, []domain.Draft{d, h}, nil, results, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task(job.ID)
	if err != nil || got.Status != "completed" {
		t.Fatal(got, err)
	}
	next, err := s.Claim(context.Background())
	if err != nil || next.Kind != "export" || next.Goal != "content" {
		t.Fatal(next, err)
	}
	if _, err := s.Claim(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatal("two workers claimed", err)
	}
	if err := s.CompleteExport(next.ID, p.ID, d); err != nil {
		t.Fatal(err)
	}
	flows, err := s.Workflows(p.ID)
	if err != nil || len(flows) != 1 || flows[0].ID != wf.ID || flows[0].Status != "completed" {
		t.Fatal(flows, err)
	}
	exports, err := s.Exports(p.ID)
	if err != nil || len(exports) != 1 {
		t.Fatal(exports, err)
	}
	project, _ := s.Project(p.ID)
	if project.Status != "exported" {
		t.Fatal(project.Status)
	}
}

func TestCancellationDoesNotPublishNewAnalysisResults(t *testing.T) {
	s, p, _, job := productionSetup(t)
	if _, err := s.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	d := domain.NewDraft("cancelled", []domain.Scene{{ID: domain.ID(), Start: 0, End: 45}})
	d.Goal = "content"
	err := s.CompleteProductionAnalysis(job.ID, []domain.Draft{d}, nil, []domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}}}, false)
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	drafts, _ := s.Drafts(p.ID)
	if len(drafts) != 0 {
		t.Fatal("cancelled outputs published")
	}
	if err := s.Finish(job.ID, "completed", "", false); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(job.ID)
	if task.Status != "cancelled" {
		t.Fatal(task)
	}
}

func TestPartialProductionKeepsSuccessAndCanRetryFailure(t *testing.T) {
	s, p, _, job := productionSetup(t)
	d := domain.NewDraft("content", []domain.Scene{{ID: domain.ID(), Start: 0, End: 45}})
	d.Goal = "content"
	results := []domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}},
		{Goal: "highlight", Status: "failed", Error: "temporary model failure"}}
	if err := s.CompleteProductionAnalysis(job.ID, []domain.Draft{d}, nil, results, true); err != nil {
		t.Fatal(err)
	}
	export, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteExport(export.ID, p.ID, d); err != nil {
		t.Fatal(err)
	}
	flows, _ := s.Workflows(p.ID)
	if flows[0].Status != "partial" {
		t.Fatal(flows)
	}
	if _, err = s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	flows, _ = s.Workflows(p.ID)
	if flows[0].Status != "running" {
		t.Fatal(flows)
	}
	drafts, _ := s.Drafts(p.ID)
	if len(drafts) != 1 {
		t.Fatal(drafts)
	}
}

func TestAnalysisRetryDoesNotInheritPreviousExportFailure(t *testing.T) {
	s, p, wf, job := productionSetup(t)
	content := domain.NewDraft("content", []domain.Scene{{ID: domain.ID(), Start: 0, End: 45}})
	content.Goal = "content"
	if err := s.CompleteProductionAnalysis(job.ID, []domain.Draft{content}, nil,
		[]domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{content.ID}},
			{Goal: "highlight", Status: "failed", Error: "temporary model failure"}}, true); err != nil {
		t.Fatal(err)
	}
	export, err := s.Claim(context.Background())
	if err != nil || export.Kind != "export" || export.WorkflowID != wf.ID {
		t.Fatal(export, err)
	}
	if err := s.Finish(export.ID, "failed", "independent encoder failure", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Claim(context.Background())
	if err != nil || retry.ID != job.ID {
		t.Fatal(retry, err)
	}
	highlight := domain.NewDraft("highlight", []domain.Scene{{ID: domain.ID(), Start: 50, End: 65}})
	highlight.Goal = "highlight"
	if err := s.CompleteProductionAnalysis(job.ID, []domain.Draft{highlight}, nil,
		[]domain.GoalResult{{Goal: "highlight", Status: "completed", DraftIDs: []string{highlight.ID}}}, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task(job.ID)
	if err != nil || got.Status != "completed" || got.Error != "" || got.Retryable {
		t.Fatalf("successful analyze inherited an unrelated export failure: %+v %v", got, err)
	}
	drafts, err := s.Drafts(p.ID)
	if err != nil || len(drafts) != 2 || drafts[0].ID != content.ID || drafts[1].ID != highlight.ID {
		t.Fatalf("successful drafts lost or duplicated: %+v %v", drafts, err)
	}
	flow, err := s.Workflow(wf.ID)
	if err != nil || flow.Status != "partial" || flow.Goals[0].Status != "failed" ||
		flow.Goals[0].Error != "independent encoder failure" || flow.Goals[1].Status != "completed" {
		t.Fatalf("workflow must still expose the independent rendering failure: %+v %v", flow, err)
	}
	tasks, err := s.Tasks(p.ID)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("analysis retry scheduled duplicate exports: %+v %v", tasks, err)
	}
	if _, err := s.Retry(export.ID); err != nil {
		t.Fatal(err)
	}
	exportRetry, err := s.Claim(context.Background())
	if err != nil || exportRetry.ID != export.ID {
		t.Fatal(exportRetry, err)
	}
	if err := s.CompleteExport(export.ID, p.ID, content); err != nil {
		t.Fatal(err)
	}
	flow, err = s.Workflow(wf.ID)
	if err != nil || flow.Status != "completed" {
		t.Fatalf("workflow did not recover after the separate export retry: %+v %v", flow, err)
	}
	after, err := s.Task(job.ID)
	if err != nil || !reflect.DeepEqual(got, after) {
		t.Fatalf("export completion rewrote the analyze result: %+v %v", after, err)
	}
}

func TestLegacyProjectEvidenceUsesValidatedExistingCues(t *testing.T) {
	for _, tc := range []struct {
		name, raw, status string
	}{
		{"available", `[{"start":0,"end":20,"text":"Existing subtitle evidence"}]`, "available"},
		{"empty", `[]`, "empty"},
		{"malformed", `[`, "invalid"},
		{"negative-start", `[{"start":-1,"end":20,"text":"bad"}]`, "invalid"},
		{"reversed", `[{"start":20,"end":10,"text":"bad"}]`, "invalid"},
		{"past-source", `[{"start":0,"end":101,"text":"bad"}]`, "invalid"},
		{"missing-file", "", "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, imp := setup(t)
			// Legacy evidence belongs to an imported project, not a partially
			// published import blocked by the source-ready recovery gate.
			if _, err := s.Claim(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.Finish(imp.ID, "completed", "", false); err != nil {
				t.Fatal(err)
			}
			p, err := s.Project(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			dir, err := s.ProjectDir(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.name != "missing-file" {
				if err := os.WriteFile(filepath.Join(dir, "subtitles.json"), []byte(tc.raw), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetAsset(p.ID, "subtitles", "subtitles.json"); err != nil {
				t.Fatal(err)
			}
			got, err := s.ProjectEvidence(p)
			if err != nil || got.SubtitleStatus != tc.status {
				t.Fatalf("wrong evidence status: %+v %v", got, err)
			}
			if got.SubtitleStatus == "available" && got.SubtitleSource != "existing" {
				t.Fatalf("old evidence provenance was guessed: %+v", got)
			}
			plan, err := s.Plan(p.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"highlight"}
			if !reflect.DeepEqual(plan.Options.Goals, want) || !reflect.DeepEqual(plan.SuggestedGoals, want) ||
				plan.Options.Confirmed || plan.Options.BurnSubtitles {
				t.Fatalf("invalid local recommendation or implicit consent: %+v", plan)
			}
			stored, err := s.Project(p.ID)
			if err != nil || !reflect.DeepEqual(stored, p) {
				t.Fatalf("read-only evidence inspection rewrote legacy project: %+v %v", stored, err)
			}
		})
	}
}
