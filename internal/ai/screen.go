package ai

import (
	"context"

	"autoclip-go/internal/domain"
)

// Screening is advisory only; it never changes mode or grants paid consent.
type Screening struct {
	Mode   string   `json:"mode"`
	Goals  []string `json:"goals"`
	Reason string   `json:"reason"`
}

// Screen makes one logical screening request with at most four static images.
// The caller must obtain consent before invoking it; no speech is analyzed.
func Screen(ctx context.Context, client *Client, frames []domain.Frame, duration float64) (Screening, error) {
	if err := client.ready(ctx); err != nil {
		return Screening{}, err
	}
	frames, _, err := prepareFrames(ctx, frames, duration)
	if err != nil {
		return Screening{}, err
	}
	value, err := ask[Screening](ctx, client, "", "screen", map[string]any{"duration": duration}, sampleFrames(frames, 4))
	if err != nil {
		return Screening{}, atStage("screen", err)
	}
	if !oneOf(value.Mode, "subtitle", "visual") || len(value.Goals) == 0 || len(value.Goals) > 3 || !textOK(value.Reason, 800, true) {
		return Screening{}, atStage("screen", invalid("Screening requires a valid mode, goals and bounded visual-only reason."))
	}
	if _, err := normalizeOptions(domain.AnalysisOptions{Mode: value.Mode, Goals: value.Goals}); err != nil {
		return Screening{}, atStage("screen", err)
	}
	value.Reason = "Static-image recommendation only; speech/audio was not analyzed. " + value.Reason
	return value, nil
}
