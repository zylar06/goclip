package media

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"autoclip-go/internal/domain"
)

const subtitleLimit = 8 << 20

var timestampRE = regexp.MustCompile(`^(\d{1,3}):([0-5]\d):([0-5]\d)[,.](\d{3})$`)

func parseTime(s string) (float64, error) {
	m := timestampRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, fmt.Errorf("invalid SRT timestamp %q", s)
	}
	var v [4]int
	for i := range v {
		// All four captures are bounded decimal strings.
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return float64(v[0]*3600000+v[1]*60000+v[2]*1000+v[3]) / 1000, nil
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func validateCues(cues []domain.Cue) error {
	if len(cues) > 100000 {
		return errors.New("too many subtitle cues")
	}
	for i, c := range cues {
		if !finite(c.Start) || !finite(c.End) || c.Start < 0 || c.End <= c.Start || c.End >= 3600000 ||
			!utf8.ValidString(c.Text) || strings.ContainsRune(c.Text, 0) || len(c.Text) > 16384 || normalizeText(c.Text) == "" {
			return fmt.Errorf("invalid subtitle cue %d", i+1)
		}
	}
	return nil
}

// ParseSRT accepts UTF-8, an optional BOM, CR/LF, multiline cues, optional cue
// indices and comma/dot millisecond separators. Malformed cues fail explicitly.
func ParseSRT(data []byte) ([]domain.Cue, error) {
	if len(data) > subtitleLimit || !utf8.Valid(data) {
		return nil, errors.New("SRT must be UTF-8 and at most 8 MiB")
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	cues := make([]domain.Cue, 0)
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		line := i + 1
		if _, err := strconv.Atoi(strings.TrimSpace(lines[i])); err == nil {
			i++
		}
		if i >= len(lines) {
			return nil, fmt.Errorf("SRT line %d: missing timing", line)
		}
		timing := strings.Split(lines[i], "-->")
		if len(timing) != 2 {
			return nil, fmt.Errorf("SRT line %d: invalid timing", i+1)
		}
		start, err := parseTime(timing[0])
		if err != nil {
			return nil, fmt.Errorf("SRT line %d: %w", i+1, err)
		}
		end, err := parseTime(timing[1])
		if err != nil {
			return nil, fmt.Errorf("SRT line %d: %w", i+1, err)
		}
		i++
		first := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			i++
		}
		cues = append(cues, domain.Cue{Start: start, End: end, Text: normalizeText(strings.Join(lines[first:i], "\n"))})
	}
	if err := validateCues(cues); err != nil {
		return nil, err
	}
	return cues, nil
}

func timeSRT(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// FormatSRT rounds to milliseconds and preserves cue order. Invalid input yields
// nil (the signature has no error return); valid empty input yields non-nil empty
// bytes. Native operations always validate before calling this serializer.
func FormatSRT(cues []domain.Cue) []byte {
	if validateCues(cues) != nil {
		return nil
	}
	buf := bytes.NewBuffer(make([]byte, 0))
	for i, c := range cues {
		start := int64(math.Round(c.Start * 1000))
		end := max(start+1, int64(math.Round(c.End*1000)))
		fmt.Fprintf(buf, "%d\n%s --> %s\n%s\n\n", i+1, timeSRT(start), timeSRT(end), normalizeText(c.Text))
	}
	return buf.Bytes()
}

// Timeline intersection, not a global offset: reordered/repeated scenes receive
// their own clipped copy of every intersecting source subtitle.
func timelineCues(scenes []domain.Scene, cues []domain.Cue) []domain.Cue {
	result := make([]domain.Cue, 0)
	offset := 0.0
	for _, scene := range scenes {
		for _, cue := range cues {
			start, end := max(scene.Start, cue.Start), min(scene.End, cue.End, scene.Start+sceneDuration(scene))
			if end > start {
				result = append(result, domain.Cue{Start: offset + start - scene.Start, End: offset + end - scene.Start, Text: cue.Text})
			}
		}
		offset += sceneDuration(scene)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Start < result[j].Start })
	return result
}
