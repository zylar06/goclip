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

func TestSubtitleASSWrapsLongCJK(t *testing.T) {
	tools := New(Config{})
	text := strings.Repeat("这是没有空格的长中文字幕，需要完整显示。", 4)
	cues := []domain.Cue{{Start: .25, End: 5.75, Text: text}}
	for _, dimensions := range [][2]int{{640, 360}, {1080, 1920}, {1920, 1080}} {
		w, h := dimensions[0], dimensions[1]
		data, err := tools.subtitleASS(cues, w, h)
		if err != nil {
			t.Fatal(err)
		}
		out := string(data)
		prefix := "Dialogue: 0,0:00:00.25,0:00:05.75,Default,,0,0,0,,"
		_, body, ok := strings.Cut(out, prefix)
		if !ok {
			t.Fatalf("subtitle timings changed: %s", out)
		}
		lines := strings.Split(strings.TrimSuffix(body, "\n"), `\N`)
		if len(lines) < 2 || strings.Join(lines, "") != text || cues[0].Text != text {
			t.Fatalf("long cue not wrapped losslessly: %q", lines)
		}
		faces, err := tools.titleFaces("NotoSansSC-StaticBold.ttf", float64(subtitleFontSize(w, h)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := faces.close(); err != nil {
				t.Error(err)
			}
		})
		for _, line := range lines {
			width, err := faces.measure(line)
			if err != nil || width.Ceil() > w-2*(w/15) {
				t.Fatalf("subtitle exceeds safe width: %d at %dx%d: %v", width.Ceil(), w, h, err)
			}
		}
	}
}

func TestSubtitleASSRejectsUnrenderableText(t *testing.T) {
	tools := New(Config{})
	for _, text := range []string{"missing \U0010FFFF", strings.Repeat("太长", 2000)} {
		if _, err := tools.subtitleASS([]domain.Cue{{Start: 0, End: 1, Text: text}}, 640, 360); err == nil {
			t.Fatal("missing glyphs or subtitles taller than the frame must fail explicitly")
		}
	}
	if _, err := New(Config{FontDir: t.TempDir()}).subtitleASS(
		[]domain.Cue{{Start: 0, End: 1, Text: "中文"}}, 640, 360); err == nil {
		t.Fatal("missing subtitle font must fail explicitly")
	}
	data, err := tools.subtitleASS([]domain.Cue{{Start: 0, End: 1, Text: "hello\tworld\n中文"}}, 640, 360)
	if err != nil || !strings.Contains(string(data), `hello world\N中文`) {
		t.Fatalf("tabs or explicit newlines mishandled: %s %v", data, err)
	}
}
