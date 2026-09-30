package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/httpapi"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

func productionTools(t *testing.T) (string, string) {
	t.Helper()
	find := func(key, name string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		if v, err := exec.LookPath(name); err == nil {
			return v
		}
		if runtime.GOOS == "windows" {
			v := filepath.Join(os.Getenv("LOCALAPPDATA"), "AutoClip Desktop", "resources", "ffmpeg", name+".exe")
			if _, err := os.Stat(v); err == nil {
				return v
			}
		}
		if os.Getenv("REQUIRE_MEDIA_TESTS") == "1" {
			t.Fatalf("required native tool missing: %s", name)
		}
		t.Skip("native production acceptance requires " + name)
		return ""
	}
	return find("FFMPEG_PATH", "ffmpeg"), find("FFPROBE_PATH", "ffprobe")
}

func TestRealProductionConfirmationToPlayableContent(t *testing.T) {
	ffmpeg, ffprobe := productionTools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Name: "完整语义与字幕默认值", Status: "importing"}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=red:size=160x90:rate=30:duration=80",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=80", "-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", "-c:a", "aac", source}
	if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	cues := []domain.Cue{}
	for i := 0; i < 8; i++ {
		cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: fmt.Sprintf("完整语义句子%d，不应因为默认三十秒被截断。", i)})
	}
	if err = os.WriteFile(filepath.Join(dir, "uploaded.srt"), media.FormatSRT(cues), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		prompt := ""
		for _, m := range req.Messages {
			prompt += m.Content
		}
		replies := map[string]string{
			"outline":  `[{"title":"第一段","subtopics":["解释"]},{"title":"第二段","subtopics":["总结"]}]`,
			"timeline": `[{"topic_id":"topic-1","start":0,"end":40},{"topic_id":"topic-2","start":40,"end":80}]`,
			"scoring":  `[{"id":"text-1","score":0.9,"reason":"完整解释"},{"id":"text-2","score":0.8,"reason":"完整总结"}]`,
			"titles":   `[{"id":"text-1","title":"第一段完整讲解","hook":"第一段"},{"id":"text-2","title":"第二段完整讲解","hook":"第二段"}]`,
		}
		reply := ""
		for stage, v := range replies {
			if strings.Contains(prompt, "AUTOCLIP_STAGE: "+stage+"\n") {
				reply = v
				break
			}
		}
		if reply == "" {
			t.Error("unexpected model stage", prompt)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": reply}}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	if err = s.PutModels(map[string]domain.ModelSettings{"text": {BaseURL: server.URL + "/v1", Model: "offline-acceptance"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(p, domain.ImportPayload{Video: "source.mp4", Subtitle: "uploaded.srt"}); err != nil {
		t.Fatal(err)
	}
	m := media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe, Whisper: "must-not-run-before-confirmation"})
	w := Worker{Store: s, Media: m, TaskTimeout: 90 * time.Second}
	job, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.runTask(ctx, job); err != nil {
		t.Fatal(err)
	}
	p, err = s.Project(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "source_ready" || p.Plan == nil || p.Plan.Status != "awaiting_confirmation" || calls.Load() != 0 {
		t.Fatal("import started production", p, calls.Load())
	}
	a := httpapi.API{Store: s, Media: m}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		a.Handler().ServeHTTP(res, req)
		return res
	}
	options := p.Plan.Options
	options.Goals = []string{"content", "highlight"}
	res := call("PUT", "/api/v1/projects/"+p.ID+"/plan", domain.PlanUpdate{Revision: p.Plan.Revision, Options: options})
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var plan domain.ProductionPlan
	if err = json.Unmarshal(res.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	confirm := domain.ConfirmProduction{PlanRevision: plan.Revision, Confirmed: true}
	res = call("POST", "/api/v1/projects/"+p.ID+"/confirm", confirm)
	if res.Code != 202 {
		t.Fatal(res.Code, res.Body.String())
	}
	var flow domain.Workflow
	if err = json.Unmarshal(res.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	again := call("POST", "/api/v1/projects/"+p.ID+"/confirm", confirm)
	if again.Code != 202 || again.Body.String() != res.Body.String() {
		t.Fatal("duplicate confirmation changed workflow")
	}
	for i := 0; i < 3; i++ {
		job, err = s.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.runTask(ctx, job); err != nil {
			t.Fatal(err)
		}
		got, err := s.Task(job.ID)
		if err != nil || got.Status != "completed" {
			t.Fatal(got, err)
		}
	}
	flow, err = s.Workflow(flow.ID)
	if err != nil || flow.Status != "completed" {
		t.Fatal(flow, err)
	}
	if calls.Load() != 4 {
		t.Fatal("goals did not reuse their paid analysis", calls.Load())
	}
	drafts, err := s.Drafts(p.ID)
	if err != nil || len(drafts) != 4 {
		t.Fatal(drafts, err)
	}
	for _, d := range drafts {
		if d.Subtitles || d.Scenes[0].End-d.Scenes[0].Start <= 30 {
			t.Fatal("subtitle or semantic regression", d)
		}
		if d.Goal == "content" && (d.TitleEnabled == nil || *d.TitleEnabled || d.Title == "") {
			t.Fatal("content must retain title metadata but explicitly disable its overlay", d)
		}
		if d.Goal == "highlight" && d.TitleEnabled != nil && !*d.TitleEnabled {
			t.Fatal("content title suppression leaked into highlight drafts", d)
		}
	}
	exports, err := s.Exports(p.ID)
	if err != nil || len(exports) != 2 {
		t.Fatal(exports, err)
	}
	for _, output := range exports {
		path := filepath.Join(dir, "exports", output.TaskID, "output.mp4")
		info, err := m.Probe(ctx, path)
		if err != nil || info.Duration < 39.967 || info.Duration > 40.034 || !info.HasAudio {
			t.Fatal(info, err)
		}
		if out, err := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput(); err != nil {
			t.Fatalf("full decode: %v %s", err, out)
		}
		frame := productionPNGFrame(t, ctx, ffmpeg, path, .5)
		for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
			for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
				r, g, b, _ := frame.At(x, y).RGBA()
				if r < 50000 || g > 10000 || b > 10000 {
					t.Fatalf("content export painted over the red source at %d,%d before title expiry", x, y)
				}
			}
		}
		req := httptest.NewRequest("GET", "/api/v1/projects/"+p.ID+"/exports/"+output.TaskID+"/video", nil)
		req.Header.Set("Range", "bytes=0-31")
		out := httptest.NewRecorder()
		a.Handler().ServeHTTP(out, req)
		if out.Code != 206 || out.Body.Len() != 32 {
			t.Fatal("file not downloadable", out.Code)
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source changed", err)
	}
}

func TestImportDoesNotTranscribeAndASRFailureDoesNotBlockSilentExport(t *testing.T) {
	ffmpeg, ffprobe := productionTools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Name: "ASR failure"}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:size=160x90:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:v", "libx264", "-threads", "2", "-c:a", "aac", source}
	if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err = s.CreateProject(p, domain.ImportPayload{Video: "source.mp4"}); err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Media: media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe, Whisper: filepath.Join(dir, "missing-whisper")})}
	job, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.runTask(ctx, job); err != nil {
		t.Fatal(err)
	}
	imported, _ := s.Task(job.ID)
	if imported.Status != "completed" {
		t.Fatal(imported)
	}
	if _, err = w.ensureTranscript(ctx, p.ID, dir, func(string, *float64) error { return nil }); err == nil {
		t.Fatal("expected explicit ASR failure")
	}
	p, err = s.Project(p.ID)
	if err != nil || p.SubtitleStatus != "failed" {
		t.Fatal(p, err)
	}
	// Reproduce the old missing-subtitle-asset condition, not just an empty SRT.
	if _, err = s.DB.Exec("DELETE FROM assets WHERE project_id=? AND kind='subtitles'", p.ID); err != nil {
		t.Fatal(err)
	}
	d := domain.NewDraft("无新增字幕", []domain.Scene{{ID: domain.ID(), Start: 0, End: 1}})
	if _, err = s.SaveDraft(p.ID, d, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueExport(p.ID, d.ID, 1); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.runTask(ctx, job); err != nil {
		t.Fatal(err)
	}
	done, _ := s.Task(job.ID)
	if done.Status != "completed" {
		t.Fatal(done)
	}
}

func productionPNGFrame(t *testing.T, ctx context.Context, ffmpeg, source string, at float64) image.Image {
	t.Helper()
	timed, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	data, err := exec.CommandContext(timed, ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-ss", fmt.Sprintf("%.3f", at), "-i", source, "-frames:v", "1", "-threads", "2",
		"-c:v", "png", "-f", "image2pipe", "pipe:1").CombinedOutput()
	if err != nil {
		t.Fatalf("frame extraction: %v %s", err, data)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestLegacyAnalyzeExecutesLinkedPromoWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	p := domain.Project{ID: domain.ID(), Name: "legacy promo route", Duration: 80, SubtitleStatus: "available"}
	imp, err := s.CreateProject(p, domain.ImportPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(imp.ID, "completed", "", false); err != nil {
		t.Fatal(err)
	}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var cues []domain.Cue
	for i := 0; i < 8; i++ {
		cues = append(cues, domain.Cue{Start: float64(i * 10), End: float64((i + 1) * 10), Text: "A complete source-grounded sentence."})
	}
	if err := atomicJSON(filepath.Join(dir, "subtitles.json"), cues); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAsset(p.ID, "subtitles", "subtitles.json"); err != nil {
		t.Fatal(err)
	}
	var calls, promoCalls atomic.Int32
	replies := map[string]string{
		"outline":  `[{"title":"First topic","subtopics":["first"]},{"title":"Second topic","subtopics":["second"]}]`,
		"timeline": `[{"topic_id":"topic-1","start":0,"end":40},{"topic_id":"topic-2","start":40,"end":80}]`,
		"scoring":  `[{"id":"text-1","score":0.9,"reason":"complete"},{"id":"text-2","score":0.8,"reason":"complete"}]`,
		"titles":   `[{"id":"text-1","title":"First","hook":"First regular hook"},{"id":"text-2","title":"Second","hook":"Second regular hook"}]`,
		"promo":    `[{"candidate_id":"text-1","title":"Promo A","hook":"Opening A"},{"candidate_id":"text-1","title":"Promo B","hook":"Opening B"},{"candidate_id":"text-1","title":"Promo C","hook":"Opening C"}]`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 1 {
			t.Error("unexpected model request", err)
			w.WriteHeader(400)
			return
		}
		stage := strings.TrimPrefix(strings.SplitN(req.Messages[0].Content, "\n", 2)[0], "AUTOCLIP_STAGE: ")
		reply, ok := replies[stage]
		if !ok {
			t.Error("unexpected stage", stage)
			w.WriteHeader(400)
			return
		}
		if stage == "promo" {
			promoCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"finish_reason": "stop", "message": map[string]any{"content": reply}},
		}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	if err := s.PutModels(map[string]domain.ModelSettings{"text": {BaseURL: server.URL + "/v1", Model: "offline-promo"}}); err != nil {
		t.Fatal(err)
	}
	// Existing subtitle evidence must suffice; no ASR, frame sampling or source
	// video access is necessary to execute this text-backed legacy request.
	m := media.New(media.Config{FFmpeg: filepath.Join(dir, "must-not-run"), Whisper: filepath.Join(dir, "must-not-run-asr")})
	a := httpapi.API{Store: s, Media: m}
	req := httptest.NewRequest("POST", "/api/v1/projects/"+p.ID+"/analyze",
		strings.NewReader(`{"mode":"subtitle","goals":["promo"],"confirmed":true,"duration":0,"aspect":"original"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	a.Handler().ServeHTTP(res, req)
	if res.Code != 202 {
		t.Fatal(res.Code, res.Body.String())
	}
	var accepted domain.Task
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.WorkflowID == "" || accepted.Kind != "analyze" {
		t.Fatal("legacy API bypassed production", accepted)
	}
	job, err := s.Claim(ctx)
	if err != nil || job.ID != accepted.ID {
		t.Fatal(job, err)
	}
	worker := Worker{Store: s, Media: m, TaskTimeout: 30 * time.Second}
	if err := worker.runTask(ctx, job); err != nil {
		t.Fatal(err)
	}
	finished, err := s.Task(job.ID)
	if err != nil || finished.Status != "completed" {
		t.Fatal(finished, err)
	}
	flow, err := s.Workflow(accepted.WorkflowID)
	if err != nil || flow.Status != "completed" || len(flow.Goals) != 1 || flow.Goals[0].Goal != "promo" ||
		len(flow.Goals[0].DraftIDs) != 3 || len(flow.Goals[0].ExportTaskIDs) != 0 {
		t.Fatalf("wrong promo workflow result: %+v %v", flow, err)
	}
	drafts, err := s.Drafts(p.ID)
	if err != nil || len(drafts) != 3 {
		t.Fatal(drafts, err)
	}
	openings := map[string]bool{}
	for _, draft := range drafts {
		if draft.Goal != "promo" || draft.Origin != "subtitle-promo" || draft.Subtitles ||
			len(draft.Scenes) != 1 || draft.Scenes[0].Start != 0 || draft.Scenes[0].End != 40 ||
			!strings.HasPrefix(draft.Title, "Promo ") || !strings.HasPrefix(draft.Hook, "Opening ") || openings[draft.Hook] {
			t.Fatalf("legacy request returned ordinary or duplicated drafts: %+v", draft)
		}
		openings[draft.Hook] = true
	}
	if calls.Load() != 5 || promoCalls.Load() != 1 {
		t.Fatalf("promo not called once after reused analysis: total=%d promo=%d", calls.Load(), promoCalls.Load())
	}
	t.Log("legacy /analyze -> linked workflow -> four analysis calls + one promo call -> three distinct opening drafts")
}

func TestRealOrphanPreviewRetryRecoversWithoutReencoding(t *testing.T) {
	ffmpeg, ffprobe := productionTools(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	p := domain.Project{ID: domain.ID(), Name: "published-before-commit"}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source.mp4")
	args := []string{"-hide_banner", "-nostdin", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=blue:s=160x90:r=30:d=2",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2",
		"-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", "-c:a", "aac", source}
	if out, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	sourceBefore, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	tools := media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe, Whisper: filepath.Join(dir, "absent-whisper")})
	worker := Worker{Store: s, Media: tools, TaskTimeout: 30 * time.Second}
	if _, err := s.CreateProject(p, domain.ImportPayload{Video: "source.mp4"}); err != nil {
		t.Fatal(err)
	}
	importTask, err := s.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.runTask(ctx, importTask); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Queue(p.ID, "preview", nil); err != nil {
		t.Fatal(err)
	}
	preview, err := s.Claim(ctx)
	if err != nil || preview.Kind != "preview" {
		t.Fatal(preview, err)
	}
	outDir := filepath.Join(dir, "preview", preview.ID)
	output := filepath.Join(outDir, "preview.mp4")
	if _, err := tools.CompatiblePreview(ctx, source, outDir, output, nil); err != nil {
		t.Fatal("must create a genuinely valid H264/AAC orphan", err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(preview.ID); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("preview", preview.ID, "preview.mp4")
	if err := s.CompletePreview(preview.ID, p.ID, rel); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancelled publication must not enter the asset table: %v", err)
	}
	if _, err := s.Asset(p.ID, "preview"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("uncommitted preview appeared as an asset", err)
	}
	if err := s.Finish(preview.ID, "cancelled", "cancelled after native publication", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retry(preview.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Claim(ctx)
	if err != nil || retry.ID != preview.ID {
		t.Fatal(retry, err)
	}
	// Any accidental re-encode fails, whereas verification via ffprobe works.
	worker.Media = media.New(media.Config{FFprobe: ffprobe, FFmpeg: filepath.Join(dir, "must-not-reencode")})
	if err := worker.runTask(ctx, retry); err != nil {
		t.Fatal(err)
	}
	done, err := s.Task(preview.ID)
	if err != nil || done.Status != "completed" || done.Error != "" || done.Retryable {
		t.Fatalf("valid orphan did not recover: %+v %v", done, err)
	}
	asset, err := s.Asset(p.ID, "preview")
	if err != nil || asset != output {
		t.Fatalf("recovery registered the wrong output: %q %v", asset, err)
	}
	after, err := os.ReadFile(output)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(before) {
		t.Fatal("orphan recovery changed the published bytes", err)
	}
	sourceAfter, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(sourceBefore) != sha256.Sum256(sourceAfter) {
		t.Fatal("orphan recovery changed the source", err)
	}
	a := httpapi.API{Store: s, Media: worker.Media}
	req := httptest.NewRequest("GET", "/api/v1/projects/"+p.ID+"/source-preview/video", nil)
	req.Header.Set("Range", "bytes=0-31")
	res := httptest.NewRecorder()
	a.Handler().ServeHTTP(res, req)
	if res.Code != 206 || res.Body.Len() != 32 {
		t.Fatal("recovered preview not downloadable by project ID", res.Code, res.Body.String())
	}
	tasks, err := s.Tasks(p.ID)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("retry created an extra task instead of recovering: %+v %v", tasks, err)
	}
	t.Log("valid H264/AAC orphan recovered under the original task ID with encoder unavailable; bytes unchanged, Range=206")
}
