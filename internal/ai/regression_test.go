package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New(testKey + serverSecret) }
func (brokenBody) Close() error             { return errors.New(testKey + serverSecret) }

func TestTransportErrorsAndNonReplayableBodies(t *testing.T) {
	client := New(domain.ModelSettings{BaseURL: "http://localhost/v1", Model: "mock-model", APIKey: testKey})
	var calls int
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.GetBody != nil {
			t.Error("paid body is replayable")
		}
		return nil, errors.New("network failure " + testKey + " " + serverSecret)
	})
	_, err := client.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeEndpoint)
	if calls != 1 {
		t.Fatal("transport retried")
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("raw error is exposed")
	}
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: brokenBody{}, Header: http.Header{}}, nil
	})
	_, err = client.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeEndpoint)
	var zero Client
	_, err = zero.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeModel)
}

func TestClientTimeoutSeparateFromParentContext(t *testing.T) {
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	})
	client.http.Timeout = 20 * time.Millisecond
	_, err := client.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeTimeout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout sentinel unavailable")
	}
}

func TestImageAggregateAndCorruptImageBounds(t *testing.T) {
	path := tinyImage(t, "png")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(t.TempDir(), "broken.png")
	mustWrite(t, corrupt, raw[:len(raw)-16])
	_, _, err = readImage(corrupt)
	assertCode(t, err, CodeInvalidResponse)
	padded := filepath.Join(t.TempDir(), "large-valid.png")
	mustWrite(t, padded, append(raw, make([]byte, maxImageBytes-len(raw))...))
	var frames []domain.Frame
	for i := 0; i < 7; i++ {
		frames = append(frames, domain.Frame{Time: float64(i), Path: padded})
	}
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("oversize input called model")
		w.WriteHeader(500)
	})
	_, err = client.Complete(context.Background(), "test", frames)
	assertCode(t, err, CodeInvalidResponse)
	frames[0].Time = math.NaN()
	_, err = client.Complete(context.Background(), "test", frames[:1])
	assertCode(t, err, CodeInvalidResponse)
}

func TestVisionReturnedIDCannotLeakThroughErrorOrFilename(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = strings.ReplaceAll(scanReply, "event-1", testKey)
	model.fail = "refine"
	dir := t.TempDir()
	_, _, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, visualOptions(), dir, nil)
	ae := assertCode(t, err, CodeAuth)
	if ae.Stage != "02-visual-refine-01" {
		t.Fatal("provider content entered stage name")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), testKey) {
			t.Fatal("key entered checkpoint filename")
		}
	}
}

func TestChangedKeyCanReplayButChangedModelCannot(t *testing.T) {
	model := textModel()
	client, dir := model.client(t), t.TempDir()
	if _, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), dir, nil); err != nil {
		t.Fatal(err)
	}
	settings := client.settings
	settings.APIKey = "replacement-key"
	if _, _, err := AnalyzeText(context.Background(), New(settings), fixtureCues(), textOptions(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if model.count() != 5 {
		t.Fatal("key rotation needlessly invalidated valid stages")
	}
	settings.Model = "different-model"
	_, _, err := AnalyzeText(context.Background(), New(settings), fixtureCues(), textOptions(), dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 5 {
		t.Fatal("model mismatch silently paid for new analysis")
	}
}

func TestCheckpointsWithoutDirectoryAndEmptyCollections(t *testing.T) {
	model := textModel()
	model.replies["clustering"] = "[]"
	client := model.client(t)
	for i := 0; i < 2; i++ {
		drafts, candidates, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(drafts) != 2 || len(candidates) != 2 {
			t.Fatal("empty clusters should preserve individual drafts")
		}
	}
	if model.count() != 10 {
		t.Fatal("empty directory must not use hidden global caches")
	}
}

func TestCancelledCheckpointWriteCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := writeCheckpoint(ctx, filepath.Join(dir, "stage.json"), []byte(`{}`))
	assertCode(t, err, CodeTimeout)
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("cancelled write committed or leaked temporary data")
	}
}

func TestMultiChunkTextPipelineUsesExactChunkEvidence(t *testing.T) {
	cues := make([]domain.Cue, 3)
	for i := range cues {
		cues[i] = domain.Cue{Start: float64(i * 1800), End: float64(i*1800 + 30), Text: fmt.Sprintf("Long source chunk %d", i)}
	}
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, _ := decodeRequest(t, r)
		stage := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		switch stage {
		case "outline":
			answer(t, w, `[{"title":"Chunk topic","subtopics":["Supported point"]}]`)
		case "timeline":
			var input struct {
				Cues   []domain.Cue `json:"cues"`
				Topics []topic      `json:"topics"`
			}
			raw := strings.SplitN(prompt, "INPUT_JSON:\n", 2)
			if len(raw) != 2 || json.Unmarshal([]byte(raw[1]), &input) != nil || len(input.Cues) != 1 || len(input.Topics) != 1 {
				t.Error("chunk prompt contains wrong evidence")
				w.WriteHeader(500)
				return
			}
			answer(t, w, fmt.Sprintf(`[{"topic_id":%q,"start":%v,"end":%v}]`, input.Topics[0].ID, input.Cues[0].Start, input.Cues[0].End))
		case "scoring":
			answer(t, w, `[{"id":"text-1","score":0.2,"reason":"one"},{"id":"text-2","score":0.3,"reason":"two"},{"id":"text-3","score":0.4,"reason":"three"}]`)
		case "titles":
			answer(t, w, `[{"id":"text-1","title":"One","hook":""},{"id":"text-2","title":"Two","hook":""},{"id":"text-3","title":"Three","hook":""}]`)
		case "clustering":
			answer(t, w, "[]")
		default:
			t.Error("unexpected chunk stage", stage)
		}
	})
	opts := textOptions()
	opts.Duration = 30
	drafts, candidates, err := AnalyzeText(context.Background(), client, cues, opts, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 3 || len(candidates) != 3 || calls.Load() != 9 {
		t.Fatal("multi-chunk pipeline lost evidence or reran stages")
	}
	for i, c := range candidates {
		if c.Start != cues[i].Start || c.End != cues[i].End || c.Evidence != cues[i].Text {
			t.Fatal("chunk-relative vs source-relative timestamp error")
		}
	}
}

func TestStrictJSONEnvelopeBoundary(t *testing.T) {
	deep := strings.Repeat("[", 66) + strings.Repeat("]", 66)
	for _, raw := range [][]byte{
		[]byte(deep), []byte(`{"value":"ok","VALUE":"other"}`), []byte(`{} []`), []byte(`{"x":1e999}`),
	} {
		if err := checkJSON(raw); err == nil {
			t.Fatal("accepted ambiguous or unsafe JSON")
		}
	}
	client := New(domain.ModelSettings{BaseURL: "http://localhost", Model: "mock"})
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewBuffer([]byte{0xff}))}, nil
	})
	_, err := client.Complete(context.Background(), "test", nil)
	assertCode(t, err, CodeInvalidResponse)
}

func TestBadOutlineChunkStopsBeforeNextPaidCall(t *testing.T) {
	model := textModel()
	model.replies["outline"] = `[{"title":"","subtopics":["not valid"]}]`
	cues := []domain.Cue{{Start: 0, End: 30, Text: "first"}, {Start: 2000, End: 2030, Text: "second"}}
	_, _, err := AnalyzeText(context.Background(), model.client(t), cues, textOptions(), "", nil)
	assertCode(t, err, CodeInvalidResponse)
	if model.count() != 1 {
		t.Fatal("invalid first chunk spent on later chunks")
	}
}

func TestShortAndOverlappingSubtitleCues(t *testing.T) {
	cues := []domain.Cue{
		{Start: 1, End: 3, Text: "overlapping"},
		{Start: 0, End: .05, Text: "brief"},
		{Start: .05, End: 2, Text: "speech"},
	}
	normalized, duration, err := normalizeCues(cues)
	if err != nil || duration != 3 {
		t.Fatal("positive short/overlapping cues rejected", err)
	}
	if normalized[0].Start != 0 || cues[0].Start != 1 {
		t.Fatal("cue sorting mutated input")
	}
	item := domain.Candidate{Scene: domain.Scene{Label: "Grounded", Start: 0, End: 3}}
	result, err := refineTimeline([]domain.Candidate{item}, normalized, profileFor(duration, 30))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTimeline(result, normalized, profileFor(duration, 30)); err != nil {
		t.Fatal(err)
	}
	if subtitleCoverage(normalized, 0, 3) != 1 {
		t.Fatal("overlapping speech coverage double-counted")
	}
}
