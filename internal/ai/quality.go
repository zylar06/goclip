package ai

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"autoclip-go/internal/domain"
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func textOK(s string, limit int, required bool) bool {
	return utf8.ValidString(s) && (!required || strings.TrimSpace(s) != "") && utf8.RuneCountInString(s) <= limit
}

func normalizeOptions(opts domain.AnalysisOptions) (domain.AnalysisOptions, error) {
	if opts.Aspect == "" {
		opts.Aspect = "original"
	}
	if len(opts.Goals) == 0 {
		opts.Goals = []string{"content"}
	}
	if opts.Duration < 0 || opts.Duration > 1800 || !oneOf(opts.Aspect, "original", "portrait", "landscape") ||
		!textOK(opts.Instruction, 4000, false) || !validCategory(opts.Category) ||
		!oneOf(opts.Mode, "", "subtitle", "auto", "visual", "fused") || len(opts.Goals) > 3 {
		return opts, invalid("Invalid analysis duration, aspect, category, mode or instruction.")
	}
	seen := map[string]bool{}
	for _, goal := range opts.Goals {
		if !oneOf(goal, "content", "highlight", "promo") || seen[goal] {
			return opts, invalid("Goals must be distinct content, highlight or promo values.")
		}
		seen[goal] = true
	}
	return opts, nil
}

func oneOf(value string, values ...string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}

func normalizeCues(input []domain.Cue) ([]domain.Cue, float64, error) {
	if len(input) == 0 || len(input) > 100000 {
		return nil, 0, invalid("Provide 1–100000 valid subtitle cues; text analysis never falls back to vision.")
	}
	cues := append([]domain.Cue(nil), input...)
	total, duration := 0, 0.0
	for _, cue := range cues {
		total += len(cue.Text)
		if !finite(cue.Start) || !finite(cue.End) || cue.Start < 0 || cue.End <= cue.Start ||
			cue.End > 7200 || !textOK(cue.Text, 8000, true) || total > 8<<20 {
			return nil, 0, invalid("Subtitle cues need finite ordered bounds within 2 hours and nonempty bounded UTF-8 text (8 MiB total).")
		}
		duration = math.Max(duration, cue.End)
	}
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].Start == cues[j].Start {
			return cues[i].End < cues[j].End
		}
		return cues[i].Start < cues[j].Start
	})
	return cues, duration, nil
}

// durationProfile keeps tier guidance and treats subtitle duration as advisory.
type durationProfile struct {
	Min     float64 `json:"min_seconds"`
	Target  float64 `json:"target_seconds"`
	Max     float64 `json:"max_seconds"`
	HardMax float64 `json:"hard_max_seconds,omitempty"`
	MinKeep int     `json:"min_keep"`
	MaxKeep int     `json:"max_keep"`
	// Tier, TopicsLow/High and Guidance port upstream's prompt_hint, which the
	// port previously dropped. The model received only the numeric bounds and no
	// statement that they outrank the prompt body's own figures, so it returned
	// topic counts and clip lengths that then collided with the strict downstream
	// checks. Serialized with the profile into the outline and timeline prompts.
	Tier       string `json:"tier"`
	TopicsLow  int    `json:"topics_low"`
	TopicsHigh int    `json:"topics_high"`
	Guidance   string `json:"guidance"`
}

func profileFor(duration float64, target int) durationProfile {
	p := durationProfile{Min: 20, Target: 60, Max: 150, MinKeep: 2, MaxKeep: 6,
		Tier: "short", TopicsLow: 3, TopicsHigh: 6}
	if duration >= 1800 {
		hours := math.Max(1, duration/3600)
		p = durationProfile{Min: 90, Target: 240, Max: 480, MinKeep: 3, MaxKeep: 32,
			Tier: "long", TopicsLow: int(math.Max(6, 6*hours)), TopicsHigh: int(math.Max(12, 14*hours))}
	} else if duration >= 480 {
		p = durationProfile{Min: 45, Target: 120, Max: 300, MinKeep: 3, MaxKeep: 10,
			Tier: "medium", TopicsLow: 4, TopicsHigh: 10}
	}
	if target > 0 {
		p.Target = float64(target)
		p.Min = math.Min(p.Min, float64(target)/2)
	}
	p.Target = math.Min(duration, p.Target)
	// Preserve upstream tier guidance without mistaking a preferred range or
	// requested target for a semantic cut point.
	p.Max = math.Min(duration, p.Max)
	p.HardMax = math.Min(duration, 1800)
	p.Min = math.Min(p.Min, p.Max/2)
	// Ports prompt_hint's priority assertion: without it the model follows the
	// prompt body's generic figures instead of this source's actual parameters.
	p.Guidance = fmt.Sprintf("These parameters outrank any duration or count written in the instructions above. "+
		"Source is %.0f seconds (%s). Extract %d-%d nonoverlapping topics in total. "+
		"Prefer segments of %.0f-%.0f seconds; target %.0f seconds is advisory. "+
		"Requested duration zero means automatic complete semantics. Exceed the preferred tier only "+
		"when needed for a complete semantic unit; never blindly truncate a valid complete passage. "+
		"The hard maximum is only a safety budget, not a desired length: do not pad toward it. "+
		"Start and end must fall exactly on subtitle-cue "+
		"boundaries: quote a cue's own timestamps rather than computing your own.",
		duration, p.Tier, p.TopicsLow, p.TopicsHigh, p.Min, p.Max, p.Target)
	return p
}

type qualityReport struct {
	Input    int `json:"input"`
	Output   int `json:"output"`
	Snapped  int `json:"snapped"`
	Merged   int `json:"merged"`
	Shifted  int `json:"shifted"`
	Extended int `json:"extended"`
	Trimmed  int `json:"trimmed"`
	Dropped  int `json:"dropped"`
	OverTier int `json:"over_tier"`
}

type timelineResult struct {
	Candidates []domain.Candidate `json:"candidates"`
	Report     qualityReport      `json:"quality"`
}

// timestamp accepts seconds or strict upstream SRT/MM:SS timestamps. Missing
// fields are detected by the containing pointer, rather than defaulting to zero.
type timestamp float64

var timestampPattern = regexp.MustCompile(`^(?:[0-9]{2,}:)?[0-5][0-9]:[0-5][0-9](?:[,.][0-9]{1,3})?$`)

func (t *timestamp) UnmarshalJSON(raw []byte) error {
	var n float64
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return invalid("Invalid timestamp.")
		}
		if !timestampPattern.MatchString(value) {
			return invalid("Use strict MM:SS or HH:MM:SS timestamps with at most three fractional digits.")
		}
		parts := strings.Split(strings.ReplaceAll(value, ",", "."), ":")
		if len(parts) != 2 && len(parts) != 3 {
			return invalid("Use seconds or HH:MM:SS.mmm timestamps.")
		}
		for i, part := range parts {
			if part == "" || strings.ContainsAny(part, "+- eE") {
				return invalid("Invalid timestamp component.")
			}
			v, err := strconv.ParseFloat(part, 64)
			if err != nil || !finite(v) || v < 0 || (i > 0 && v >= 60) ||
				(i < len(parts)-1 && math.Trunc(v) != v) || (len(parts) == 2 && i == 0 && v >= 60) {
				return invalid("Timestamp components overflow or are malformed.")
			}
			n = n*60 + v
		}
	} else if string(raw) == "null" || json.Unmarshal(raw, &n) != nil {
		return invalid("Timestamp must be numeric or a timestamp string.")
	}
	if !finite(n) {
		return invalid("Timestamp must be finite.")
	}
	*t = timestamp(n)
	return nil
}

func excerpt(cues []domain.Cue, start, end float64) string {
	var text strings.Builder
	for _, cue := range cues {
		if cue.Start < end && cue.End > start {
			if text.Len() > 0 {
				text.WriteByte(' ')
			}
			text.WriteString(strings.TrimSpace(cue.Text))
		}
		if text.Len() > 4000 {
			break
		}
	}
	return truncate(text.String(), 1000)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func subtitleCoverage(cues []domain.Cue, start, end float64) float64 {
	covered, cursor := 0.0, start
	for _, cue := range cues {
		s, e := math.Max(start, cue.Start), math.Min(end, cue.End)
		if e > math.Max(s, cursor) {
			covered += e - math.Max(s, cursor)
			cursor = e
		}
	}
	return covered / (end - start)
}

func boundary(cues []domain.Cue, sec float64, start bool) float64 {
	best, distance := sec, math.Inf(1)
	for _, cue := range cues {
		value := cue.End
		if start {
			value = cue.Start
		}
		if d := math.Abs(value - sec); d < distance {
			best, distance = value, d
		}
	}
	if distance <= 3 {
		return best
	}
	for _, cue := range cues {
		if cue.Start <= sec && cue.End >= sec {
			if start {
				return cue.Start
			}
			return cue.End
		}
	}
	// Upstream quality.py _snap_start/_snap_end always land on a real cue: they
	// fall back to the first cue reaching past the requested second, then to the
	// last cue. Returning the raw second instead made cueBoundaries fail and
	// aborted the whole timeline stage whenever a bound landed in a silent gap
	// wider than the 3-second window — a long pause, applause or music. The
	// nearest cue edge is still evidence-grounded; it is never a fabricated time.
	if len(cues) == 0 {
		return sec
	}
	for _, cue := range cues {
		if cue.End >= sec {
			if start {
				return cue.Start
			}
			return cue.End
		}
	}
	if start {
		return cues[len(cues)-1].Start
	}
	return cues[len(cues)-1].End
}

func cueBoundaries(cues []domain.Cue, start, end float64) bool {
	s, e := false, false
	for _, cue := range cues {
		s = s || math.Abs(cue.Start-start) < 1e-6
		e = e || math.Abs(cue.End-end) < 1e-6
	}
	return s && e
}

func refineTimeline(items []domain.Candidate, cues []domain.Cue, p durationProfile) (timelineResult, error) {
	report := qualityReport{Input: len(items)}
	if len(items) == 0 || len(items) > 256 {
		return timelineResult{}, invalid("Timeline must contain 1–256 subtitle-grounded candidates.")
	}
	last := 0.0
	for _, cue := range cues {
		last = math.Max(last, cue.End)
	}
	parsed := append([]domain.Candidate(nil), items...)
	for i := range parsed {
		s := &parsed[i]
		if !finite(s.Start) || !finite(s.End) || s.End <= s.Start || s.End <= cues[0].Start || s.Start >= last {
			return timelineResult{}, invalid("Timeline has reversed or out-of-source bounds.")
		}
		oldStart, oldEnd := s.Start, s.End
		s.Start = boundary(cues, math.Max(cues[0].Start, s.Start), true)
		s.End = boundary(cues, math.Min(last, s.End), false)
		if s.Start != oldStart || s.End != oldEnd {
			report.Snapped++
		}
		if s.End <= s.Start || !cueBoundaries(cues, s.Start, s.End) {
			return timelineResult{}, invalid("Timeline boundaries cannot be grounded in the supplied subtitle cues.")
		}
	}
	sort.SliceStable(parsed, func(i, j int) bool {
		if parsed[i].Start == parsed[j].Start {
			return parsed[i].End < parsed[j].End
		}
		return parsed[i].Start < parsed[j].Start
	})
	var merged []domain.Candidate
	for _, item := range parsed {
		if len(merged) > 0 {
			prev := &merged[len(merged)-1]
			overlap := math.Min(prev.End, item.End) - item.Start
			shorter := math.Min(prev.End-prev.Start, item.End-item.Start)
			if overlap > 0 && overlap/shorter >= .5 {
				prev.End = math.Max(prev.End, item.End)
				report.Merged++
				continue
			}
			if overlap > 0 {
				shift := math.Inf(1)
				for _, cue := range cues {
					if cue.Start >= prev.End && cue.Start < item.End {
						shift = cue.Start
						break
					}
				}
				if math.IsInf(shift, 1) {
					prev.End = math.Max(prev.End, item.End)
					report.Merged++
					continue
				}
				item.Start = shift
				report.Shifted++
			}
		}
		merged = append(merged, item)
	}
	for i := range merged {
		item := &merged[i]
		limit := last
		if i+1 < len(merged) {
			limit = merged[i+1].Start
		}
		oldEnd := item.End
		for _, cue := range cues {
			if item.End-item.Start >= p.Min {
				break
			}
			if cue.End > item.End && cue.Start-item.End <= 5 && cue.End <= limit && cue.End-item.Start <= p.Max {
				item.End = cue.End
			}
		}
		if item.End > oldEnd {
			report.Extended++
		}
		if item.End-item.Start > timelineHardMax(p) {
			return timelineResult{}, invalid("A complete timeline span exceeds the hard safety budget; request shorter complete topics instead of blindly cutting speech.")
		}
	}
	var result []domain.Candidate
	for i := 0; i < len(merged); i++ {
		item := merged[i]
		for item.End-item.Start < p.Min && i+1 < len(merged) {
			next := merged[i+1]
			if next.Start-item.End > 5 || next.End-item.Start > p.Max {
				break
			}
			item.End = next.End
			report.Merged++
			i++
		}
		if item.End-item.Start < p.Min {
			if len(result) > 0 {
				prev := &result[len(result)-1]
				if item.Start-prev.End <= 5 && item.End-prev.Start <= p.Max {
					prev.End = math.Max(prev.End, item.End)
					report.Merged++
					continue
				}
			}
			report.Dropped++
			continue
		}
		if subtitleCoverage(cues, item.Start, item.End) < .5 {
			report.Dropped++
			continue
		}
		result = append(result, item)
	}
	for i := range result {
		result[i].ID = fmt.Sprintf("text-%d", i+1)
		result[i].Evidence = excerpt(cues, result[i].Start, result[i].End)
		result[i].Kind = "text"
		if result[i].End-result[i].Start > p.Max+1e-6 {
			report.OverTier++
		}
	}
	report.Output = len(result)
	if len(result) == 0 {
		return timelineResult{}, invalid("No timeline survived cue-boundary, speech-coverage and duration safeguards; inspect subtitles or adjust duration.")
	}
	return timelineResult{result, report}, nil
}

func validateTimeline(result timelineResult, cues []domain.Cue, p durationProfile) error {
	if len(result.Candidates) == 0 || result.Report.Output != len(result.Candidates) {
		return invalid("Verified timeline is empty or inconsistent.")
	}
	end, overTier := 0.0, 0
	for i, c := range result.Candidates {
		if c.ID != fmt.Sprintf("text-%d", i+1) || !textOK(c.Label, 120, true) || !finite(c.Start) || !finite(c.End) ||
			c.Start < end || c.End-c.Start < p.Min-1e-6 || c.End-c.Start > timelineHardMax(p)+1e-6 ||
			!cueBoundaries(cues, c.Start, c.End) || c.Evidence != excerpt(cues, c.Start, c.End) ||
			subtitleCoverage(cues, c.Start, c.End) < .5 || c.Kind != "text" || c.Score != 0 {
			return invalid("Timeline failed subtitle grounding, duration, identity or nonoverlap validation.")
		}
		end = c.End
		if c.End-c.Start > p.Max+1e-6 {
			overTier++
		}
	}
	if result.Report.OverTier != overTier {
		return invalid("Timeline over-tier quality report is inconsistent.")
	}
	return nil
}

func timelineHardMax(p durationProfile) float64 {
	if p.HardMax > 0 {
		return math.Min(1800, p.HardMax)
	}
	return 1800
}
