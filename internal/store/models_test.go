package store

import (
	"testing"

	"autoclip-go/internal/domain"
)

func TestPutModelsAtomicRollback(t *testing.T) {
	s, err := Open(t.TempDir())
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
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_vision BEFORE INSERT ON secrets
WHEN NEW.name='vision' BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	next := domain.ModelSettings{BaseURL: "https://new.example/v1", Model: "new", APIKey: "new-secret"}
	if err := s.PutModels(map[string]domain.ModelSettings{"text": next, "vision": next}); err == nil {
		t.Fatal("database write failure swallowed")
	}
	got, err := s.Model("text")
	if err != nil || got != old {
		t.Fatal("failed batch changed saved text settings")
	}
	if err := s.PutModels(map[string]domain.ModelSettings{"cookies": next}); err == nil {
		t.Fatal("non-model secret accepted")
	}
}
