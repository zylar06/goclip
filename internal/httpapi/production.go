package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
)

func validateProductionOptions(o domain.AnalysisOptions) error {
	if o.Mode != "subtitle" && o.Mode != "auto" && o.Mode != "visual" {
		return bad("Choose subtitle, smart or visual analysis")
	}
	if o.Duration != 0 && (o.Duration < 10 || o.Duration > 120) {
		return bad("Choose automatic duration or an expected duration between 10 and 120 seconds")
	}
	if !slices.Contains([]string{"original", "portrait", "landscape"}, o.Aspect) || len(o.Instruction) > 4000 ||
		(o.Category != "" && !slices.Contains(ai.Categories, o.Category)) {
		return bad("Invalid production options")
	}
	if len(o.Goals) < 1 || len(o.Goals) > 3 {
		return bad("Choose 1–3 production goals")
	}
	seen := map[string]bool{}
	for _, g := range o.Goals {
		if seen[g] || !slices.Contains([]string{"content", "highlight", "promo"}, g) {
			return bad("Invalid or duplicate production goal")
		}
		seen[g] = true
	}
	if o.Mode == "visual" && len(o.Goals) == 1 && o.Goals[0] == "content" {
		return bad("Content clips require subtitle analysis")
	}
	return nil
}
func (a *API) getPlan(w http.ResponseWriter, r *http.Request) error {
	p, err := a.Store.Plan(r.PathValue("id"))
	if err != nil {
		return err
	}
	return jsonResponse(w, 200, p)
}
func (a *API) updatePlan(w http.ResponseWriter, r *http.Request) error {
	var u domain.PlanUpdate
	if err := body(r, &u); err != nil {
		return err
	}
	if err := validateProductionOptions(u.Options); err != nil {
		return err
	}
	p, err := a.Store.UpdatePlan(r.PathValue("id"), u)
	if err != nil {
		return err
	}
	return jsonResponse(w, 200, p)
}
func (a *API) confirmProduction(w http.ResponseWriter, r *http.Request) error {
	var c domain.ConfirmProduction
	if err := body(r, &c); err != nil {
		return err
	}
	if !c.Confirmed || c.PlanRevision < 1 {
		return bad("Explicit production confirmation and plan revision are required")
	}
	p, err := a.Store.Plan(r.PathValue("id"))
	if err != nil {
		return err
	}
	// Return an accepted confirmation even if provider settings changed afterward.
	flows, err := a.Store.Workflows(r.PathValue("id"))
	if err != nil {
		return err
	}
	for _, f := range flows {
		if f.PlanRevision == c.PlanRevision {
			return jsonResponse(w, 202, f)
		}
	}
	if p.Revision != c.PlanRevision {
		return store.ErrConflict
	}
	if err = validateProductionOptions(p.Options); err != nil {
		return err
	}
	if err = a.validateProductionModels(p.Options); err != nil {
		return err
	}
	f, err := a.Store.ConfirmProduction(r.PathValue("id"), c.PlanRevision)
	if err != nil {
		return err
	}
	return jsonResponse(w, 202, f)
}

func (a *API) validateProductionModels(options domain.AnalysisOptions) error {
	if options.Mode == "visual" && !options.AllowVisual {
		return bad("Explicit permission to send sampled images is required")
	}
	kinds := []string{"text"}
	if options.Mode == "visual" {
		kinds = []string{"vision"}
		if slices.Contains(options.Goals, "content") || slices.Contains(options.Goals, "promo") {
			kinds = append(kinds, "text")
		}
	}
	if options.Mode == "auto" && options.AllowVisual {
		kinds = append(kinds, "vision")
	}
	for _, kind := range kinds {
		m, err := a.Store.Model(kind)
		if errors.Is(err, store.ErrNotFound) {
			return bad("Configure the " + kind + " model before confirming")
		}
		if err != nil {
			return err
		}
		if m.BaseURL == "" || m.Model == "" {
			return bad("Configure the " + kind + " model before confirming")
		}
		if kind == "vision" && m.Capability != "" && m.Capability != "multimodal" {
			return bad("Configure a multimodal vision model before confirming")
		}
	}
	return nil
}
func (a *API) inspect(w http.ResponseWriter, r *http.Request) error {
	var o domain.InspectOptions
	if err := body(r, &o); err != nil {
		return err
	}
	p, err := a.Store.Project(r.PathValue("id"))
	if err != nil {
		return err
	}
	if p.Duration <= 0 {
		return store.ErrConflict
	}
	if o.AllowVisual && !o.Confirmed {
		return bad("Confirm image transmission and possible charges before visual screening")
	}
	if o.AllowVisual {
		if _, err = a.Store.Model("vision"); err != nil {
			return bad("Configure a vision model first")
		}
	}
	t, err := a.Store.Queue(p.ID, "inspect", o)
	if err != nil {
		return err
	}
	return jsonResponse(w, 202, t)
}
func (a *API) disableSubtitles(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Confirm bool `json:"confirm"`
	}
	if err := body(r, &b); err != nil {
		return err
	}
	if !b.Confirm {
		return bad("Confirm disabling added subtitles on all project drafts")
	}
	n, err := a.Store.DisableSubtitles(r.PathValue("id"))
	if err != nil {
		return err
	}
	return jsonResponse(w, 200, map[string]int{"updated": n})
}
func (a *API) thumbnail(w http.ResponseWriter, r *http.Request) error {
	pid := r.PathValue("id")
	source, err := a.Store.Asset(pid, "source")
	if err != nil {
		return err
	}
	at := 0.0
	if id := r.PathValue("draftId"); id != "" {
		d, err := a.Store.Draft(pid, id)
		if err != nil {
			return err
		}
		rev, err := strconv.Atoi(r.URL.Query().Get("revision"))
		if err != nil || rev != d.Revision {
			return store.ErrConflict
		}
		if len(d.Scenes) == 0 {
			return store.ErrConflict
		}
		at = d.Scenes[0].Start
	}
	data, err := a.Media.Thumbnail(r.Context(), source, at)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, err = w.Write(data)
	return err
}
func (a *API) sourcePreview(pid string) (domain.PreviewStatus, error) {
	tasks, err := a.Store.Tasks(pid)
	if err != nil {
		return domain.PreviewStatus{}, err
	}
	for _, t := range tasks {
		if t.Kind == "preview" {
			return domain.PreviewStatus{Status: t.Status, Task: &t}, nil
		}
	}
	return domain.PreviewStatus{Status: "missing"}, nil
}
func (a *API) previewStatus(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.Store.Project(r.PathValue("id")); err != nil {
		return err
	}
	s, err := a.sourcePreview(r.PathValue("id"))
	if err != nil {
		return err
	}
	return jsonResponse(w, 200, s)
}
func (a *API) startPreview(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := body(r, &b); err != nil {
		return err
	}
	if !b.Confirmed {
		return bad("Confirm local compatible-preview generation")
	}
	pid := r.PathValue("id")
	if _, err := a.Store.Asset(pid, "source"); err != nil {
		return err
	}
	s, err := a.sourcePreview(pid)
	if err != nil {
		return err
	}
	if s.Task != nil && (s.Status == "completed" || !s.Task.Terminal()) {
		return jsonResponse(w, 202, s.Task)
	}
	t, err := a.Store.Queue(pid, "preview", nil)
	if err != nil {
		return err
	}
	return jsonResponse(w, 202, t)
}
func (a *API) previewVideo(w http.ResponseWriter, r *http.Request) error {
	pid := r.PathValue("id")
	s, err := a.sourcePreview(pid)
	if err != nil {
		return err
	}
	if s.Status != "completed" {
		return store.ErrNotFound
	}
	path, err := a.Store.Asset(pid, "preview")
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, path)
	return nil
}
