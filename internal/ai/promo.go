package ai

import (
	"context"
	"math"
	"sort"
	"strings"

	"autoclip-go/internal/domain"
)

const promoPromptVersion = "initial-promo-2"

type promoWire struct {
	CandidateID string `json:"candidate_id"`
	Title       string `json:"title"`
	Hook        string `json:"hook"`
}

// MakePromos creates at most three initial single-candidate promo drafts.
// This is not a draft rewriting API: it accepts only verified candidates and
// preserves their scenes exactly. Goal routing and consent belong to the worker.
func MakePromos(ctx context.Context, client *Client, candidates []domain.Candidate,
	opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	if !opts.Confirmed || len(candidates) == 0 || len(candidates) > 6 {
		return nil, invalid("Initial promos require confirmed analysis and 1-6 verified candidates.")
	}
	seen := map[string]bool{}
	var selected []domain.Candidate
	duration := 0.0
	for _, c := range candidates {
		if !domain.ValidID(c.ID) || seen[c.ID] || !finite(c.Start) || !finite(c.End) ||
			c.Start < 0 || c.End-c.Start < .1 || c.End-c.Start > 1800 || c.End > 7200 ||
			!finite(c.Score) || c.Score < 0 || c.Score > 1 ||
			!textOK(c.Label, 120, false) || !textOK(c.Evidence, 1000, false) {
			return nil, invalid("Promo input must contain unique, bounded verified candidate scenes.")
		}
		seen[c.ID] = true
		duration = math.Max(duration, c.End)
		if !nonPlay(c.Kind) {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		return nil, noVisualHighlights()
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].Score > selected[j].Score })
	r, err := newRunner(ctx, client, struct {
		Version    string                 `json:"version"`
		Candidates []domain.Candidate     `json:"candidates"`
		Options    domain.AnalysisOptions `json:"options"`
	}{promoPromptVersion, candidates, opts}, checkpointDir, progress)
	if err != nil {
		return nil, err
	}
	// Only verified label/evidence is material for the model. IDs are opaque
	// mapping keys, not prose evidence; bounds are never model-editable.
	type evidence struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Evidence string `json:"evidence"`
	}
	var material []evidence
	for _, c := range selected {
		material = append(material, evidence{c.ID, c.Label, c.Evidence})
	}
	variants, err := stage(ctx, r, "01-promo-titles", 0, 90, func() ([]promoWire, error) {
		return ask[[]promoWire](ctx, client, "", "promo", map[string]any{
			"candidates": material, "preferences": opts.Instruction,
		}, nil)
	}, func(items *[]promoWire) error {
		// No model-editing or repair request on malformed output.
		return validatePromos(*items, selected)
	})
	if err != nil {
		return nil, err
	}
	byID := candidateMap(selected)
	var plans []draftPlan
	for _, v := range variants {
		plans = append(plans, draftPlan{v.Title, v.Hook, "promo", []domain.Scene{byID[v.CandidateID].Scene}})
	}
	// Visual evidence cannot enable subtitle burning.
	subtitles := opts.BurnSubtitles && opts.Mode != "visual"
	return stage(ctx, r, "02-promo-drafts", 90, 100, func() ([]domain.Draft, error) {
		return makeDrafts(plans, opts, subtitles), nil
	}, func(items *[]domain.Draft) error {
		return validateDrafts(*items, plans, opts, duration, subtitles)
	})
}

func validatePromos(items []promoWire, candidates []domain.Candidate) error {
	if len(items) < 1 || len(items) > 3 {
		return invalid("Initial promos must contain one to three distinct opening plans.")
	}
	byID := candidateMap(candidates)
	openings := map[string]bool{}
	for _, item := range items {
		if _, ok := byID[item.CandidateID]; !ok || !textOK(item.Title, 200, true) || !textOK(item.Hook, 120, false) {
			return invalid("Promo plans must reference supplied candidates with bounded titles and hooks.")
		}
		// An opening is the hook, or the title when no hook is supplied. Changing
		// an ID/title while copying the same hook is not another opening.
		opening := item.Hook
		if strings.TrimSpace(opening) == "" {
			opening = item.Title
		}
		opening = strings.ToLower(strings.Join(strings.Fields(opening), " "))
		if openings[opening] {
			return invalid("Promo plans repeated an opening; distinct initial openings are required.")
		}
		openings[opening] = true
	}
	return nil
}
