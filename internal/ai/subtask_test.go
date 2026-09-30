package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"autoclip-go/internal/domain"
)

func TestStageCanonicalizationPersistsSliceHeaderAndScalar(t *testing.T) {
	type result struct {
		Items []int `json:"items"`
		Count int   `json:"count"`
	}
	model := textModel()
	client, dir := model.client(t), t.TempDir()
	calls := 0
	run := func() result {
		r, err := newRunner(context.Background(), client, "input", dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		value, err := stage(context.Background(), r, "canonical", 0, 100, func() (result, error) {
			calls++
			return result{[]int{1, 2, 999}, 0}, nil
		}, func(v *result) error {
			v.Items = v.Items[:2]
			v.Count = len(v.Items)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first, replay := run(), run()
	if calls != 1 || first.Count != 2 || len(first.Items) != 2 || !reflect.DeepEqual(first, replay) {
		t.Fatalf("canonicalization lost at stage boundary/replay: %+v %+v calls=%d", first, replay, calls)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "999") {
		t.Fatal("checkpoint persisted the discarded tail")
	}
}

func TestAnalyzeVisualThirteenEventsBadTailAndReplay(t *testing.T) {
	for _, badTail := range []bool{false, true} {
		t.Run(fmt.Sprint(badTail), func(t *testing.T) {
			var initial visualResult
			if err := json.Unmarshal([]byte(refineReply), &initial); err != nil {
				t.Fatal(err)
			}
			var events []visualEvent
			for i := 1; i <= 13; i++ {
				var item visualEvent
				raw, err := json.Marshal(initial.Events[0])
				if err != nil || json.Unmarshal(raw, &item) != nil {
					t.Fatal("fixture encoding failed", err)
				}
				item.ID = fmt.Sprintf("event-%d", i)
				*item.Score = 88
				if i == 13 && badTail {
					item = visualEvent{} // Nil bounds/score used to panic downstream.
				}
				events = append(events, item)
			}
			raw, err := json.Marshal(visualResult{events})
			if err != nil {
				t.Fatal(err)
			}
			model := visualModel()
			model.replies["visual"], model.replies["titles"] = string(raw), `[]`
			client, dir, frames := model.client(t), t.TempDir(), fixtureFrames(t)
			first, candidates, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(candidates) != 12 || len(first) != 6 || model.count() != 3 {
				t.Fatalf("untruncated output or extra paid work: candidates=%d drafts=%d calls=%d", len(candidates), len(first), model.count())
			}
			raw, err = os.ReadFile(filepath.Join(dir, "01-visual-events.json"))
			if err != nil {
				t.Fatal(err)
			}
			var cp checkpoint
			var stored visualResult
			if json.Unmarshal(raw, &cp) != nil || json.Unmarshal(cp.Data, &stored) != nil || len(stored.Events) != 12 {
				t.Fatal("scan checkpoint retained event thirteen")
			}
			for _, event := range stored.Events {
				if *event.Score != .88 {
					t.Fatal("checkpoint did not persist normalized score")
				}
			}
			replay, replayCandidates, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
			if err != nil || model.count() != 3 || !reflect.DeepEqual(first, replay) || !reflect.DeepEqual(candidates, replayCandidates) {
				t.Fatalf("replay changed canonical result or spent: %v", err)
			}
		})
	}
}

func TestMalformedOutlineChunksStopImmediately(t *testing.T) {
	for _, raw := range []string{
		`[`, `null`, `{}`, `[{"title":"valid","extra":true}]`,
		`[{"title":"valid"}] []`, `[{"title":123}]`, `[{"title":""}]`,
	} {
		for _, badIndex := range []int{1, 2} {
			t.Run(fmt.Sprintf("%d/%s", badIndex, raw), func(t *testing.T) {
				calls := 0
				client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if calls == badIndex {
						answer(t, w, raw)
					} else {
						answer(t, w, `[{"title":"Valid","subtopics":[]}]`)
					}
				})
				cues := []domain.Cue{{Start: 0, End: 30, Text: "first"}, {Start: 2000, End: 2030, Text: "second"}, {Start: 4000, End: 4030, Text: "third"}}
				_, _, err := AnalyzeText(context.Background(), client, cues, textOptions(), t.TempDir(), nil)
				assertCode(t, err, CodeInvalidResponse)
				if calls != badIndex {
					t.Fatalf("malformed chunk triggered later request: calls=%d want=%d", calls, badIndex)
				}
			})
		}
	}
}

func TestAnalysisRetryBudgetAndNonRetryableResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"429", 429, "", 3}, {"500", 500, "", 3}, {"503", 503, "", 3}, {"504", 504, "", 3},
		{"408", 408, "", 1}, {"auth", 401, "", 1}, {"forbidden", 403, "", 1}, {"bad-request", 400, "", 1},
		{"server-auth", 500, `{"error":{"code":"invalid_api_key"}}`, 1},
		{"server-model", 500, `{"error":{"code":"model_not_found"}}`, 1},
		{"quota-on-400", 400, `{"error":{"code":"rate_limit_exceeded"}}`, 1},
		{"bad-json", 200, `{`, 1}, {"bad-schema", 200, `{"unknown":true}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.status == 200 {
					answer(t, w, tc.body)
					return
				}
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			})
			_, err := ask[[]outlineWire](context.Background(), client, "", "outline", nil, nil)
			if err == nil || int(calls.Load()) != tc.want {
				t.Fatalf("retry policy mismatch: calls=%d want=%d err=%v", calls.Load(), tc.want, err)
			}
		})
	}
}

func TestAnalysisNetworkRecoveryAndCancellation(t *testing.T) {
	t.Run("network-third-attempt-succeeds", func(t *testing.T) {
		client := New(domain.ModelSettings{BaseURL: "http://localhost", Model: "test"})
		calls := 0
		client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls < 3 {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection interrupted")}
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"finish_reason":"stop","message":{"content":"[]"}}]}`))}, nil
		})
		value, err := ask[[]outlineWire](context.Background(), client, "", "outline", nil, nil)
		if err != nil || value == nil || calls != 3 {
			t.Fatalf("bounded network recovery failed: calls=%d err=%v", calls, err)
		}
	})
	t.Run("cancel-is-not-retried", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			cancel()
			w.WriteHeader(503)
		})
		_, err := ask[[]outlineWire](ctx, client, "", "outline", nil, nil)
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("cancellation retried/lost: calls=%d err=%v", calls.Load(), err)
		}
	})
	t.Run("no-nested-stage-retries", func(t *testing.T) {
		var calls atomic.Int32
		client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(503)
		})
		_, _, err := AnalyzeText(context.Background(), client, fixtureCues(), textOptions(), t.TempDir(), nil)
		if err == nil || calls.Load() != 3 {
			t.Fatalf("pipeline stacked retries: calls=%d err=%v", calls.Load(), err)
		}
	})
}

func TestChunkCheckpointsResumeOnlyUnfinishedCalls(t *testing.T) {
	for _, failStage := range []string{"outline", "timeline"} {
		t.Run(failStage, func(t *testing.T) {
			cues := []domain.Cue{{Start: 0, End: 30, Text: "first"}, {Start: 2000, End: 2030, Text: "second"}, {Start: 4000, End: 4030, Text: "third"}}
			counts := map[string]int{}
			fail := true
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				prompt, _ := decodeRequest(t, r)
				name := strings.TrimPrefix(strings.SplitN(prompt, "\n", 2)[0], "AUTOCLIP_STAGE: ")
				var input struct {
					Cues   []domain.Cue `json:"cues"`
					Topics []topic      `json:"topics"`
				}
				if err := json.Unmarshal([]byte(strings.SplitN(prompt, "INPUT_JSON:\n", 2)[1]), &input); err != nil {
					t.Error(err)
				}
				key := name
				if len(input.Cues) > 0 {
					key = fmt.Sprintf("%s-%.0f", name, input.Cues[0].Start)
				}
				counts[key]++
				if fail && name == failStage && len(input.Cues) > 0 && input.Cues[0].Start == 2000 {
					w.WriteHeader(401)
					return
				}
				switch name {
				case "outline":
					answer(t, w, `[{"title":"Topic","subtopics":[]}]`)
				case "timeline":
					answer(t, w, fmt.Sprintf(`[{"topic_id":%q,"start":%v,"end":%v}]`, input.Topics[0].ID, input.Cues[0].Start, input.Cues[0].End))
				default:
					answer(t, w, `[]`)
				}
			})
			opts, dir := textOptions(), t.TempDir()
			opts.Duration = 30
			_, progress := progressRecorder(t)
			_, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, progress)
			assertCode(t, err, CodeAuth)
			partial := filepath.Join(dir, "01-outline-chunk-001.json")
			if failStage == "timeline" {
				partial = filepath.Join(dir, "02-timeline-chunk-001.json")
			}
			if _, err := os.Stat(partial); err != nil {
				t.Fatal("completed chunk was not saved", err)
			}
			fail = false
			_, progress = progressRecorder(t)
			drafts, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, progress)
			if err != nil || len(drafts) != 3 {
				t.Fatalf("chunk resume failed: drafts=%d err=%v", len(drafts), err)
			}
			for key, count := range counts {
				want := 1
				if key == failStage+"-2000" {
					want = 2
				}
				if count != want {
					t.Errorf("completed chunk repeated: %s calls=%d want=%d", key, count, want)
				}
			}
			if len(counts) != 8 {
				t.Fatalf("unexpected logical request count: %+v", counts)
			}
			before := fmt.Sprint(counts)
			if _, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, nil); err != nil || before != fmt.Sprint(counts) {
				t.Fatalf("complete replay spent again: %v", err)
			}
		})
	}
}

func TestPartialChunkCheckpointBindsModelMaterialAndVersion(t *testing.T) {
	for _, mutation := range []string{"model", "material", "version", "malformed"} {
		t.Run(mutation, func(t *testing.T) {
			cues := []domain.Cue{{Start: 0, End: 30, Text: "first"}, {Start: 2000, End: 2030, Text: "second"}}
			calls := 0
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					answer(t, w, `[{"title":"Topic"}]`)
				} else {
					w.WriteHeader(401)
				}
			})
			dir := t.TempDir()
			_, _, err := AnalyzeText(context.Background(), client, cues, textOptions(), dir, nil)
			assertCode(t, err, CodeAuth)
			switch mutation {
			case "model":
				client.settings.Model = "different-model"
			case "material":
				cues[0].Text = "different source"
			case "version":
				path := filepath.Join(dir, "01-outline-chunk-001.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				mustWrite(t, path, []byte(strings.Replace(string(raw), pipelineVersion, "old-version", 1)))
			case "malformed":
				mustWrite(t, filepath.Join(dir, "01-outline-chunk-001.json"), []byte(`{`))
			}
			_, _, err = AnalyzeText(context.Background(), client, cues, textOptions(), dir, nil)
			assertCode(t, err, CodeInvalidResponse)
			if calls != 2 {
				t.Fatal("stale partial chunk caused paid replacement")
			}
		})
	}
}

func TestSubtitleTargetsPreserveSemanticUnitsAndBurnIsOptIn(t *testing.T) {
	for _, target := range []int{0, 15, 30} {
		for _, goal := range []string{"content", "highlight"} {
			for _, burn := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/burn=%v", target, goal, burn), func(t *testing.T) {
					model := textModel()
					model.replies["outline"] = `[{"title":"Complete argument","subtopics":[]}]`
					model.replies["timeline"] = `[{"topic_id":"topic-1","start":0,"end":200}]`
					model.replies["scoring"], model.replies["titles"] = `[]`, `[]`
					var cues []domain.Cue
					for i := 0; i < 30; i++ {
						cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: "Part of one argument."})
					}
					opts := textOptions()
					opts.Duration, opts.Goals, opts.BurnSubtitles = target, []string{goal}, burn
					client, dir := model.client(t), t.TempDir()
					drafts, candidates, err := AnalyzeText(context.Background(), client, cues, opts, dir, nil)
					if err != nil {
						t.Fatal(err)
					}
					if len(drafts) != 1 || len(candidates) != 1 || candidates[0].End != 200 || drafts[0].Scenes[0].End != 200 || drafts[0].Subtitles != burn {
						t.Fatalf("semantic unit cut or burn preference lost: %+v %+v", drafts, candidates)
					}
					replay, _, err := AnalyzeText(context.Background(), client, cues, opts, dir, nil)
					if err != nil || !reflect.DeepEqual(drafts, replay) || model.count() != 4 {
						t.Fatalf("replay changed preference or cost: %v", err)
					}
				})
			}
		}
	}
	normalized, err := normalizeOptions(domain.AnalysisOptions{})
	if err != nil || normalized.Duration != 0 || normalized.BurnSubtitles {
		t.Fatalf("defaults silently became 30s or burned subtitles: %+v %v", normalized, err)
	}
}

func TestVisualSamplerUsesFreshDenseImagesAndFingerprintsReplay(t *testing.T) {
	frames := fixtureFrames(t)
	densePath := tinyImage(t, "png")
	data, err := os.ReadFile(densePath)
	if err != nil {
		t.Fatal(err)
	}
	// Distinct valid bytes let the test distinguish dense from coarse images.
	data = append(data, 0)
	mustWrite(t, densePath, data)
	expectedImage := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, parts := decodeRequest(t, r)
		switch {
		case strings.HasPrefix(prompt, "AUTOCLIP_STAGE: visual\n"):
			answer(t, w, scanReply)
		case strings.HasPrefix(prompt, "AUTOCLIP_STAGE: refine\n"):
			count := 0
			for _, p := range parts {
				if p.Type == "image_url" {
					count++
					if p.ImageURL.URL != expectedImage {
						t.Error("review reused coarse frames instead of sampled images")
					}
				}
			}
			if count != 20 {
				t.Errorf("want 20 fresh samples in [8,27], got %d", count)
			}
			// These frames do not exist in the coarse input.
			answer(t, w, strings.Replace(refineReply, `"frame_times":[10,20]`, `"frame_times":[11,21]`, 1))
		default:
			answer(t, w, `[]`)
		}
	})
	sampleCalls := 0
	sampler := func(ctx context.Context, times []float64) ([]domain.Frame, error) {
		sampleCalls++
		if len(times) != 20 || times[0] != 8 || times[len(times)-1] != 27 {
			t.Fatalf("wrong fresh review window: %v", times)
		}
		var out []domain.Frame
		for _, tm := range times {
			out = append(out, domain.Frame{Time: tm, Path: densePath})
		}
		return out, nil
	}
	opts, dir := visualOptions(), t.TempDir()
	opts.Duration = 0
	drafts, candidates, err := AnalyzeVisualWithSampler(context.Background(), client, frames, 60, opts, dir, nil, sampler)
	if err != nil {
		t.Fatal(err)
	}
	replay, replayCandidates, err := AnalyzeVisualWithSampler(context.Background(), client, frames, 60, opts, dir, nil, sampler)
	if err != nil || sampleCalls != 2 || calls.Load() != 3 || !reflect.DeepEqual(drafts, replay) || !reflect.DeepEqual(candidates, replayCandidates) {
		t.Fatalf("dense replay changed output or repeated models: calls=%d samples=%d err=%v", calls.Load(), sampleCalls, err)
	}
	mustWrite(t, densePath, append(data, 1))
	_, _, err = AnalyzeVisualWithSampler(context.Background(), client, frames, 60, opts, dir, nil, sampler)
	assertCode(t, err, CodeInvalidResponse)
	if calls.Load() != 3 {
		t.Fatal("changed dense image bypassed checkpoint fingerprint")
	}
}

func TestReviewTimesBoundedAndSamplerFailuresNeverSpendOnRefine(t *testing.T) {
	for _, bounds := range [][3]float64{{0, 34, 60}, {58, 60, 60}, {0, .1, .1}, {1.1234, 500, 600}, {8, 27, 60}} {
		times := reviewTimes(bounds[0], bounds[1], bounds[2])
		if len(times) == 0 || len(times) > 25 {
			t.Fatalf("bad sample budget: %v", times)
		}
		step := math.Max(1, (bounds[1]-bounds[0])/24)
		for i, tm := range times {
			if tm < bounds[0]-frameTimeTolerance || tm > bounds[1]+frameTimeTolerance || tm >= bounds[2] {
				t.Fatal("sample outside source/window", times)
			}
			if i > 0 && math.Abs(tm-times[i-1]-step) > .002 {
				t.Fatal("dense sampler did not use bounded step", times)
			}
		}
	}
	for _, failure := range []string{"error", "cancel", "missing", "wrong-time"} {
		t.Run(failure, func(t *testing.T) {
			model := visualModel()
			frames := fixtureFrames(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sampler := func(ctx context.Context, times []float64) ([]domain.Frame, error) {
				switch failure {
				case "error":
					return nil, errors.New("private path must not leak")
				case "cancel":
					cancel()
					return nil, context.Canceled
				case "missing":
					return frames[:1], nil
				default:
					out := make([]domain.Frame, len(times))
					for i, tm := range times {
						out[i] = domain.Frame{Time: tm + .5, Path: frames[0].Path}
					}
					return out, nil
				}
			}
			_, _, err := AnalyzeVisualWithSampler(ctx, model.client(t), frames, 60, visualOptions(), t.TempDir(), nil, sampler)
			if err == nil || strings.Contains(err.Error(), "private path") || model.count() != 1 {
				t.Fatalf("bad sampler caused spending or error leak: calls=%d err=%v", model.count(), err)
			}
			if failure == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("sampler cancellation lost identity", err)
			}
		})
	}
}

func TestVisualAutomaticAndExplicitDurationStayHardCaps(t *testing.T) {
	for _, target := range []int{0, 15, 30} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			// Evidence spanning 50s must not make the visual cap optional.
			model := visualModel()
			model.replies["visual"] = `{"events":[{"id":"event-1","label":"Scene","start":0,"end":55,"kind":"other","score":0.9,"frame_times":[0,50]}]}`
			model.replies["refine"] = `{"events":[{"id":"event-1","label":"Scene","start":0,"end":15,"kind":"other","score":0.9,"frame_times":[0,10]}]}`
			model.replies["titles"] = `[]`
			opts := visualOptions()
			opts.Duration = target
			drafts, candidates, err := AnalyzeVisual(context.Background(), model.client(t), fixtureFrames(t), 60, opts, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			cap := float64(target)
			if cap == 0 {
				cap = 30
			}
			for _, c := range candidates {
				if c.End-c.Start > cap+frameTimeTolerance {
					t.Fatal("visual event exceeded cap", c)
				}
			}
			for _, d := range drafts {
				if d.Scenes[0].End-d.Scenes[0].Start > cap+frameTimeTolerance {
					t.Fatal("visual context buffer exceeded cap")
				}
			}
		})
	}
}

func promoCandidates() []domain.Candidate {
	var candidates []domain.Candidate
	for i := 0; i < 6; i++ {
		candidates = append(candidates, domain.Candidate{
			Scene: domain.Scene{ID: fmt.Sprintf("source-%d", i), Label: fmt.Sprintf("Label %d", i), Evidence: fmt.Sprintf("Visible evidence %d", i), Start: float64(i * 20), End: float64(i*20 + 15)},
			Score: .9 - float64(i)*.1, Kind: "other",
		})
	}
	return candidates
}

func TestMakePromosOnlyUsesEvidencePreservesIntervalsAndCapsThree(t *testing.T) {
	candidates := promoCandidates()
	input := append([]domain.Candidate(nil), candidates...)
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, parts := decodeRequest(t, r)
		if len(parts) != 0 || !strings.HasPrefix(prompt, "AUTOCLIP_STAGE: promo\n") {
			t.Error("initial promo unexpectedly used images or editing stage")
		}
		var input struct {
			Candidates []map[string]any `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(strings.SplitN(prompt, "INPUT_JSON:\n", 2)[1]), &input); err != nil {
			t.Fatal(err)
		}
		if len(input.Candidates) != 6 {
			t.Fatal("promo prompt must offer up to six input candidates")
		}
		var titles []promoWire
		for _, c := range input.Candidates[:3] {
			if len(c) != 3 || c["label"] == nil || c["evidence"] == nil || c["id"] == nil {
				t.Errorf("promo prompt included non-evidence fields: %+v", c)
			}
			titles = append(titles, promoWire{CandidateID: c["id"].(string), Title: "Supported title " + c["id"].(string), Hook: ""})
		}
		raw, err := json.Marshal(titles)
		if err != nil {
			t.Error(err)
		}
		answer(t, w, string(raw))
	})
	dir, opts := t.TempDir(), textOptions()
	drafts, err := MakePromos(context.Background(), client, candidates, opts, dir, nil)
	if err != nil || len(drafts) != 3 {
		t.Fatalf("initial promo generation failed: drafts=%d err=%v", len(drafts), err)
	}
	for i, d := range drafts {
		if len(d.Scenes) != 1 || d.Scenes[0] != candidates[i].Scene || d.Origin != "promo" || d.Subtitles {
			t.Fatal("promo changed verified interval/evidence or burned subtitles", d)
		}
	}
	replay, err := MakePromos(context.Background(), client, candidates, opts, dir, nil)
	if err != nil || calls.Load() != 1 || !reflect.DeepEqual(drafts, replay) || !reflect.DeepEqual(candidates, input) {
		t.Fatalf("promo replay repeated model or mutated input: %v", err)
	}
	candidates[0].End++
	_, err = MakePromos(context.Background(), client, candidates, opts, dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	if calls.Load() != 1 {
		t.Fatal("changed promo source bypassed checkpoint invalidation")
	}
}

func TestPromoInvalidInputsAndStructureDoNotRetryOrRepair(t *testing.T) {
	model := textModel()
	model.replies["promo"] = `[{"candidate_id":"source-0","title":"Invented","hook":"","start":999}]`
	client := model.client(t)
	opts := textOptions()
	opts.Confirmed = false
	if _, err := MakePromos(context.Background(), client, promoCandidates(), opts, "", nil); err == nil || model.count() != 0 {
		t.Fatal("unconfirmed promos spent")
	}
	opts.Confirmed = true
	oversize := append(promoCandidates(), domain.Candidate{})
	if _, err := MakePromos(context.Background(), client, oversize, opts, "", nil); err == nil || model.count() != 0 {
		t.Fatal("more than six promo candidates reached model")
	}
	candidates := promoCandidates()
	candidates[1].ID = candidates[0].ID
	if _, err := MakePromos(context.Background(), client, candidates, opts, "", nil); err == nil || model.count() != 0 {
		t.Fatal("invalid promo source spent")
	}
	dir := t.TempDir()
	_, err := MakePromos(context.Background(), client, promoCandidates(), opts, dir, nil)
	assertCode(t, err, CodeInvalidResponse)
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 || model.count() != 1 {
		t.Fatalf("malformed promo was repaired or checkpointed: files=%v calls=%d err=%v", files, model.count(), err)
	}
}

func TestMakePromosAcceptsVisualModeWithTextMetadataOnly(t *testing.T) {
	model := textModel()
	model.replies["promo"] = `[{"candidate_id":"source-0","title":"Visible scene","hook":""}]`
	opts := visualOptions()
	opts.BurnSubtitles = true
	drafts, err := MakePromos(context.Background(), model.client(t), promoCandidates()[:1], opts, "", nil)
	if err != nil || len(drafts) != 1 || drafts[0].Subtitles || model.count() != 1 {
		t.Fatalf("visual candidates not accepted by text-only promo metadata stage: %v", err)
	}
}

func TestCachedRefinementSemanticFailureCannotBecomeASkip(t *testing.T) {
	model := visualModel()
	model.replies["visual"] = twoPlayableScan
	model.replies["refine"] = `{"events":[{"id":"strong","label":"Stronger segment","start":20,"end":34,"kind":"other","score":0.9,"frame_times":[20,30]}]}`
	model.replies["titles"] = `[]`
	client, frames, dir := model.client(t), fixtureFrames(t), t.TempDir()
	if _, _, err := AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, refineStage+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	if err := json.Unmarshal(raw, &cp); err != nil {
		t.Fatal(err)
	}
	cp.Data = []byte(strings.Replace(string(cp.Data), `"id":"strong"`, `"id":"fabricated"`, 1))
	cp.DataHash = digest(cp.Data)
	raw, err = json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, raw)
	_, _, err = AnalyzeVisual(context.Background(), client, frames, 60, visualOptions(), dir, nil)
	ae := assertCode(t, err, CodeInvalidResponse)
	if ae.Stage != refineStage || !strings.Contains(ae.Message, "Checkpoint") || rejectedRefinement(err) || model.count() != 3 {
		t.Fatalf("cached corruption was treated as skippable model output: %+v calls=%d", ae, model.count())
	}
}

func TestScreenIsStaticAdvisoryAndBounded(t *testing.T) {
	var calls atomic.Int32
	client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		prompt, parts := decodeRequest(t, r)
		if !strings.Contains(prompt, "No audio or transcript") || !strings.Contains(prompt, "advisory") {
			t.Error("screen prompt pretends to know speech or authorizes analysis")
		}
		images := 0
		for _, p := range parts {
			if p.Type == "image_url" {
				images++
			}
		}
		if images != 4 {
			t.Errorf("screen must use at most 4 stills, got %d", images)
		}
		answer(t, w, `{"mode":"subtitle","goals":["content"],"reason":"A presenter is visible; narration is unknown."}`)
	})
	path := tinyImage(t, "png")
	var frames []domain.Frame
	for i := 0; i < 50; i++ {
		frames = append(frames, domain.Frame{Time: float64(i), Path: path})
	}
	result, err := Screen(context.Background(), client, frames, 60)
	if err != nil || result.Mode != "subtitle" || len(result.Goals) != 1 || !strings.Contains(result.Reason, "speech/audio was not analyzed") || calls.Load() != 1 {
		t.Fatalf("bad advisory screen: %+v err=%v calls=%d", result, err, calls.Load())
	}
}

func TestScreenRejectsInvalidRecommendationsWithoutRepair(t *testing.T) {
	for _, reply := range []string{
		`{"mode":"auto","goals":["content"],"reason":"visible"}`,
		`{"mode":"visual","goals":["translate"],"reason":"visible"}`,
		`{"mode":"visual","goals":["content","content"],"reason":"visible"}`,
		`{"mode":"visual","goals":["content"],"reason":""}`,
		`{"mode":"visual","goals":["content"],"reason":"visible","extra":true}`,
	} {
		t.Run(reply, func(t *testing.T) {
			var calls atomic.Int32
			client, _ := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				answer(t, w, reply)
			})
			_, err := Screen(context.Background(), client, fixtureFrames(t), 60)
			assertCode(t, err, CodeInvalidResponse)
			if calls.Load() != 1 {
				t.Fatal("screen repaired malformed recommendation")
			}
		})
	}
}
