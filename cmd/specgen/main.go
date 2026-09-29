// specgen produces the web contract from shared Go entities. Run: go run ./cmd/specgen
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"autoclip-go/internal/domain"
)

type object = map[string]any

var models = map[string]reflect.Type{}

func schema(t reflect.Type) object {
	if t.Kind() == reflect.Pointer {
		s := schema(t.Elem())
		return object{"anyOf": []any{s, object{"type": "null"}}}
	}
	if name := t.Name(); models[name] == t && name != "" {
		return object{"$ref": "#/components/schemas/" + name}
	}
	return shape(t)
}
func shape(t reflect.Type) object {
	if t == reflect.TypeOf(json.RawMessage{}) {
		return object{}
	}
	switch t.Kind() {
	case reflect.String:
		return object{"type": "string"}
	case reflect.Bool:
		return object{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return object{"type": "integer"}
	case reflect.Float64, reflect.Float32:
		return object{"type": "number"}
	case reflect.Slice:
		return object{"type": "array", "items": schema(t.Elem())}
	case reflect.Struct:
		props := object{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous {
				nested := shape(f.Type)
				for k, v := range nested["properties"].(object) {
					props[k] = v
				}
				required = append(required, nested["required"].([]string)...)
				continue
			}
			parts := strings.Split(f.Tag.Get("json"), ",")
			name := parts[0]
			if name == "" || name == "-" {
				continue
			}
			props[name] = schema(f.Type)
			if len(parts) == 1 {
				required = append(required, name)
			}
		}
		return object{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	default:
		return object{}
	}
}
func ref(name string) object { return object{"$ref": "#/components/schemas/" + name} }
func response(s object, code string) object {
	return object{code: object{"description": "Success", "content": object{"application/json": object{"schema": s}}}, "default": object{"description": "Explicit error", "content": object{"application/json": object{"schema": ref("Error")}}}}
}
func main() {
	for _, v := range []any{domain.Project{}, domain.Task{}, domain.Draft{}, domain.Scene{}, domain.Candidate{}, domain.Cue{}, domain.Export{}, domain.ModelSettings{}, domain.ModelStatus{}, domain.AnalysisOptions{}} {
		t := reflect.TypeOf(v)
		models[t.Name()] = t
	}
	schemas := object{}
	for name, t := range models {
		schemas[name] = shape(t)
	}
	schemas["Error"] = object{"type": "object", "properties": object{"code": object{"type": "string"}, "message": object{"type": "string"}, "retryable": object{"type": "boolean"}, "request_id": object{"type": "string"}}, "required": []string{"code", "message", "retryable", "request_id"}}
	schemas["Workspace"] = object{"type": "object", "properties": object{"project": ref("Project"), "drafts": object{"type": "array", "items": ref("Draft")}, "tasks": object{"type": "array", "items": ref("Task")}, "candidates": object{"type": "array", "items": ref("Candidate")}, "exports": object{"type": "array", "items": ref("Export")}}, "required": []string{"project", "drafts", "tasks", "candidates", "exports"}}
	paths := object{}
	add := func(method, path, name, out, status string, in object) {
		res := ref(out)
		if strings.HasSuffix(out, "[]") {
			res = object{"type": "array", "items": ref(strings.TrimSuffix(out, "[]"))}
		}
		if out == "" {
			res = object{"type": "object"}
		}
		op := object{"operationId": name, "responses": response(res, status)}
		params := []any{}
		for _, s := range strings.Split(path, "/") {
			if strings.HasPrefix(s, "{") {
				params = append(params, object{"name": strings.Trim(s, "{}"), "in": "path", "required": true, "schema": object{"type": "string"}})
			}
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if in != nil {
			op["requestBody"] = object{"required": true, "content": object{"application/json": object{"schema": in}}}
		}
		if paths[path] == nil {
			paths[path] = object{}
		}
		paths[path].(object)[method] = op
	}
	add("get", "/health", "health", "", "200", nil)
	add("get", "/version", "version", "", "200", nil)
	add("get", "/projects", "listProjects", "Project[]", "200", nil)
	add("post", "/projects", "createProject", "Project", "201", object{"type": "object", "properties": object{"name": object{"type": "string"}, "url": object{"type": "string"}}, "required": []string{"url"}})
	paths["/projects"].(object)["post"].(object)["requestBody"].(object)["content"].(object)["multipart/form-data"] = object{"schema": object{"type": "object", "properties": object{"name": object{"type": "string"}, "video": object{"type": "string", "format": "binary"}, "subtitle": object{"type": "string", "format": "binary"}}, "required": []string{"video"}}}
	add("get", "/projects/{id}", "getWorkspace", "Workspace", "200", nil)
	add("delete", "/projects/{id}", "deleteProject", "", "200", object{"type": "object", "properties": object{"confirm": object{"type": "boolean", "const": true}}, "required": []string{"confirm"}})
	add("post", "/projects/{id}/analyze", "analyze", "Task", "202", ref("AnalysisOptions"))
	add("get", "/projects/{id}/subtitles", "getSubtitles", "Cue[]", "200", nil)
	add("post", "/projects/{id}/drafts", "createDraft", "Draft", "201", ref("Draft"))
	add("put", "/projects/{id}/drafts/{draftId}", "saveDraft", "Draft", "200", ref("Draft"))
	add("post", "/projects/{id}/drafts/{draftId}/duplicate", "duplicateDraft", "Draft", "201", object{"type": "object", "properties": object{"title": object{"type": "string"}, "language": object{"type": "string"}}, "required": []string{"title", "language"}})
	add("post", "/projects/{id}/drafts/{draftId}/export", "exportDraft", "Task", "202", object{"type": "object", "properties": object{"revision": object{"type": "integer", "minimum": 1}}, "required": []string{"revision"}})
	add("post", "/projects/{id}/rewrite", "rewriteDraft", "Draft", "200", object{"type": "object", "properties": object{"draft": ref("Draft"), "instruction": object{"type": "string"}}, "required": []string{"draft", "instruction"}})
	add("get", "/tasks/{id}", "getTask", "Task", "200", nil)
	add("post", "/tasks/{id}/cancel", "cancelTask", "Task", "200", nil)
	add("post", "/tasks/{id}/retry", "retryTask", "Task", "200", nil)
	add("get", "/settings", "getSettings", "", "200", nil)
	add("put", "/settings/{kind}", "saveModel", "ModelStatus", "200", ref("ModelSettings"))
	add("post", "/settings/{kind}/test", "testModel", "", "200", nil)
	add("put", "/settings/cookies", "saveCookies", "", "200", nil)
	paths["/settings/cookies"].(object)["put"].(object)["requestBody"] = object{"required": true, "content": object{"text/plain": object{"schema": object{"type": "string", "maxLength": 1048576}}}}
	add("delete", "/settings/cookies", "deleteCookies", "", "200", nil)
	for _, r := range []struct{ method, path, name, content string }{
		{"get", "/projects/{id}/source", "source", "video/mp4"},
		{"get", "/projects/{id}/exports/{taskId}/video", "exportVideo", "video/mp4"},
		{"post", "/projects/{id}/title-preview", "titlePreview", "image/png"},
		{"get", "/tasks/{id}/events", "taskEvents", "text/event-stream"},
	} {
		var in object
		if r.method == "post" {
			in = ref("Draft")
		}
		add(r.method, r.path, r.name, "", "200", in)
		op := paths[r.path].(object)[r.method].(object)
		op["responses"].(object)["200"] = object{"description": "Stream", "content": object{r.content: object{"schema": object{"type": "string", "format": "binary"}}}}
		if r.content == "video/mp4" {
			op["responses"].(object)["206"] = object{"description": "HTTP Range partial content"}
		}
	}
	data := object{"openapi": "3.1.0", "info": object{"title": "AutoClip Go", "version": "0.1.0"}, "servers": []object{{"url": "/api/v1"}}, "paths": paths, "components": object{"schemas": schemas}}
	b, e := json.MarshalIndent(data, "", "  ")
	if e != nil {
		panic(e)
	}
	if e = os.MkdirAll("api", 0755); e != nil {
		panic(e)
	}
	if e = os.WriteFile("api/openapi.json", append(b, '\n'), 0644); e != nil {
		panic(e)
	}
	fmt.Println("generated api/openapi.json")
}
