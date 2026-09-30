package ai

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"

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

// A tolerated rejection is a durable analysis decision, not an unfinished paid
// request. Only accepted decisions carry provider-derived, validated results.
type refineDecision struct {
	Status  string        `json:"status"`
	EventID string        `json:"event_id"`
	Result  *visualResult `json:"result,omitempty"`
}

// Scope prompt/checkpoint semantics to vision; text checkpoints are unchanged.
// Version 5 omitted skip decisions, so its downstream chain cannot safely replay
// under the new schema. Reject it at the scan checkpoint before another request.
const visualPromptVersion = "general-visual-6"

// Stage names are locally authored: a provider ID must never reach an error
// message or a checkpoint filename (a provider could echo a credential in an
// otherwise valid ID). Exactly one candidate is reviewed, so the rank is fixed.
const (
	refineStage        = "02-visual-refine-01"
	refineStagePrefix  = "02-visual-refine"
	refineSkippedStage = "02-visual-refine-skipped"
	refineAccepted     = "accepted"
	refineSkipped      = "skipped"
)

// Sampled timestamps reach the provider only at the precision of the
// "Source timestamp %.3f seconds" label written by Client.Complete, so a model
// that faithfully echoes a supplied sample returns the rounded value, not the
// exact float. Sampling at i*duration/count almost never lands on a 3-decimal
// value, so an exact comparison rejected correct answers. Accept a match within
// the transmitted precision; an exact match stays exact.
const frameTimeTolerance = 1e-3

// Matches upstream Scene.label default so a segment with no prose is still
// editable rather than a discarded paid run.
const defaultVisualLabel = "片段"

// nearestFrame resolves a model-supplied timestamp to the exact time of the
// closest supplied frame. Resolving to the nearest match keeps the result
// unambiguous without constraining how densely the caller may sample.
func nearestFrame(frames []domain.Frame, tm float64) (float64, bool) {
	best, gap, found := 0., 0., false
	for _, f := range frames {
		d := math.Abs(f.Time - tm)
		if d <= frameTimeTolerance && (!found || d < gap) {
			best, gap, found = f.Time, d, true
		}
	}
	return best, found
}

func noVisualHighlights() *Error {
	return failure(CodeNoHighlights, "No supported highlights were found in the sampled images. For speech-led videos, choose Text / subtitles analysis, or revise the visual instructions. No automatic retry was made.")
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

// validateVisual both checks and canonicalizes: accepted events are clamped
// back into the request window and their supporting timestamps are snapped to
// the exact supplied frame times, so later stages and drafts never inherit a
// provider's rounding.
func validateVisual(value *visualResult, frames []domain.Frame, start, end, maxDuration float64, allowEmpty bool) error {
	if value.Events == nil {
		return invalid("Vision response must contain an events array; it was missing or null.")
	}
	if len(value.Events) > 12 {
		// Upstream select_highlights truncates with raw_events[:12] rather than
		// rejecting. The prompt's "at most 12" is a soft limit models overshoot,
		// and discarding a billed scan over a thirteenth event is the wrong trade.
		value.Events = value.Events[:12]
	}
	if !allowEmpty && len(value.Events) == 0 {
		return noVisualHighlights()
	}
	seen := map[string]bool{}
	for i := range value.Events {
		e := &value.Events[i]
		// Checked in groups rather than one boolean: the combined condition made
		// a real provider failure unattributable, costing a paid run to diagnose.
		if !domain.ValidID(e.ID) || seen[e.ID] {
			return invalid("Visual event IDs must be unique and match the documented identifier format.")
		}
		// Upstream `backend/services/studio/models.py` gives Scene.label a
		// default of '片段' and evidence a default of '', so a model that returns
		// a usable segment without prose still produces a draft the user can
		// edit. The Go port turned both into hard failures, which discarded whole
		// billed analyses over a missing description rather than missing
		// evidence. Length and UTF-8 limits stay strict and are counted in
		// characters like upstream's max_length and domain.Scene.Validate; only
		// emptiness falls back, never oversize.
		if !textOK(e.Label, 120, false) {
			return invalid("Visual event label must be valid UTF-8 text within 120 characters.")
		}
		if !textOK(e.Evidence, 1000, false) {
			return invalid("Visual event evidence must be valid UTF-8 text within 1000 characters.")
		}
		if strings.TrimSpace(e.Label) == "" {
			e.Label = defaultVisualLabel
		}
		// Upstream HighlightCandidate declares watch_score as `int | None` and
		// sorts a missing one last, so an unscored event costs its ranking, not
		// the run. Models also carry over a 0-100 or 0-10 habit despite the
		// prompt, which normalizeScore rescales exactly as the text pipeline does.
		if e.Score == nil {
			zero := 0.0
			e.Score = &zero
		} else if scaled, ok := normalizeScore(*e.Score); ok {
			*e.Score = scaled
		} else {
			return invalid("Visual event score must be a number between 0 and 1.")
		}
		// Upstream defaults event_type to 'unknown' and notes that "Missing
		// annotations remain usable"; only the explicit non-play kinds filter an
		// event out. An absent kind decodes to "" here and used to fail the run.
		if strings.TrimSpace(e.Kind) == "" {
			e.Kind = "unknown"
		}
		if !oneOf(e.Kind, "gameplay", "menu", "reward_screen", "loading", "other", "unknown") {
			return invalid("Visual event kind must be gameplay, menu, reward_screen, loading, other or unknown.")
		}
		if e.Start == nil || e.End == nil || !finite(float64(*e.Start)) || !finite(float64(*e.End)) {
			return invalid("Visual event must carry finite numeric start and end times.")
		}
		if float64(*e.Start) < start-frameTimeTolerance || float64(*e.End) > end+frameTimeTolerance {
			return invalid("Visual event lies outside the analyzed window of the source video.")
		}
		if float64(*e.End-*e.Start) < .1-frameTimeTolerance {
			return invalid("Visual event must last at least 0.1 seconds.")
		}
		if len(e.FrameTimes) == 0 || len(e.FrameTimes) > len(frames) {
			return invalid("Visual event must cite between one and the number of supplied sample timestamps.")
		}
		seen[e.ID] = true
		// A bound rounded just outside the window is tolerated above, so clamp
		// it before it can become a negative or overlong scene bound downstream.
		*e.Start = timestamp(math.Max(start, float64(*e.Start)))
		*e.End = timestamp(math.Min(end, float64(*e.End)))
		if float64(*e.End)-float64(*e.Start) < .1-frameTimeTolerance {
			return invalid("Visual event must last at least 0.1 seconds after clamping to the analyzed window.")
		}
		trimVisualEvent(e, start, end, maxDuration)
		// Deduplicate rather than reject. Two cited timestamps can legitimately
		// snap to the same supplied frame when sampling is coarse (12.4 and 12.6
		// against a single frame at 12.5), and losing a billed scan over that is
		// the wrong trade. An event still has to cite at least one real supplied
		// frame, and a timestamp far from every frame remains a fabrication.
		times := map[float64]bool{}
		snapped := make([]float64, 0, len(e.FrameTimes))
		for _, tm := range e.FrameTimes {
			if !finite(tm) || tm < float64(*e.Start)-frameTimeTolerance || tm > float64(*e.End)+frameTimeTolerance {
				continue
			}
			exact, found := nearestFrame(frames, tm)
			if !found {
				return invalid("Visual event cites a frame that was not supplied to this request.")
			}
			if times[exact] {
				continue
			}
			times[exact] = true
			snapped = append(snapped, exact)
		}
		if len(snapped) == 0 {
			return invalid("Visual event cites no supporting frame inside its own bounds.")
		}
		e.FrameTimes = snapped
	}
	return nil
}

func nonPlay(kind string) bool { return oneOf(kind, "menu", "reward_screen", "loading") }

// trimVisualEvent shortens an event that exceeds the requested clip length,
// keeping it centered on the evidence the model actually cited.
//
// The requested duration is the user's target clip length, not a claim about
// the source, and models routinely propose a slightly longer span. Rejecting
// the response discarded an entire paid analysis over a bound the caller can
// simply trim. The window stays inside the original event and request bounds.
// Widely separated evidence cannot override this cap; supporting timestamps
// outside the retained span are dropped during validation.
func trimVisualEvent(e *visualEvent, start, end, maxDuration float64) {
	if float64(*e.End-*e.Start) <= maxDuration+frameTimeTolerance {
		return
	}
	lo, hi := float64(*e.End), float64(*e.Start)
	for _, tm := range e.FrameTimes {
		lo, hi = math.Min(lo, tm), math.Max(hi, tm)
	}
	if lo > hi { // No usable samples; fall back to the head of the event.
		lo, hi = float64(*e.Start), float64(*e.Start)
	}
	span := maxDuration
	center := (lo + hi) / 2
	if hi-lo > span {
		// A visual target is a hard cap. Keep a real supporting sample rather
		// than widening the clip to encompass widely separated scan frames.
		center = lo
	}
	from := math.Max(math.Max(start, float64(*e.Start)), math.Min(center-span/2, float64(*e.End)-span))
	to := math.Min(math.Min(end, float64(*e.End)), from+span)
	*e.Start, *e.End = timestamp(math.Max(start, from)), timestamp(math.Min(end, to))
}

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
// This compatibility wrapper reuses supplied frames; new integrations should
// use AnalyzeVisualWithSampler for a genuinely fresh dense review.
func AnalyzeVisual(ctx context.Context, client *Client, frames []domain.Frame, duration float64,
	opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc) ([]domain.Draft, []domain.Candidate, error) {
	return AnalyzeVisualWithSampler(ctx, client, frames, duration, opts, checkpointDir, progress, nil)
}

// AnalyzeVisualWithSampler reviews only the best event, acquiring fresh images
// from its +/-2s window. The caller owns media access and paid-analysis consent.
// A nil sampler preserves the legacy supplied-frame-only review.
func AnalyzeVisualWithSampler(ctx context.Context, client *Client, frames []domain.Frame, duration float64,
	opts domain.AnalysisOptions, checkpointDir string, progress domain.ProgressFunc,
	sample func(context.Context, []float64) ([]domain.Frame, error)) ([]domain.Draft, []domain.Candidate, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return nil, nil, err
	}
	if !opts.AllowVisual || !opts.Confirmed || opts.Mode == "subtitle" {
		return nil, nil, invalid("Visual analysis requires explicit visual opt-in and paid-analysis confirmation from orchestration.")
	}
	target := float64(opts.Duration)
	if target == 0 {
		target = 30
	}
	if err := client.ready(ctx); err != nil {
		return nil, nil, err
	}
	frames, hashes, err := prepareFrames(ctx, frames, duration)
	if err != nil {
		return nil, nil, err
	}
	r, err := newRunner(ctx, client, struct {
		PromptVersion string                 `json:"prompt_version"`
		Frames        []frameFingerprint     `json:"frames"`
		Duration      float64                `json:"duration"`
		Options       domain.AnalysisOptions `json:"options"`
		DenseReview   bool                   `json:"dense_review"`
	}{visualPromptVersion, hashes, duration, opts, sample != nil}, checkpointDir, progress)
	if err != nil {
		return nil, nil, err
	}
	scanFrames := sampleFrames(frames, 60)
	scan, err := stage(ctx, r, "01-visual-events", 0, 40, func() (visualResult, error) {
		return ask[visualResult](ctx, client, opts.Category, "visual", map[string]any{"duration": duration, "options": opts}, scanFrames)
	}, func(value *visualResult) error {
		if err := validateVisual(value, scanFrames, 0, duration, target, false); err != nil {
			return err
		}
		if len(playable(*value)) == 0 {
			return noVisualHighlights()
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	best := playable(scan)
	// All scan candidates remain available for audit; replace only reviewed IDs.
	candidates := visualCandidates(scan)
	// Upstream reviews exactly one candidate (`best = events[0]` in
	// backend/services/studio/intelligence.py): a single dense second pass.
	// The port ran that paid review for up to six events, multiplying both cost
	// and failure probability by six for a stage that only sharpens boundaries.
	// The other playable candidates keep their scanned (already trimmed and
	// clamped) bounds and still become drafts.
	top := best[0]
	start := math.Max(0, float64(*top.Start)-2)
	end := math.Min(duration, float64(*top.End)+2)
	var local []domain.Frame
	for _, f := range frames {
		if f.Time >= start && f.Time <= end {
			local = append(local, f)
		}
	}
	local = sampleFrames(local, 25)
	if sample != nil {
		if err := contextError(ctx); err != nil {
			return nil, nil, err
		}
		times := reviewTimes(start, end, duration)
		local, err = sample(ctx, times)
		if err != nil {
			return nil, nil, atStage(refineStage, err)
		}
		var fingerprints []frameFingerprint
		local, fingerprints, err = prepareFrames(ctx, local, duration)
		if err != nil {
			return nil, nil, atStage(refineStage, err)
		}
		if len(local) != len(times) {
			return nil, nil, atStage(refineStage, invalid("Dense sampler must return exactly the requested local frames."))
		}
		for i, f := range local {
			if math.Abs(f.Time-times[i]) > frameTimeTolerance || f.Time < start-frameTimeTolerance || f.Time > end+frameTimeTolerance {
				return nil, nil, atStage(refineStage, invalid("Dense sampler returned an unrequested frame timestamp."))
			}
		}
		data, err := json.Marshal(fingerprints)
		if err != nil {
			return nil, nil, atStage(refineStage, invalid("Cannot fingerprint dense review frames."))
		}
		// Bind all downstream checkpoints to actual newly sampled image bytes,
		// not just their times. Re-sampling on replay incurs no model request.
		r.previous = digest([]byte(r.previous + ":dense:" + digest(data)))
	}
	rest := best[1:]
	validateRefinement := func(value *visualResult) error {
		if err := validateVisual(value, local, start, end, target, false); err != nil {
			return err
		}
		if len(value.Events) != 1 || value.Events[0].ID != top.ID {
			return invalid("Boundary refinement must preserve exactly the reviewed event identity.")
		}
		return nil
	}
	decision, err := stage(ctx, r, refineStage, 40, 80, func() (refineDecision, error) {
		value, err := ask[visualResult](ctx, client, opts.Category, "refine", map[string]any{
			"event": top, "window_start": start, "window_end": end, "options": opts,
		}, local)
		if err == nil {
			err = validateRefinement(&value)
		}
		if err != nil {
			err = markRefineRejection(err)
			// Only a fresh response rejected by the refine boundary may become a
			// skip, and only if a playable fallback exists. Never persist the bad
			// response/error text or turn auth/cancel/no-highlights into success.
			if rejectedRefinement(err) && len(rest) > 0 {
				return refineDecision{Status: refineSkipped, EventID: top.ID}, nil
			}
			return refineDecision{}, err
		}
		return refineDecision{Status: refineAccepted, EventID: top.ID, Result: &value}, nil
	}, func(value *refineDecision) error {
		if value.EventID != top.ID {
			return invalid("Refinement decision must identify the reviewed event.")
		}
		switch value.Status {
		case refineSkipped:
			if value.Result != nil || len(rest) == 0 {
				return invalid("Skipped refinement must have no result and a playable fallback.")
			}
		case refineAccepted:
			if value.Result == nil {
				return invalid("Accepted refinement must contain a validated result.")
			}
			return validateRefinement(value.Result)
		default:
			return invalid("Refinement decision must be accepted or skipped.")
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	// Both decisions advance the checkpoint chain before any downstream call or
	// skip notification. Replay preserves the same candidate set and never asks
	// the provider to reconsider a skip accepted by an earlier attempt.
	selected := make([]domain.Candidate, 0, len(best))
	if decision.Status == refineSkipped {
		if err := r.notify(ctx, refineSkippedStage, 80); err != nil {
			return nil, nil, err
		}
	} else {
		c := visualCandidates(*decision.Result)[0]
		for j := range candidates {
			if candidates[j].ID == c.ID {
				candidates[j] = c
			}
		}
		// A review may reclassify the strongest candidate as non-play content.
		if !nonPlay(c.Kind) {
			selected = append(selected, c)
		}
	}
	// The remaining playable scan candidates keep their scanned bounds, already
	// trimmed and clamped by validateVisual, and still become drafts.
	selected = append(selected, visualCandidates(visualResult{Events: rest})...)
	if len(selected) == 0 {
		return nil, nil, atStage(refineStagePrefix, noVisualHighlights())
	}
	// Independent visual events are never merged merely because they are adjacent.
	titles, err := stage(ctx, r, "03-visual-titles", 80, 95, func() ([]titleItem, error) {
		items, err := ask[[]titleItem](ctx, client, opts.Category, "titles", map[string]any{"candidates": selected, "options": opts}, nil)
		if err != nil {
			return nil, err
		}
		// Upstream needs no title call here at all: it uses the scan's own label.
		// Back-filling from that label keeps a skipped title from discarding the
		// whole billed scan and review.
		return alignTitles(items, selected), nil
	}, func(value *[]titleItem) error { return validateTitles(*value, selected) })
	if err != nil {
		return nil, nil, err
	}
	// Upstream assemble_sequences adds bounded run-up and tail to every event
	// before drafting, so a clip does not open mid-action or cut on the last
	// frame of evidence. Applied after the scan and review have verified bounds,
	// and before draftPlans, which is upstream's order.
	selected = addContext(selected, target, duration)
	plans := draftPlans(selected, titles, "visual")
	drafts, err := stage(ctx, r, "04-visual-drafts", 95, 100, func() ([]domain.Draft, error) {
		return makeDrafts(plans, opts, false), nil
	}, func(value *[]domain.Draft) error { return validateDrafts(*value, plans, opts, duration, false) })
	if err != nil {
		return nil, nil, err
	}
	return drafts, candidates, nil
}

func reviewTimes(start, end, duration float64) []float64 {
	step := math.Max(1, (end-start)/24)
	var times []float64
	for i := 0; i < 25; i++ {
		tm := math.Round((start+float64(i)*step)*1000) / 1000
		if tm > end+frameTimeTolerance || tm >= duration {
			break
		}
		times = append(times, tm)
	}
	return times
}

// addContext ports upstream assemble_sequences: each event gains a short run-up
// and tail so the clip does not open mid-action or cut on the final frame of
// evidence. Independent events keep their own identity and are never merged by
// proximity. The lead is at most 1.5 seconds and only uses slack the requested
// duration actually leaves, the tail is at most 2 seconds, and the total stays
// within both the requested duration and the source.
func addContext(candidates []domain.Candidate, target, sourceDuration float64) []domain.Candidate {
	if len(candidates) == 0 || !finite(sourceDuration) || sourceDuration <= 0 {
		return candidates
	}
	out := make([]domain.Candidate, 0, len(candidates))
	for _, c := range candidates {
		lead := math.Max(0, math.Min(1.5, (target-(c.End-c.Start))/2))
		start := math.Max(0, c.Start-lead)
		end := math.Min(sourceDuration, math.Min(c.End+2, start+target))
		// Never return a shorter or inverted span than the verified one.
		if end > start && end-start >= c.End-c.Start {
			c.Start, c.End = start, end
		}
		out = append(out, c)
	}
	return out
}
