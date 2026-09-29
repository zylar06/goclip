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
	if opts.Duration == 0 {
		opts.Duration = 30
	}
	if opts.Aspect == "" {
		opts.Aspect = "original"
	}
	if opts.Language == "" {
		opts.Language = "source"
	}
	if len(opts.Goals) == 0 {
		opts.Goals = []string{"content"}
	}
	if opts.Duration < 1 || opts.Duration > 1800 || !oneOf(opts.Aspect, "original", "portrait", "landscape") ||
		!oneOf(opts.Language, "source", "zh", "en", "ja") || !textOK(opts.Instruction, 4000, false) ||
		!oneOf(opts.Mode, "", "subtitle", "visual") || len(opts.Goals) > 3 {
		return opts, invalid("Invalid analysis duration, aspect, language, mode or instruction.")
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

// durationProfile ports upstream pipeline/quality.py, with the requested
// duration as an additional upper bound and support for genuinely tiny sources.
type durationProfile struct {
	Min     float64 `json:"min_seconds"`
	Target  float64 `json:"target_seconds"`
	Max     float64 `json:"max_seconds"`
	MinKeep int     `json:"min_keep"`
	MaxKeep int     `json:"max_keep"`
}

func profileFor(duration float64, target int) durationProfile {
	p := durationProfile{20, 60, 150, 2, 6}
	if duration >= 1800 {
		p = durationProfile{90, 240, 480, 3, 32}
	} else if duration >= 480 {
		p = durationProfile{45, 120, 300, 3, 10}
	}
	p.Target = math.Min(duration, float64(target))
	p.Max = math.Min(duration, math.Min(p.Max, float64(target)))
	p.Min = math.Min(p.Min, p.Max)
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
	return sec // validation rejects non-cue boundaries, never fabricates evidence.
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
		if item.End-item.Start > p.Max {
			end := item.Start
			for _, cue := range cues {
				if cue.End > end && cue.End <= item.Start+p.Max && cue.End <= item.End {
					end = cue.End
				}
			}
			item.End = end
			report.Trimmed++
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
	end := 0.0
	for i, c := range result.Candidates {
		if c.ID != fmt.Sprintf("text-%d", i+1) || !textOK(c.Label, 120, true) || !finite(c.Start) || !finite(c.End) ||
			c.Start < end || c.End-c.Start < p.Min-1e-6 || c.End-c.Start > p.Max+1e-6 ||
			!cueBoundaries(cues, c.Start, c.End) || c.Evidence != excerpt(cues, c.Start, c.End) ||
			subtitleCoverage(cues, c.Start, c.End) < .5 || c.Kind != "text" || c.Score != 0 {
			return invalid("Timeline failed subtitle grounding, duration, identity or nonoverlap validation.")
		}
		end = c.End
	}
	return nil
}
