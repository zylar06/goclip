package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

func testAPI(t *testing.T) (*API, http.Handler) {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	a := &API{Store: s, Media: media.New(media.Config{}), Config: Config{MaxBytes: 1024, WebDir: t.TempDir(), Version: "test"}}
	return a, a.Handler()
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestHealthSettingsAndCSRF(t *testing.T) {
	a, h := testAPI(t)
	if w := request(h, "GET", "/api/v1/health", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	body := `{"base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","model":"qwen-plus","api_key":"private-key-123"}`
	w := request(h, "PUT", "/api/v1/settings/text", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-key") {
		t.Fatal("secret leaked")
	}
	w = request(h, "GET", "/api/v1/settings", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-key") {
		t.Fatal(w.Body.String())
	}
	w = request(h, "PUT", "/api/v1/settings/text", `{"base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","model":"new","api_key":""}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	m, e := a.Store.Model("text")
	if e != nil || m.APIKey != "private-key-123" {
		t.Fatal(m, e)
	}
	w = request(h, "PUT", "/api/v1/settings/text", `{"base_url":"https://attacker.example/v1","model":"new","api_key":""}`)
	if w.Code != 400 {
		t.Fatal("stored key could be redirected", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("PUT", "http://localhost/api/v1/settings/text", strings.NewReader(body))
	r.Header.Set("Origin", "https://attacker.example")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = request(h, "POST", "/api/v1/projects", `{"url":"http://127.0.0.1/secrets"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestUploadRevisionAndExportIsolation(t *testing.T) {
	a, h := testAPI(t)
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	p, e := mp.CreateFormFile("video", "中文.mp4")
	if e != nil {
		t.Fatal(e)
	}
	p.Write([]byte("fake-media-for-http-upload-test"))
	sub, e := mp.CreateFormFile("subtitle", "中文.srt")
	if e != nil {
		t.Fatal(e)
	}
	sub.Write([]byte("1\n00:00:00,000 --> 00:00:02,000\n测试\n"))
	if e = mp.Close(); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/api/v1/projects", &buf)
	r.Header.Set("Content-Type", mp.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var project domain.Project
	if e = json.Unmarshal(w.Body.Bytes(), &project); e != nil {
		t.Fatal(e)
	}
	tasks, e := a.Store.Tasks(project.ID)
	if e != nil || len(tasks) != 1 {
		t.Fatal(e)
	}
	if _, e = a.Store.Cancel(tasks[0].ID); e != nil {
		t.Fatal(e)
	}
	project.Duration = 30
	project.Width = 320
	project.Height = 240
	if e = a.Store.UpdateProject(project); e != nil {
		t.Fatal(e)
	}
	dir, e := a.Store.ProjectDir(project.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.SetAsset(project.ID, "source", "source.mp4"); e != nil {
		t.Fatal(e)
	}
	d := domain.NewDraft("测试", []domain.Scene{{ID: domain.ID(), Start: 0, End: 2}})
	d, e = a.Store.SaveDraft(project.ID, d, true)
	if e != nil {
		t.Fatal(e)
	}
	d.Title = "modified"
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	path := "/api/v1/projects/" + project.ID + "/drafts/" + d.ID
	if w = request(h, "PUT", path, string(b)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "PUT", path, string(b)); w.Code != 409 {
		t.Fatal("lost revision conflict", w.Code)
	}
	if w = request(h, "POST", path+"/export", `{"revision":1}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = request(h, "POST", path+"/export", `{"revision":2}`)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job domain.Task
	if e = json.Unmarshal(w.Body.Bytes(), &job); e != nil {
		t.Fatal(e)
	}
	videoPath := "/api/v1/projects/" + project.ID + "/exports/" + job.ID + "/video"
	if w = request(h, "GET", videoPath, ""); w.Code != 404 {
		t.Fatal("incomplete export exposed")
	}
	if _, e = a.Store.Claim(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(dir, "exports", job.ID), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "exports", job.ID, "output.mp4"), []byte("0123456789"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = a.Store.Finish(job.ID, "completed", "", false); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", videoPath, nil)
	r.Header.Set("Range", "bytes=2-4")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "234" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/tasks/"+job.ID+"/events", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: snapshot") {
		t.Fatal(w.Body.String())
	}
}
func TestModelErrorClassAndSecretRedaction(t *testing.T) {
	a, h := testAPI(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"error":{"message":"SECRET-KEY provider raw diagnostic"}}`)
	}))
	defer provider.Close()
	m := domain.ModelSettings{BaseURL: provider.URL + "/v1", Model: "vision", APIKey: "SECRET-KEY"}
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.PutSecret("vision", b); e != nil {
		t.Fatal(e)
	}
	w := request(h, "POST", "/api/v1/settings/vision/test", "{}")
	if w.Code != 502 || !strings.Contains(w.Body.String(), "model_auth") || strings.Contains(w.Body.String(), "SECRET-KEY") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestVisualRequiresConfirmationAndOptIn(t *testing.T) {
	a, h := testAPI(t)
	p := domain.Project{ID: domain.ID(), Duration: 10}
	task, e := a.Store.CreateProject(p, domain.ImportPayload{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.Cancel(task.ID); e != nil {
		t.Fatal(e)
	}
	for _, payload := range []string{
		`{"mode":"visual","confirmed":false,"allow_visual":true}`,
		`{"mode":"visual","confirmed":true,"allow_visual":false}`,
	} {
		w := request(h, "POST", "/api/v1/projects/"+p.ID+"/analyze", payload)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestTitlePreviewUsesSameDimensionsAsExport(t *testing.T) {
	a, h := testAPI(t)
	p := domain.Project{ID: domain.ID(), Duration: 10, Width: 3840, Height: 2160}
	job, e := a.Store.CreateProject(p, domain.ImportPayload{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.Cancel(job.ID); e != nil {
		t.Fatal(e)
	}
	d := domain.NewDraft("中文", []domain.Scene{{ID: domain.ID(), Start: 0, End: 2}})
	d.Hook = "开头文字"
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	w := request(h, "POST", "/api/v1/projects/"+p.ID+"/title-preview", string(b))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	img, e := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	if img.Width != 1920 || img.Height != 1080 {
		t.Fatalf("preview/export mismatch: %+v", img)
	}
}
