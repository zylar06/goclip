package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
)

func TestNoHighlightsDoesNotOfferBlindRetry(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	project := domain.Project{ID: domain.ID(), Name: "no highlights"}
	if _, err := s.CreateProject(project, domain.ImportPayload{}); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Execute: func(context.Context, domain.Task, domain.ProgressFunc) error {
		return &ai.Error{Code: "no_highlights", Message: "No supported highlights; consider subtitles.", Stage: "01-visual-events"}
	}}
	if err := w.runTask(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" || got.Retryable || got.Error == "" {
		t.Fatalf("no-highlight result must remain explicit without a blind retry: %+v", got)
	}
}

func TestAnalysisDoesNotPublishArtificialPercentages(t *testing.T) {
	n := 0
	fn := analysisProgress(func(stage string, percent *float64) error {
		n++
		if stage != "03-scoring" || percent != nil {
			t.Fatal("LLM must remain indeterminate", stage, percent)
		}
		return nil
	})
	for _, v := range []float64{0, 50, 100} {
		if e := fn("03-scoring", &v); e != nil {
			t.Fatal(e)
		}
	}
	if n != 3 {
		t.Fatal("stage notifications lost")
	}
}

func TestWorkerFailureAndCancellation(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancel"}[cancelTask], func(t *testing.T) {
			s, e := store.Open(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p := domain.Project{ID: domain.ID(), Name: "test"}
			job, e := s.CreateProject(p, domain.ImportPayload{})
			if e != nil {
				t.Fatal(e)
			}
			job, e = s.Claim(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			w := Worker{Store: s, TaskTimeout: 5 * time.Second, Execute: func(ctx context.Context, t domain.Task, p domain.ProgressFunc) error {
				if cancelTask {
					if _, e := s.Cancel(t.ID); e != nil {
						return e
					}
					<-ctx.Done()
					return ctx.Err()
				}
				return errors.New("native tool crashed")
			}}
			if e = w.runTask(context.Background(), job); e != nil {
				t.Fatal(e)
			}
			got, e := s.Task(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			want := "failed"
			if cancelTask {
				want = "cancelled"
			}
			if got.Status != want || !got.Retryable {
				t.Fatal(got)
			}
		})
	}
}
