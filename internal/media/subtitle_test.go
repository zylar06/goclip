package media

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
)

func TestParseSRT(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []domain.Cue
	}{
		{"BOM and CRLF", "\ufeff1\r\n00:00:00,123 --> 00:01:02,345\r\n你好\r\nworld\r\n\r\n",
			[]domain.Cue{{Start: .123, End: 62.345, Text: "你好\nworld"}}},
		{"optional index and dot", "00:00:01.000 --> 00:00:02.001\nhello", []domain.Cue{{Start: 1, End: 2.001, Text: "hello"}}},
		{"empty", "\ufeff\r\n", []domain.Cue{}},
		{"overlap", "1\n00:00:00,000 --> 00:00:02,000\na\n\n2\n00:00:01,000 --> 00:00:03,000\nb\n",
			[]domain.Cue{{Start: 0, End: 2, Text: "a"}, {Start: 1, End: 3, Text: "b"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSRT([]byte(tc.input))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestParseSRTRejectsMalformed(t *testing.T) {
	for _, input := range []string{
		"\xff", "1", "no timing", "1\n00:00:00,000 --> 00:00:00,000\nx",
		"1\n00:00:02,000 --> 00:00:01,000\nx", "1\n00:60:00,000 --> 01:00:01,000\nx",
		"1\n-00:00:01,000 --> 00:00:02,000\nx", "1\n00:00:01,000 --> 00:00:02,000\n",
		"1\n00:00:01,000 --> 00:00:02,000\nx\x00", strings.Repeat("x", subtitleLimit+1),
	} {
		if _, err := ParseSRT([]byte(input)); err == nil {
			t.Fatalf("accepted malformed SRT (length %d)", len(input))
		}
	}
}

func TestFormatSRTRoundingAndRoundTrip(t *testing.T) {
	cues := []domain.Cue{{Start: 59.9996, End: 3600.0014, Text: "你好\r\nworld"}, {Start: 3600.1, End: 3600.1001, Text: "short"}}
	out := FormatSRT(cues)
	if !bytes.Contains(out, []byte("00:01:00,000 --> 01:00:00,001")) ||
		!bytes.Contains(out, []byte("01:00:00,100 --> 01:00:00,101")) {
		t.Fatalf("bad rounding: %s", out)
	}
	parsed, err := ParseSRT(out)
	if err != nil || len(parsed) != 2 || parsed[0].Text != "你好\nworld" {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
	if FormatSRT(nil) == nil || FormatSRT([]domain.Cue{{Start: math.NaN(), End: 2, Text: "x"}}) != nil {
		t.Fatal("empty and invalid serializers must have distinct results")
	}
}

func TestTimelineIntersectionReorderAndRepeat(t *testing.T) {
	scenes := []domain.Scene{{Start: 4, End: 6}, {Start: 1, End: 3}, {Start: 4, End: 5}}
	cues := []domain.Cue{{Start: 0, End: 2, Text: "A"}, {Start: 2, End: 5, Text: "B"}, {Start: 5, End: 7, Text: "C"}}
	want := []domain.Cue{
		{Start: 0, End: 1, Text: "B"}, {Start: 1, End: 2, Text: "C"},
		{Start: 2, End: 3, Text: "A"}, {Start: 3, End: 4, Text: "B"}, {Start: 4, End: 5, Text: "B"},
	}
	if got := timelineCues(scenes, cues); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRenderTimingQuantization(t *testing.T) {
	scenes := []domain.Scene{{Start: 1, End: 1.111}, {Start: 3, End: 3.151}}
	if sceneFrames(scenes[0]) != 3 || sceneFrames(scenes[1]) != 5 {
		t.Fatal("scene duration must round once to the 30 fps timeline")
	}
	out := timelineCues(scenes, []domain.Cue{{Start: 3, End: 3.1, Text: "x"}})
	if len(out) != 1 || math.Abs(out[0].Start-.1) > .00001 {
		t.Fatalf("subtitles drift from encoded scene durations: %+v", out)
	}
}

func TestASSEscaping(t *testing.T) {
	out := string(formatASS([]domain.Cue{{Start: 0, End: 1, Text: "{\\an8}\nDialogue: injected"}}, 320, 180))
	if strings.Count(out, "\nDialogue:") != 1 || !strings.Contains(out, `\{\\an8\}\NDialogue: injected`) {
		t.Fatalf("ASS text escaped incorrectly: %s", out)
	}
}
