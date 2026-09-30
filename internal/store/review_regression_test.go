package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

func TestWorkerLeaseFencesEveryPublicationAfterRecovery(t *testing.T) {
	s, p, _, old := productionSetup(t)
	stale := s.ForTask(old)
	if n, err := s.Recover(time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.Retry(old.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.Claim(context.Background())
	if err != nil || current.LeaseID == "" || current.LeaseID == old.LeaseID {
		t.Fatal("claim did not replace lease", current, err)
	}
	d := domain.NewDraft("stale result", []domain.Scene{{ID: domain.ID(), Start: 0, End: 40}})
	checks := map[string]func() error{
		"heartbeat":  func() error { return stale.Heartbeat(old.ID) },
		"progress":   func() error { return stale.Progress(old.ID, "stale", nil) },
		"finish":     func() error { return stale.Finish(old.ID, "completed", "", false) },
		"metadata":   func() error { return stale.UpdateProject(p) },
		"asset":      func() error { return stale.SetAsset(p.ID, "source", "stale.mp4") },
		"transcript": func() error { return stale.CompleteTranscript(p.ID, "subtitles-asr.json", "asr", 1) },
		"analysis":   func() error { return stale.CompleteAnalysis(old.ID, p.ID, []domain.Draft{d}, nil) },
		"production": func() error {
			return stale.CompleteProductionAnalysis(old.ID, []domain.Draft{d}, nil,
				[]domain.GoalResult{{Goal: "content", Status: "completed", DraftIDs: []string{d.ID}}}, false)
		},
		"export":     func() error { return stale.CompleteExport(old.ID, p.ID, d) },
		"preview":    func() error { return stale.CompletePreview(old.ID, p.ID, "preview.mp4") },
		"inspection": func() error { return stale.CompleteInspection(old.ID, p) },
		"import":     func() error { return stale.CompleteImport(old.ID, p, "source.mp4", "subtitles.json") },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrConflict) {
				t.Fatal("old lease published", err)
			}
		})
	}
	if err := s.ForTask(current).Progress(current.ID, "new-attempt", nil); err != nil {
		t.Fatal("current owner cannot progress", err)
	}
	got, err := s.Task(current.ID)
	if err != nil || got.Status != "running" || got.Stage != "new-attempt" {
		t.Fatal(got, err)
	}
	drafts, err := s.Drafts(p.ID)
	if err != nil || len(drafts) != 0 {
		t.Fatal("stale drafts escaped", drafts, err)
	}
}

func TestIndependentStoresClaimWithoutBusySnapshot(t *testing.T) {
	s, p, _ := setup(t)
	other, err := Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for attempt := 0; attempt < 15; attempt++ {
		if attempt > 0 {
			if _, err := s.Queue(p.ID, "inspect", nil); err != nil {
				t.Fatal(err)
			}
		}
		type outcome struct {
			task domain.Task
			err  error
		}
		results := make(chan outcome, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, db := range []*Store{s, other} {
			wg.Add(1)
			go func(db *Store) {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				task, err := db.Claim(ctx)
				results <- outcome{task, err}
			}(db)
		}
		close(start)
		wg.Wait()
		close(results)
		claimed := 0
		var owner domain.Task
		for result := range results {
			if result.err == nil {
				owner = result.task
				claimed++
			} else if !errors.Is(result.err, ErrNotFound) {
				t.Fatal("losing worker got fatal database contention", result.err)
			}
		}
		if claimed != 1 {
			t.Fatal("claim count", claimed)
		}
		if err := s.ForTask(owner).Finish(owner.ID, "completed", "", false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestImportPublicationRollsBackAndBlocksPrematureProduction(t *testing.T) {
	s, p, _ := setup(t)
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_subtitles BEFORE INSERT ON assets WHEN NEW.kind='subtitles' BEGIN SELECT RAISE(ABORT,'injected publication failure'); END;`); err != nil {
		t.Fatal(err)
	}
	p.SubtitleStatus = "available"
	plan := domain.LocalPlan(p, 1)
	p.Plan = &plan
	if err = s.ForTask(job).CompleteImport(job.ID, p, "source.mp4", "subtitles.json"); err == nil {
		t.Fatal("fault injection did not fire")
	}
	if _, err = s.Asset(p.ID, "source"); !errors.Is(err, ErrNotFound) {
		t.Fatal("source reference escaped transaction", err)
	}
	got, err := s.Project(p.ID)
	if err != nil || got.Plan != nil || got.SubtitleStatus != "" {
		t.Fatal("metadata escaped transaction", got, err)
	}
	if err = s.Finish(job.ID, "interrupted", "injected crash", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Plan(p.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete import exposed production plan", err)
	}
	d := domain.NewDraft("manual", []domain.Scene{{ID: domain.ID(), Start: 0, End: 10}})
	d, err = s.SaveDraft(p.ID, d, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueExport(p.ID, d.ID, d.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("incomplete import allowed export", err)
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_subtitles"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Retry(job.ID); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ForTask(job).CompleteImport(job.ID, p, "source.mp4", "subtitles.json"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Plan(p.ID); err != nil {
		t.Fatal("completed import not ready", err)
	}
}

func TestWorkflowCandidateMergeKeepsOtherModeNotPreviousWorkflow(t *testing.T) {
	s, p, flow, job := productionSetup(t)
	tx, err := s.begin()
	if err != nil {
		t.Fatal(err)
	}
	subtitle := domain.Candidate{Scene: domain.Scene{ID: "subtitle-one", Start: 0, End: 40}}
	visual := domain.Candidate{Scene: domain.Scene{ID: "visual-one", Start: 50, End: 60}}
	if _, err = mergeWorkflowCandidatesTx(tx, flow.ID, []domain.Candidate{subtitle}); err != nil {
		t.Fatal(err)
	}
	got, err := mergeWorkflowCandidatesTx(tx, flow.ID, []domain.Candidate{visual})
	if err != nil || len(got) != 2 {
		t.Fatal("retry erased successful route", got, err)
	}
	got, err = mergeWorkflowCandidatesTx(tx, flow.ID, []domain.Candidate{{Scene: domain.Scene{ID: "visual-two", Start: 60, End: 70}}})
	if err != nil || len(got) != 2 || got[0].ID != "visual-two" || got[1].ID != "subtitle-one" {
		t.Fatal("mode replacement broken", got, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(job.ID, "failed", "new plan requested", false); err != nil {
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
	next, err := s.ConfirmProduction(p.ID, plan.Revision)
	if err != nil {
		t.Fatal(err)
	}
	tx, err = s.begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err = mergeWorkflowCandidatesTx(tx, next.ID, []domain.Candidate{visual})
	if err != nil || len(got) != 1 {
		t.Fatal("different workflow inherited stale evidence", got, err)
	}
}
