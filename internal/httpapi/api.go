package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

type Config struct {
	WebDir, Version string
	MaxBytes        int64
}
type API struct {
	Store  *store.Store
	Media  *media.Tools
	Config Config
}
type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
}
type endpoint func(http.ResponseWriter, *http.Request) error
type problem struct {
	status        int
	code, message string
	retryable     bool
}

func (p *problem) Error() string { return p.message }
func bad(message string) error   { return &problem{400, "invalid_request", message, false} }
func (a *API) route(mux *http.ServeMux, pattern string, fn endpoint) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			a.fail(w, r, err)
		}
	})
}
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := 500
	e := Error{Code: "internal_error", Message: "Server operation failed; see server log with request_id", RequestID: w.Header().Get("X-Request-ID")}
	var p *problem
	var modelError *ai.Error
	switch {
	case errors.As(err, &modelError):
		status = 502
		e.Code = "model_" + modelError.Code
		e.Message = modelError.Error()
		e.Retryable = modelError.Retryable
	case errors.As(err, &p):
		status = p.status
		e.Code = p.code
		e.Message = p.message
		e.Retryable = p.retryable
	case errors.Is(err, store.ErrNotFound), errors.Is(err, os.ErrNotExist):
		status = 404
		e.Code = "not_found"
		e.Message = "Resource not found"
	case errors.Is(err, store.ErrConflict):
		status = 409
		e.Code = "conflict"
		e.Message = "Project is busy or draft revision changed. Refresh and retry."
	case errors.Is(err, context.Canceled):
		status = 408
		e.Code = "cancelled"
		e.Message = "Request cancelled"
	default:
		slog.Error("request failed", "request_id", e.RequestID, "path", r.URL.Path, "error", err)
	}
	if writeErr := jsonResponse(w, status, e); writeErr != nil {
		slog.Debug("error response disconnected", "request_id", e.RequestID, "error", writeErr)
	}
}
func jsonResponse(w http.ResponseWriter, status int, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, err = w.Write(append(b, '\n'))
	return err
}
func body(r *http.Request, v any) error {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		return bad("Content-Type must be application/json")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return bad("Invalid JSON body: " + err.Error())
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return bad("Expected a single JSON object")
	}
	return nil
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	a.route(mux, "GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) error {
		if err := a.Store.DB.PingContext(r.Context()); err != nil {
			return err
		}
		return jsonResponse(w, 200, map[string]any{"status": "ok", "version": a.Config.Version})
	})
	a.route(mux, "GET /api/v1/version", func(w http.ResponseWriter, r *http.Request) error {
		return jsonResponse(w, 200, map[string]string{"version": a.Config.Version})
	})
	a.route(mux, "GET /api/v1/projects", func(w http.ResponseWriter, r *http.Request) error {
		p, e := a.Store.Projects()
		if e != nil {
			return e
		}
		return jsonResponse(w, 200, p)
	})
	a.route(mux, "POST /api/v1/projects", a.createProject)
	a.route(mux, "GET /api/v1/projects/{id}", a.workspace)
	a.route(mux, "DELETE /api/v1/projects/{id}", a.deleteProject)
	a.route(mux, "POST /api/v1/projects/{id}/analyze", a.analyze)
	a.route(mux, "GET /api/v1/projects/{id}/plan", a.getPlan)
	a.route(mux, "PUT /api/v1/projects/{id}/plan", a.updatePlan)
	a.route(mux, "POST /api/v1/projects/{id}/confirm", a.confirmProduction)
	a.route(mux, "POST /api/v1/projects/{id}/inspect", a.inspect)
	a.route(mux, "GET /api/v1/projects/{id}/thumbnail", a.thumbnail)
	a.route(mux, "GET /api/v1/projects/{id}/drafts/{draftId}/thumbnail", a.thumbnail)
	a.route(mux, "POST /api/v1/projects/{id}/drafts/disable-subtitles", a.disableSubtitles)
	a.route(mux, "GET /api/v1/projects/{id}/source-preview", a.previewStatus)
	a.route(mux, "POST /api/v1/projects/{id}/source-preview", a.startPreview)
	a.route(mux, "GET /api/v1/projects/{id}/source-preview/video", a.previewVideo)
	a.route(mux, "GET /api/v1/projects/{id}/source", a.source)
	a.route(mux, "GET /api/v1/projects/{id}/subtitles", a.subtitles)
	a.route(mux, "POST /api/v1/projects/{id}/drafts", a.createDraft)
	a.route(mux, "PUT /api/v1/projects/{id}/drafts/{draftId}", a.saveDraft)
	a.route(mux, "POST /api/v1/projects/{id}/drafts/{draftId}/duplicate", a.duplicate)
	a.route(mux, "POST /api/v1/projects/{id}/title-preview", a.title)
	a.route(mux, "POST /api/v1/projects/{id}/drafts/{draftId}/export", a.export)
	a.route(mux, "GET /api/v1/projects/{id}/exports/{taskId}/video", a.video)
	a.route(mux, "GET /api/v1/tasks/{id}", a.task)
	a.route(mux, "GET /api/v1/tasks/{id}/events", a.events)
	a.route(mux, "POST /api/v1/tasks/{id}/cancel", func(w http.ResponseWriter, r *http.Request) error {
		t, e := a.Store.Cancel(r.PathValue("id"))
		if e != nil {
			return e
		}
		return jsonResponse(w, 200, t)
	})
	a.route(mux, "POST /api/v1/tasks/{id}/retry", func(w http.ResponseWriter, r *http.Request) error {
		t, e := a.Store.Retry(r.PathValue("id"))
		if e != nil {
			return e
		}
		return jsonResponse(w, 200, t)
	})
	a.route(mux, "GET /api/v1/settings", a.settings)
	a.route(mux, "PUT /api/v1/settings/cookies", a.cookies)
	a.route(mux, "DELETE /api/v1/settings/cookies", func(w http.ResponseWriter, r *http.Request) error {
		if e := a.Store.DeleteSecret("cookies"); e != nil {
			return e
		}
		return jsonResponse(w, 200, map[string]bool{"ok": true})
	})
	a.route(mux, "PUT /api/v1/settings/{kind}", a.saveSettings)
	a.route(mux, "POST /api/v1/settings/{kind}/test", a.testSettings)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { a.fail(w, r, store.ErrNotFound) })
	mux.Handle("/", a.static())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", domain.ID())
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if err := sameOrigin(r); err != nil {
				a.fail(w, r, err)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func sameOrigin(r *http.Request) error {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return &problem{403, "cross_origin", "Cross-origin writes are disabled", false}
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		// TLS termination may forward https with an exact host; no arbitrary proxy host trust.
		if err != nil || u.Host != r.Host || (u.Scheme != scheme && u.Scheme != "https") {
			return &problem{403, "cross_origin", "Cross-origin writes are disabled", false}
		}
	}
	return nil
}
func (a *API) static() http.Handler {
	fs := http.FileServer(http.Dir(a.Config.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method not allowed", 405)
			return
		}
		rel := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+r.URL.Path)), "/")
		if rel == "" {
			rel = "index.html"
		}
		info, err := os.Stat(filepath.Join(a.Config.WebDir, rel))
		if err == nil && !info.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		if strings.Contains(filepath.Base(rel), ".") {
			http.NotFound(w, r)
			return
		}
		if _, err = os.Stat(filepath.Join(a.Config.WebDir, "index.html")); err != nil {
			http.Error(w, "Web assets missing; build web or use Docker image", 503)
			return
		}
		http.ServeFile(w, r, filepath.Join(a.Config.WebDir, "index.html"))
	})
}
func (a *API) createProject(w http.ResponseWriter, r *http.Request) (err error) {
	id := domain.ID()
	dir, err := a.Store.ProjectDir(id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			if e := os.RemoveAll(dir); e != nil {
				slog.Error("upload cleanup failed", "project", id, "error", e)
			}
		}
	}()
	p := domain.Project{ID: id, Name: "新项目", Status: "importing", CreatedAt: domain.Now(), UpdatedAt: domain.Now()}
	input := domain.ImportPayload{}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "application/json" {
		var b struct {
			Name        string `json:"name"`
			URL         string `json:"url"`
			Instruction string `json:"instruction"`
		}
		if err = body(r, &b); err != nil {
			return err
		}
		if err = media.ValidateSourceURL(b.URL); err != nil {
			return bad(err.Error())
		}
		p.URL = b.URL
		input.URL = b.URL
		input.Instruction = b.Instruction
		if b.Name != "" {
			p.Name = b.Name
		}
	} else if mt == "multipart/form-data" {
		limit := a.Config.MaxBytes
		if limit <= 0 {
			limit = 4 << 30
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit+(12<<20))
		reader, e := r.MultipartReader()
		if e != nil {
			return bad("Invalid multipart upload")
		}
		for {
			part, e := reader.NextPart()
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				return bad("Upload truncated or exceeds size limit")
			}
			switch part.FormName() {
			case "url", "instruction":
				b, e := io.ReadAll(io.LimitReader(part, 4097))
				if e != nil {
					return e
				}
				if len(b) > 4096 {
					return bad("Import field exceeds size limit")
				}
				if part.FormName() == "url" {
					if input.URL != "" {
						return bad("Only one URL per project")
					}
					input.URL = strings.TrimSpace(string(b))
					if e = media.ValidateSourceURL(input.URL); e != nil {
						return bad(e.Error())
					}
					p.URL = input.URL
				} else {
					input.Instruction = string(b)
				}
			case "name":
				b, e := io.ReadAll(io.LimitReader(part, 1024))
				if e != nil {
					return e
				}
				p.Name = strings.TrimSpace(string(b))
			case "video":
				if input.Video != "" {
					return bad("Only one video per project")
				}
				ext := strings.ToLower(filepath.Ext(part.FileName()))
				if !slices.Contains([]string{".mp4", ".mkv", ".mov", ".webm", ".avi", ".m4v", ".flv", ".ts"}, ext) {
					return bad("Unsupported video extension")
				}
				input.Video = "source" + ext
				if err = copyUpload(filepath.Join(dir, input.Video), part, limit); err != nil {
					return err
				}
			case "subtitle":
				if input.Subtitle != "" {
					return bad("Only one SRT per project")
				}
				input.Subtitle = "uploaded.srt"
				if err = copyUpload(filepath.Join(dir, input.Subtitle), part, 10<<20); err != nil {
					return err
				}
				b, e := os.ReadFile(filepath.Join(dir, input.Subtitle))
				if e != nil {
					return e
				}
				if _, e = media.ParseSRT(b); e != nil {
					return bad("Invalid SRT: " + e.Error())
				}
			default:
				return bad("Unknown multipart field: " + part.FormName())
			}
			if err = part.Close(); err != nil {
				return err
			}
		}
		if (input.Video == "") == (input.URL == "") {
			return bad("Provide exactly one video file or URL")
		}
	} else {
		return bad("Use application/json or multipart/form-data")
	}
	if p.Name == "" {
		p.Name = "新项目"
	}
	if len([]rune(p.Name)) > 200 {
		return bad("Project name is too long")
	}
	if len(input.Instruction) > 4000 {
		return bad("Instructions exceed 4000 UTF-8 bytes")
	}
	if _, err = a.Store.CreateProject(p, input); err != nil {
		return err
	}
	committed = true
	return jsonResponse(w, 201, p)
}
func copyUpload(path string, r io.Reader, limit int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 || n > limit {
		return &problem{413, "size_limit", "Empty upload or upload size limit exceeded", false}
	}
	return nil
}
func (a *API) workspace(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	p, e := a.Store.Project(id)
	if e != nil {
		return e
	}
	p, e = a.Store.ProjectEvidence(p)
	if e != nil {
		return e
	}
	d, e := a.Store.Drafts(id)
	if e != nil {
		return e
	}
	t, e := a.Store.Tasks(id)
	if e != nil {
		return e
	}
	c, e := a.Store.Candidates(id)
	if e != nil {
		return e
	}
	x, e := a.Store.Exports(id)
	if e != nil {
		return e
	}
	flows, e := a.Store.Workflows(id)
	if e != nil {
		return e
	}
	return jsonResponse(w, 200, map[string]any{"project": p, "drafts": d, "tasks": t, "candidates": c, "exports": x, "workflows": flows})
}
func (a *API) deleteProject(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Confirm bool `json:"confirm"`
	}
	if e := body(r, &b); e != nil {
		return e
	}
	if !b.Confirm {
		return bad("Explicit deletion confirmation required")
	}
	id := r.PathValue("id")
	dir, e := a.Store.ProjectDir(id)
	if e != nil {
		return bad("Invalid project id")
	}
	if e = a.Store.DeleteProject(id); e != nil {
		return e
	}
	// dir is constructed exclusively from a validated ID under the data root.
	if e = os.RemoveAll(dir); e != nil {
		return fmt.Errorf("metadata deleted but media cleanup failed: %w", e)
	}
	return jsonResponse(w, 200, map[string]bool{"ok": true})
}
func (a *API) analyze(w http.ResponseWriter, r *http.Request) error {
	p, e := a.Store.Project(r.PathValue("id"))
	if e != nil {
		return e
	}
	if p.Duration <= 0 {
		return &problem{409, "source_not_ready", "Wait for source import to complete", false}
	}
	o := domain.AnalysisOptions{Aspect: "original", Goals: []string{"content"}}
	if e = body(r, &o); e != nil {
		return e
	}
	if !o.Confirmed {
		return bad("Confirm production before analysis")
	}
	if o.Mode != "subtitle" && o.Mode != "auto" && o.Mode != "visual" {
		return bad("Choose subtitle, smart or visual analysis")
	}
	if o.Mode == "visual" && !o.AllowVisual {
		return bad("Visual analysis requires explicit permission to upload sampled images")
	}
	if (o.Duration != 0 && o.Duration < 10) || o.Duration > 120 || !slices.Contains([]string{"original", "portrait", "landscape"}, o.Aspect) || len(o.Instruction) > 4000 ||
		(o.Category != "" && !slices.Contains(ai.Categories, o.Category)) {
		return bad("Invalid analysis options")
	}
	if len(o.Goals) < 1 || len(o.Goals) > 3 {
		return bad("Select 1–3 goals")
	}
	seen := map[string]bool{}
	for _, g := range o.Goals {
		if seen[g] || !slices.Contains([]string{"content", "highlight", "promo"}, g) {
			return bad("Invalid or duplicate goal")
		}
		seen[g] = true
	}
	kind := "text"
	if o.Mode == "visual" {
		kind = "vision"
	}
	m, e := a.Store.Model(kind)
	if errors.Is(e, store.ErrNotFound) || m.BaseURL == "" {
		return bad("Configure the " + kind + " model first")
	}
	if e != nil {
		return e
	}
	// Preserve the Task response while using the same production goal routing.
	if e = validateProductionOptions(o); e != nil {
		return e
	}
	if e = a.validateProductionModels(o); e != nil {
		return e
	}
	plan, e := a.Store.Plan(p.ID)
	if e != nil {
		return e
	}
	plan, e = a.Store.UpdatePlan(p.ID, domain.PlanUpdate{Revision: plan.Revision, Options: o})
	if e != nil {
		return e
	}
	flow, e := a.Store.ConfirmProduction(p.ID, plan.Revision)
	if e != nil {
		return e
	}
	tasks, e := a.Store.Tasks(p.ID)
	if e != nil {
		return e
	}
	for _, t := range tasks {
		if t.WorkflowID == flow.ID && t.Kind == "analyze" {
			return jsonResponse(w, 202, t)
		}
	}
	return errors.New("confirmed workflow has no analysis task")
}
func (a *API) source(w http.ResponseWriter, r *http.Request) error {
	path, e := a.Store.Asset(r.PathValue("id"), "source")
	if e != nil {
		return e
	}
	if _, e = os.Stat(path); e != nil {
		return e
	}
	http.ServeFile(w, r, path)
	return nil
}
func (a *API) subtitles(w http.ResponseWriter, r *http.Request) error {
	path, e := a.Store.Asset(r.PathValue("id"), "subtitles")
	if e != nil {
		return e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	var cues []domain.Cue
	if e = json.Unmarshal(b, &cues); e != nil {
		return e
	}
	return jsonResponse(w, 200, cues)
}
func (a *API) createDraft(w http.ResponseWriter, r *http.Request) error {
	d := domain.NewDraft("", nil)
	if e := body(r, &d); e != nil {
		return e
	}
	d.ID = domain.ID()
	d.Revision = 1
	d.Origin = "manual"
	p, e := a.Store.Project(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = d.Validate(p.Duration); e != nil {
		return bad(e.Error())
	}
	d, e = a.Store.SaveDraft(p.ID, d, true)
	if e != nil {
		return e
	}
	return jsonResponse(w, 201, d)
}
func (a *API) saveDraft(w http.ResponseWriter, r *http.Request) error {
	var d domain.Draft
	if e := body(r, &d); e != nil {
		return e
	}
	if d.ID != r.PathValue("draftId") {
		return bad("Draft id mismatch")
	}
	p, e := a.Store.Project(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = d.Validate(p.Duration); e != nil {
		return bad(e.Error())
	}
	d, e = a.Store.SaveDraft(p.ID, d, false)
	if e != nil {
		return e
	}
	return jsonResponse(w, 200, d)
}
func (a *API) duplicate(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Title string `json:"title"`
	}
	if e := body(r, &b); e != nil {
		return e
	}
	id := r.PathValue("id")
	d, e := a.Store.Draft(id, r.PathValue("draftId"))
	if e != nil {
		return e
	}
	parent, revision := d.ID, d.Revision
	d.ParentDraftID = &parent
	d.ParentRevision = &revision
	d.ID = domain.ID()
	d.Revision = 1
	d.Title = b.Title
	p, e := a.Store.Project(id)
	if e != nil {
		return e
	}
	if e = d.Validate(p.Duration); e != nil {
		return bad(e.Error())
	}
	d, e = a.Store.SaveDraft(id, d, true)
	if e != nil {
		return e
	}
	return jsonResponse(w, 201, d)
}
func (a *API) title(w http.ResponseWriter, r *http.Request) error {
	var d domain.Draft
	if e := body(r, &d); e != nil {
		return e
	}
	p, e := a.Store.Project(r.PathValue("id"))
	if e != nil {
		return e
	}
	if e = d.Validate(p.Duration); e != nil {
		return bad(e.Error())
	}
	width, height := media.OutputDimensions(media.Info{Width: p.Width, Height: p.Height}, d.Aspect)
	if width < 2 || height < 2 {
		return bad("Source dimensions unavailable")
	}
	b, e := a.Media.TitlePNG(d, width, height)
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "image/png")
	_, e = w.Write(b)
	return e
}
func (a *API) export(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Revision int `json:"revision"`
	}
	if e := body(r, &b); e != nil {
		return e
	}
	t, e := a.Store.QueueExport(r.PathValue("id"), r.PathValue("draftId"), b.Revision)
	if e != nil {
		return e
	}
	return jsonResponse(w, 202, t)
}
func (a *API) video(w http.ResponseWriter, r *http.Request) error {
	t, e := a.Store.Task(r.PathValue("taskId"))
	if e != nil {
		return e
	}
	if t.ProjectID != r.PathValue("id") || t.Kind != "export" || t.Status != "completed" {
		return store.ErrNotFound
	}
	dir, e := a.Store.ProjectDir(t.ProjectID)
	if e != nil {
		return e
	}
	path := filepath.Join(dir, "exports", t.ID, "output.mp4")
	if _, e = os.Stat(path); e != nil {
		return e
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="autoclip-%s.mp4"`, t.ID))
	}
	http.ServeFile(w, r, path)
	return nil
}
func (a *API) task(w http.ResponseWriter, r *http.Request) error {
	t, e := a.Store.Task(r.PathValue("id"))
	if e != nil {
		return e
	}
	return jsonResponse(w, 200, t)
}
func (a *API) events(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, e := a.Store.Task(id); e != nil {
		return e
	}
	f, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		t, e := a.Store.Task(id)
		if e != nil {
			slog.Error("SSE task load", "error", e)
			return nil
		}
		b, e := json.Marshal(t)
		if e != nil {
			return e
		}
		if string(b) != previous {
			if _, e = fmt.Fprintf(w, "id: %s\nevent: snapshot\ndata: %s\n\n", t.UpdatedAt, b); e != nil {
				slog.Debug("SSE disconnected", "error", e)
				return nil
			}
			f.Flush()
			previous = string(b)
		}
		if t.Terminal() {
			return nil
		}
		select {
		case <-r.Context().Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (a *API) modelStatus(kind string) (domain.ModelStatus, error) {
	m, e := a.Store.Model(kind)
	if errors.Is(e, store.ErrNotFound) {
		return domain.ModelStatus{}, nil
	}
	if e != nil {
		return domain.ModelStatus{}, e
	}
	capability := m.Capability
	if capability == "" && kind == "vision" {
		// Existing separate visual configurations predate explicit capability
		// metadata and are intentionally kept compatible.
		capability = "multimodal"
	}
	return domain.ModelStatus{BaseURL: m.BaseURL, Model: m.Model, Configured: m.BaseURL != "" && m.Model != "", Capability: capability}, nil
}
func (a *API) settings(w http.ResponseWriter, r *http.Request) error {
	text, e := a.modelStatus("text")
	if e != nil {
		return e
	}
	vision, e := a.modelStatus("vision")
	if e != nil {
		return e
	}
	cookies, e := a.Store.Secret("cookies")
	if e != nil && !errors.Is(e, store.ErrNotFound) {
		return e
	}
	return jsonResponse(w, 200, map[string]any{"text": text, "vision": vision, "cookies_configured": len(cookies) > 0})
}
func (a *API) saveSettings(w http.ResponseWriter, r *http.Request) error {
	kind := r.PathValue("kind")
	if kind != "text" && kind != "vision" {
		return store.ErrNotFound
	}
	var m domain.ModelSettings
	if e := body(r, &m); e != nil {
		return e
	}
	m.BaseURL = strings.TrimRight(strings.TrimSpace(m.BaseURL), "/")
	m.Model = strings.TrimSpace(m.Model)
	m.APIKey = strings.TrimSpace(m.APIKey)
	m.Capability = strings.TrimSpace(m.Capability)
	if m.Capability == "" {
		if kind == "vision" {
			m.Capability = "multimodal"
		} else {
			m.Capability = "text"
		}
	}
	if m.Capability != "text" && m.Capability != "multimodal" {
		return bad("Model capability must be text or multimodal")
	}
	if m.APIKey == "" {
		old, e := a.Store.Model(kind)
		if e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
		if old.APIKey != "" {
			oldURL, oldErr := url.Parse(old.BaseURL)
			newURL, newErr := url.Parse(m.BaseURL)
			if oldErr != nil || newErr != nil || oldURL.Scheme != newURL.Scheme || !strings.EqualFold(oldURL.Host, newURL.Host) {
				return bad("Changing model server requires entering a key explicitly; stored keys are never forwarded to another origin")
			}
		}
		m.APIKey = old.APIKey
	}
	if e := ai.ValidateSettings(m); e != nil {
		return bad(e.Error())
	}
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	if e = a.Store.PutSecret(kind, b); e != nil {
		return e
	}
	out, e := a.modelStatus(kind)
	if e != nil {
		return e
	}
	return jsonResponse(w, 200, out)
}
func (a *API) testSettings(w http.ResponseWriter, r *http.Request) error {
	kind := r.PathValue("kind")
	if kind != "text" && kind != "vision" {
		return store.ErrNotFound
	}
	m, e := a.Store.Model(kind)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if e = ai.New(m).Test(ctx, kind == "vision"); e != nil {
		return e
	}
	return jsonResponse(w, 200, map[string]any{"ok": true, "message": kind + " model request succeeded"})
}
func (a *API) cookies(w http.ResponseWriter, r *http.Request) error {
	b, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil {
		return e
	}
	if len(b) > 1<<20 || !strings.Contains(string(b), "Netscape HTTP Cookie File") {
		return bad("Upload a Netscape cookies.txt file (at most 1 MiB)")
	}
	if e = a.Store.PutSecret("cookies", b); e != nil {
		return e
	}
	return jsonResponse(w, 200, map[string]bool{"cookies_configured": true})
}
