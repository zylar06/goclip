package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"autoclip-go/internal/domain"
)

func TestEmbeddedEntitiesAndNullableFields(t *testing.T) {
	c := shape(reflect.TypeOf(domain.Candidate{}))
	props := c["properties"].(object)
	for _, key := range []string{"id", "start", "end", "evidence", "score", "kind"} {
		if props[key] == nil {
			t.Fatal("missing embedded property", key)
		}
	}
	d := shape(reflect.TypeOf(domain.Draft{}))
	if d["properties"].(object)["title_accent"].(object)["anyOf"] == nil {
		t.Fatal("nullable field lost")
	}
	if slices.Contains(d["required"].([]string), "parent_revision") {
		t.Fatal("optional field required")
	}
}
func TestGeneratedDocumentContainsActualPublicOperations(t *testing.T) {
	t.Chdir(t.TempDir())
	main()
	b, e := os.ReadFile(filepath.Join("api", "openapi.json"))
	if e != nil {
		t.Fatal(e)
	}
	var spec map[string]any
	if e = json.Unmarshal(b, &spec); e != nil {
		t.Fatal(e)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatal("invalid version")
	}
	paths := spec["paths"].(map[string]any)
	for _, p := range []string{"/projects", "/projects/{id}/analyze", "/projects/{id}/title-preview", "/projects/{id}/drafts/{draftId}/export", "/tasks/{id}/events", "/settings/{kind}/test"} {
		if paths[p] == nil {
			t.Fatal("operation missing", p)
		}
	}
	event := paths["/tasks/{id}/events"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)
	if event["content"].(map[string]any)["text/event-stream"] == nil {
		t.Fatal("SSE response not documented")
	}
	duplicate := paths["/projects/{id}/drafts/{draftId}/duplicate"].(map[string]any)["post"].(map[string]any)["responses"].(map[string]any)
	if duplicate["201"] == nil {
		t.Fatal("duplicate response must match API's 201 Created")
	}
	create := paths["/projects"].(map[string]any)["post"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)
	for _, contentType := range []string{"application/json", "multipart/form-data"} {
		s := create[contentType].(map[string]any)["schema"].(map[string]any)
		props := s["properties"].(map[string]any)
		if props["url"] == nil || props["instruction"] == nil {
			t.Fatal("URL and instructions missing from upload contract", contentType)
		}
		if contentType == "multipart/form-data" && len(s["oneOf"].([]any)) != 2 {
			t.Fatal("upload must choose exactly one URL or video")
		}
	}
	thumb := paths["/projects/{id}/drafts/{draftId}/thumbnail"].(map[string]any)["get"].(map[string]any)
	found := false
	for _, p := range thumb["parameters"].([]any) {
		param := p.(map[string]any)
		if param["name"] == "revision" && param["in"] == "query" && param["required"] == true {
			found = true
		}
	}
	if !found {
		t.Fatal("draft thumbnails require revision")
	}
}
