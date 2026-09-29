package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
)

func TestModelEnvironment(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	old := domain.ModelSettings{BaseURL: "https://old.example/v1", Model: "old", APIKey: "old-secret"}
	if err := s.PutModels(map[string]domain.ModelSettings{"text": old}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	check := func(kind string, want domain.ModelSettings) {
		t.Helper()
		got, err := s.Model(kind)
		if err != nil || got != want {
			t.Fatalf("%s model mismatch (values redacted): %v", kind, err)
		}
	}
	if err := applyModelEnvironment(s, "web", getenv); err != nil {
		t.Fatal(err)
	}
	check("text", old)
	env["AUTOCLIP_TEXT_API_KEY"] = "sensitive-canary"
	if err := applyModelEnvironment(s, "web", getenv); err == nil || strings.Contains(err.Error(), "sensitive-canary") {
		t.Fatal("partial settings must fail without leaking input")
	}
	check("text", old)
	env["AUTOCLIP_TEXT_BASE_URL"] = " https://text.example/v1/ "
	env["AUTOCLIP_TEXT_MODEL"] = " text-model "
	env["AUTOCLIP_VISION_BASE_URL"] = "https://vision.example/v1"
	// Invalid second group cannot partially change the first.
	if err := applyModelEnvironment(s, "web", getenv); err == nil {
		t.Fatal("incomplete vision group accepted")
	}
	check("text", old)
	env["AUTOCLIP_VISION_MODEL"] = "vision-model"
	env["AUTOCLIP_VISION_API_KEY"] = "vision-secret"
	if err := applyModelEnvironment(s, "worker", getenv); err != nil {
		t.Fatal(err)
	}
	check("text", old)
	if err := applyModelEnvironment(s, "web", getenv); err != nil {
		t.Fatal(err)
	}
	text := domain.ModelSettings{BaseURL: "https://text.example/v1", Model: "text-model", APIKey: "sensitive-canary"}
	check("text", text)
	check("vision", domain.ModelSettings{BaseURL: "https://vision.example/v1", Model: "vision-model", APIKey: "vision-secret"})
	var encrypted []byte
	if err := s.DB.QueryRow("SELECT ciphertext FROM secrets WHERE name='text'").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("sensitive-canary")) {
		t.Fatal("environment key stored as plaintext")
	}
	// Changing endpoint with an empty env key never forwards the old key.
	env["AUTOCLIP_TEXT_BASE_URL"] = "http://127.0.0.1:9999/v1"
	env["AUTOCLIP_TEXT_API_KEY"] = ""
	if err := applyModelEnvironment(s, "web", getenv); err != nil {
		t.Fatal(err)
	}
	text.BaseURL, text.APIKey = "http://127.0.0.1:9999/v1", ""
	check("text", text)
	// An ordinary UI/database edit is reapplied from env on the next web start.
	if err := s.PutModels(map[string]domain.ModelSettings{"text": old}); err != nil {
		t.Fatal(err)
	}
	if err := applyModelEnvironment(s, "web", getenv); err != nil {
		t.Fatal(err)
	}
	check("text", text)
	env = map[string]string{}
	if err := applyModelEnvironment(s, "web", getenv); err != nil {
		t.Fatal(err)
	}
	check("text", text)
	for _, input := range []map[string]string{
		{"AUTOCLIP_TEXT_BASE_URL": "https://secret-user:secret-pass@example.com/v1", "AUTOCLIP_TEXT_MODEL": "model"},
		{"AUTOCLIP_TEXT_BASE_URL": "https://example.com/v1/chat/completions", "AUTOCLIP_TEXT_MODEL": "model"},
		{"AUTOCLIP_TEXT_BASE_URL": "https://example.com/v1", "AUTOCLIP_TEXT_MODEL": "model", "AUTOCLIP_TEXT_API_KEY": "bad\nsecret"},
	} {
		env = input
		err := applyModelEnvironment(s, "web", getenv)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "https://") {
			t.Fatal("invalid settings accepted or input leaked")
		}
		check("text", text)
	}
	if _, err := s.Secret("cookies"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("model configuration touched cookies")
	}
}
