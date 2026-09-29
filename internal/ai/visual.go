package ai

import (
	"context"
	"fmt"
	"math"
	"sort"

	"autoclip-go/internal/domain"
)

type visualEvent struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Start      *timestamp `json:"start"`
	End        *timestamp `json:"end"`
	Evidence   string     `json:"evidence"`
	Kind       string     `json:"kind"`
	Score      *float64   `json:"score"`
	FrameTimes []float64  `json:"frame_times"`
}

type visualResult struct {
	Events []visualEvent `json:"events"`
}

type frameFingerprint struct {
	Time float64 `json:"time"`
	Hash string  `json:"hash"`
}

func sampleFrames(frames []domain.Frame, limit int) []domain.Frame {
	if len(frames) <= limit {
		return append([]domain.Frame(nil), frames...)
	}
	sampled := make([]domain.Frame, limit)
	for i := range sampled {
		sampled[i] = frames[i*(len(frames)-1)/(limit-1)]
	}
	return sampled
}

func prepareFrames(ctx context.Context, input []domain.Frame, duration float64) ([]domain.Frame, []frameFingerprint, error) {
	if !finite(duration) || duration < .1 || duration > 7200 || len(input) == 0 || len(input) > 600 {
		return nil, nil, invalid("Visual analysis requires 1–600 local frames and a finite source duration within 2 hours.")
	}
	frames := append([]domain.Frame(nil), input...)
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
	var fingerprints []frameFingerprint
	total := 0
	for i, f := range frames {
		if err := contextError(ctx); err != nil {
			return nil, nil, err
		}
		if !finite(f.Time) || f.Time < 0 || f.Time >= duration || (i > 0 && f.Time == frames[i-1].Time) {
			return nil, nil, invalid("Frame timestamps must be distinct and inside the source duration.")
		}
		data, _, err := readImage(f.Path)
		if err != nil {
			return nil, nil, err
		}
		total += len(data)
		if total > 64<<20 {
			return nil, nil, invalid("Visual input exceeds the 64 MiB local image budget.")
		}
		fingerprints = append(fingerprints, frameFingerprint{f.Time, digest(data)})
	}
	return frames, fingerprints, nil
}

func validateVisual(value visualResult, frames []domain.Frame, start, end, maxDuration float64, allowEmpty bool) error {
	if value.Events == nil || len(value.Events) > 12 || (!allowEmpty && len(value.Events) == 0) {
		return invalid("Vision returned no supported events or an invalid events array.")
	}
	seen := map[string]bool{}
	for _, e := range value.Events {
		if !domain.ValidID(e.ID) || seen[e.ID] || !textOK(e.Label, 120, true) || !textOK(e.Evidence, 1000, true) ||
			e.Start == nil || e.End == nil || e.Score == nil || !finite(*e.Score) || *e.Score < 0 || *e.Score > 1 ||
			!finite(float64(*e.Start)) || !finite(float64(*e.End)) ||
			float64(*e.Start) < start || float64(*e.End) > end ||
			float64(*e.End-*e.Start) < .1 || float64(*e.End-*e.Start) > maxDuration+1e-6 ||
			!oneOf(e.Kind, "gameplay", "menu", "reward_screen", "loading", "other", "unknown") ||
			len(e.FrameTimes) == 0 || len(e.FrameTimes) > len(frames) {
			return invalid("Visual event has invalid identity, evidence, score, kind or source/window/duration bounds.")
		}
		seen[e.ID] = true
		times := map[float64]bool{}
		for _, tm := range e.FrameTimes {
			if !finite(tm) || tm < float64(*e.Start) || tm > float64(*e.End) || times[tm] {
				return invalid("Visual event has invalid or duplicate supporting frame timestamps.")
			}
			found := false
			for _, f := range frames {
				if math.Abs(f.Time-tm) < 1e-6 {
					found = true
					break
				}
			}
			if !found {
				return invalid("Visual event cites a frame that was not supplied to this request.")
			}
			times[tm] = true
		}
	}
	return nil
}

func nonPlay(kind string) bool { return oneOf(kind, "menu", "reward_screen", "loading") }

func visualCandidates(value visualResult) []domain.Candidate {
	var out []domain.Candidate
	for _, e := range value.Events {
		out = append(out, domain.Candidate{
			Scene: domain.Scene{ID: e.ID, Label: e.Label, Start: float64(*e.Start), End: float64(*e.End), Evidence: e.Evidence},
			Score: *e.Score, Kind: e.Kind,
		})
	}
	return out
}

func playable(value visualResult) []visualEvent {
	var out []visualEvent
	for _, e := range value.Events {
		if !nonPlay(e.Kind) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return *out[i].Score > *out[j].Score })
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

// AnalyzeVisual never samples video or enables vision implicitly. The caller
// owns consent and sampling; both AllowVisual and Confirmed must be true here.
// Refinement reuses supplied local frames (up to 25 per event); denser review
// requires the media layer to supply denser frames in the first place.
func AnalyzeVisual(ctx context.Context, client *Client, frames []domain.Frame, duration float64,
	opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, []domain.Candidate, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, nil, err
	}
	if !opts.AllowVisual || !opts.Confirmed || opts.Mode == "subtitle" {
		return nil, nil, invalid("Visual analysis requires explicit visual opt-in and paid-analysis confirmation from orchestration.")
	}
	if err := client.ready(ctx); err != nil {
		return nil, nil, err
	}
	frames, hashes, err := prepareFrames(ctx, frames, duration)
	if err != nil {
		return nil, nil, err
	}
	r, err := newRunner(ctx, client, struct {
		Frames   []frameFingerprint     `json:"frames"`
		Duration float64                `json:"duration"`
		Options  domain.AnalysisOptions `json:"options"`
	}{hashes, duration, opts}, checkpointDir, progress)
	if err != nil {
		return nil, nil, err
	}
	scanFrames := sampleFrames(frames, 60)
	scan, err := stage(ctx, r, "01-visual-events", 0, 40, func() (visualResult, error) {
		return ask[visualResult](ctx, client, "visual", map[string]any{"duration": duration, "options": opts}, scanFrames)
	}, func(value visualResult) error {
		if err := validateVisual(value, scanFrames, 0, duration, float64(opts.Duration), false); err != nil {
			return err
		}
		if len(playable(value)) == 0 {
			return invalid("No usable visual event: only menus, reward screens or loading frames were found.")
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	best := playable(scan)
	// All scan candidates remain available for audit; replace only reviewed IDs.
	candidates := visualCandidates(scan)
	var selected []domain.Candidate
	for i, event := range best {
		start := math.Max(0, float64(*event.Start)-2)
		end := math.Min(duration, float64(*event.End)+2)
		var local []domain.Frame
		for _, f := range frames {
			if f.Time >= start && f.Time <= end {
				local = append(local, f)
			}
		}
		local = sampleFrames(local, 25)
		// Stage names are locally authored: provider IDs must never enter errors
		// or filesystem paths (a provider could echo a credential in a valid ID).
		name := fmt.Sprintf("02-visual-refine-%02d", i+1)
		refined, err := stage(ctx, r, name, 40+float64(i)*40/float64(len(best)), 40+float64(i+1)*40/float64(len(best)), func() (visualResult, error) {
			return ask[visualResult](ctx, client, "refine", map[string]any{
				"event": event, "window_start": start, "window_end": end, "options": opts,
			}, local)
		}, func(value visualResult) error {
			if err := validateVisual(value, local, start, end, float64(opts.Duration), false); err != nil {
				return err
			}
			if len(value.Events) != 1 || value.Events[0].ID != event.ID {
				return invalid("Boundary refinement must preserve exactly the reviewed event identity.")
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		c := visualCandidates(refined)[0]
		for j := range candidates {
			if candidates[j].ID == c.ID {
				candidates[j] = c
			}
		}
		if !nonPlay(c.Kind) {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		return nil, nil, atStage("02-visual-refine", invalid("No usable event survived visual boundary review."))
	}
	// Independent visual events are never merged merely because they are adjacent.
	titles, err := stage(ctx, r, "03-visual-titles", 80, 95, func() ([]titleItem, error) {
		return ask[[]titleItem](ctx, client, "titles", map[string]any{"candidates": selected, "options": opts}, nil)
	}, func(value []titleItem) error { return validateTitles(value, selected) })
	if err != nil {
		return nil, nil, err
	}
	plans := draftPlans(selected, titles, nil, "visual")
	drafts, err := stage(ctx, r, "04-visual-drafts", 95, 100, func() ([]domain.Draft, error) {
		return makeDrafts(plans, opts, false), nil
	}, func(value []domain.Draft) error { return validateDrafts(value, plans, opts, duration, false) })
	if err != nil {
		return nil, nil, err
	}
	return drafts, candidates, nil
}
