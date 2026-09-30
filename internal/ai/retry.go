package ai

import (
	"context"
	"errors"
	"log"
	"time"

	"autoclip-go/internal/domain"
)

// Retries live ONLY at the analysis request boundary. Complete and smoke tests
// remain single-attempt; stage/chunk/worker retries must not wrap this loop.
// Structural errors are decoded after this loop and can never trigger it.
func completeAnalysis(ctx context.Context, client *Client, prompt string, frames []domain.Frame) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := client.Complete(ctx, prompt, frames)
		if err == nil {
			return raw, nil
		}
		if contextError(ctx) != nil {
			return "", contextError(ctx)
		}
		var e *Error
		retry := errors.As(err, &e) && e.Code != CodeAuth && e.Code != CodeModel &&
			(e.transientNetwork || e.HTTPStatus == 429 || (e.HTTPStatus >= 500 && e.HTTPStatus <= 599))
		if !retry || attempt == 2 {
			return "", err
		}
		log.Printf("ai: transient analysis failure; retry attempt %d/3 (code=%s status=%d)", attempt+2, e.Code, e.HTTPStatus)
		timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", contextError(ctx)
		case <-timer.C:
		}
	}
	return "", invalid("Analysis request attempt budget exhausted.")
}
