package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"autoclip-go/internal/domain"
)

func readyProductionAPI(t *testing.T) (*API, string) {
	t.Helper()
	a, _ := testAPI(t)
	audio := true
	p := domain.Project{ID: domain.ID(), Name: "已有字幕", Duration: 90, HasAudio: &audio, SubtitleStatus: "available"}
	job, err := a.Store.CreateProject(p, domain.ImportPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Finish(job.ID, "completed", "", false); err != nil {
		t.Fatal(err)
	}
	return a, p.ID
}

func TestPlanRequiresExplicitConfirmationAndPreservesSafeSmartDefault(t *testing.T) {
	a, pid := readyProductionAPI(t)
	h := a.Handler()
	path := "/api/v1/projects/" + pid
	got := request(h, "GET", path+"/plan", "")
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var plan domain.ProductionPlan
	if err := json.Unmarshal(got.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Options.Mode != "fused" || plan.Options.AllowVisual || plan.Options.BurnSubtitles || plan.Options.Confirmed || plan.Options.Duration != 0 {
		t.Fatal(plan)
	}
	tasks, _ := a.Store.Tasks(pid)
	if len(tasks) != 1 {
		t.Fatal("GET launched work")
	}
	got = request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":false}`)
	if got.Code != 400 {
		t.Fatal(got.Code)
	}
	got = request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
	if got.Code != 400 {
		t.Fatal("missing model accepted", got.Code)
	}
	if err := a.Store.PutModels(map[string]domain.ModelSettings{"text": {BaseURL: "https://example.com/v1", Model: "test"}, "vision": {BaseURL: "https://example.com/v1", Model: "vision", Capability: "multimodal"}}); err != nil {
		t.Fatal(err)
	}
	got = request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	var first domain.Workflow
	if err := json.Unmarshal(got.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	got = request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
	var second domain.Workflow
	if err := json.Unmarshal(got.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID != second.ID {
		t.Fatal("confirmation was not idempotent", first, second)
	}
	tasks, _ = a.Store.Tasks(pid)
	if len(tasks) != 2 {
		t.Fatal("duplicate paid task", len(tasks))
	}
}

func TestVisualPlanSavingIsNotImageConsent(t *testing.T) {
	a, pid := readyProductionAPI(t)
	h := a.Handler()
	path := "/api/v1/projects/" + pid
	update := `{"revision":1,"auto_export":false,"options":{"mode":"visual","allow_visual":false,"confirmed":true,"goals":["highlight"],"duration":0,"aspect":"original","instruction":""}}`
	got := request(h, "PUT", path+"/plan", update)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	var plan domain.ProductionPlan
	if err := json.Unmarshal(got.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Options.Confirmed {
		t.Fatal("save granted consent")
	}
	got = request(h, "POST", path+"/confirm", `{"plan_revision":2,"confirmed":true}`)
	if got.Code != 400 {
		t.Fatal("images sent without consent")
	}
	got = request(h, "POST", path+"/inspect", `{"allow_visual":true,"confirmed":false}`)
	if got.Code != 400 {
		t.Fatal("visual screening has no consent gate")
	}
}

func TestBulkSubtitleChangeRequiresConsentAndPreservesOldExport(t *testing.T) {
	a, pid := readyProductionAPI(t)
	h := a.Handler()
	path := "/api/v1/projects/" + pid
	d := domain.NewDraft("old", []domain.Scene{{ID: domain.ID(), Start: 0, End: 30}})
	d.Subtitles = true
	if _, err := a.Store.SaveDraft(pid, d, true); err != nil {
		t.Fatal(err)
	}
	if got := request(h, "POST", path+"/drafts/disable-subtitles", `{"confirm":false}`); got.Code != 400 {
		t.Fatal(got.Code)
	}
	got := request(h, "POST", path+"/drafts/disable-subtitles", `{"confirm":true}`)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	updated, err := a.Store.Draft(pid, d.ID)
	if err != nil || updated.Subtitles || updated.Revision != 2 {
		t.Fatal(updated, err)
	}
}

func TestAcceptedConfirmationReplaysAfterPlanEditAndModelRemoval(t *testing.T) {
	a, pid := readyProductionAPI(t)
	if err := a.Store.PutModels(map[string]domain.ModelSettings{"text": {
		BaseURL: "http://127.0.0.1:1/v1", Model: "not-called",
	}, "vision": {BaseURL: "http://127.0.0.1:1/v1", Model: "vision", Capability: "multimodal"}}); err != nil {
		t.Fatal(err)
	}
	h, path := a.Handler(), "/api/v1/projects/"+pid
	firstResponse := request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
	if firstResponse.Code != 202 {
		t.Fatal(firstResponse.Code, firstResponse.Body.String())
	}
	var first domain.Workflow
	if err := json.Unmarshal(firstResponse.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	tasks, err := a.Store.Tasks(pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.WorkflowID == first.ID {
			if _, err := a.Store.Cancel(task.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	plan, err := a.Store.Plan(pid)
	if err != nil {
		t.Fatal(err)
	}
	plan.Options.Instruction = "A different future production"
	newPlan, err := a.Store.UpdatePlan(pid, domain.PlanUpdate{Revision: plan.Revision, Options: plan.Options})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store.DeleteSecret("text"); err != nil {
		t.Fatal(err)
	}
	accepted, err := a.Store.Workflow(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := a.Store.Tasks(pid)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		response := request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
		var replay domain.Workflow
		if response.Code != 202 {
			t.Fatal("accepted revision must replay before current revision/settings checks", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &replay); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(replay, accepted) {
			t.Fatalf("replay changed accepted workflow: %+v", replay)
		}
	}
	after, err := a.Store.Tasks(pid)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("replay changed or enqueued tasks: %v", err)
	}
	current, err := a.Store.Plan(pid)
	if err != nil || !reflect.DeepEqual(current, newPlan) {
		t.Fatalf("replay changed the newer unconfirmed plan: %+v %v", current, err)
	}
	flows, err := a.Store.Workflows(pid)
	if err != nil || len(flows) != 1 {
		t.Fatalf("duplicate workflow: %+v %v", flows, err)
	}
	if response := request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":false}`); response.Code != 400 {
		t.Fatal("replay must still require explicit confirmation", response.Code)
	}
	if response := request(h, "POST", path+"/confirm", `{"plan_revision":3,"confirmed":true}`); response.Code != 409 {
		t.Fatal("unaccepted wrong revision must conflict", response.Code)
	}
}

func TestLegacySubtitleEvidencePlanCanBeConfirmedWithoutEditing(t *testing.T) {
	a, _ := testAPI(t)
	p := domain.Project{ID: domain.ID(), Name: "legacy subtitles", Duration: 30}
	job, err := a.Store.CreateProject(p, domain.ImportPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.Finish(job.ID, "completed", "", false); err != nil {
		t.Fatal(err)
	}
	dir, err := a.Store.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cues := []byte(`[{"start":0,"end":30,"text":"Existing complete subtitle evidence."}]`)
	subtitles := filepath.Join(dir, "subtitles.json")
	if err := os.WriteFile(subtitles, cues, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.SetAsset(p.ID, "subtitles", "subtitles.json"); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.PutModels(map[string]domain.ModelSettings{"text": {
		BaseURL: "http://127.0.0.1:1/v1", Model: "not-called",
	}, "vision": {
		BaseURL: "http://127.0.0.1:1/v1", Model: "not-called", Capability: "multimodal",
	}}); err != nil {
		t.Fatal(err)
	}
	h, path := a.Handler(), "/api/v1/projects/"+p.ID
	response := request(h, "GET", path+"/plan", "")
	var plan domain.ProductionPlan
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Options.Goals, []string{"highlight"}) ||
		!reflect.DeepEqual(plan.SuggestedGoals, []string{"highlight"}) ||
		plan.Options.Confirmed || plan.Options.BurnSubtitles {
		t.Fatalf("valid old cues were not used as unconfirmed local evidence: %+v", plan)
	}
	tasks, err := a.Store.Tasks(p.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reading old evidence enqueued work: %+v %v", tasks, err)
	}
	response = request(h, "POST", path+"/confirm", `{"plan_revision":1,"confirmed":true}`)
	if response.Code != 202 {
		t.Fatal("the exact plan returned by GET must be confirmable without a PUT", response.Code, response.Body.String())
	}
	var flow domain.Workflow
	if err := json.Unmarshal(response.Body.Bytes(), &flow); err != nil {
		t.Fatal(err)
	}
	if flow.PlanRevision != plan.Revision || !flow.Options.Confirmed ||
		!reflect.DeepEqual(flow.Options.Goals, plan.Options.Goals) {
		t.Fatalf("confirmation lost evidence-based plan: %+v", flow)
	}
	after, err := os.ReadFile(subtitles)
	if err != nil || string(after) != string(cues) {
		t.Fatal("legacy transcript was modified", err)
	}
}

func TestLegacyAnalyzeReturnsTaskLinkedToPromoWorkflow(t *testing.T) {
	a, pid := readyProductionAPI(t)
	if err := a.Store.PutModels(map[string]domain.ModelSettings{"text": {
		BaseURL: "http://127.0.0.1:1/v1", Model: "not-called",
	}}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/projects/" + pid + "/analyze"
	response := request(a.Handler(), "POST", path, `{"mode":"subtitle","goals":["promo"],"confirmed":true,"aspect":"original","duration":0}`)
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	var task domain.Task
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Kind != "analyze" || task.Status != "queued" || task.WorkflowID == "" || task.ProjectID != pid {
		t.Fatalf("legacy Task response must link the production route: %+v", task)
	}
	flow, err := a.Store.Workflow(task.WorkflowID)
	if err != nil || !flow.Options.Confirmed || len(flow.Goals) != 1 || flow.Goals[0].Goal != "promo" {
		t.Fatalf("missing confirmed promo workflow: %+v %v", flow, err)
	}
	var payload domain.AnalysisOptions
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, flow.Options) {
		t.Fatalf("legacy task/workflow options disagree: %+v %+v", payload, flow.Options)
	}
	tasks, err := a.Store.Tasks(pid)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("legacy request queued more than one child: %+v %v", tasks, err)
	}
}
