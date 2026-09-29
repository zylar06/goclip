package store

import (
	"encoding/json"
	"errors"

	"autoclip-go/internal/domain"
)

// PutModels atomically replaces complete, caller-validated model settings.
// It deliberately does not merge/preserve keys from a different configuration.
func (s *Store) PutModels(models map[string]domain.ModelSettings) error {
	if len(models) == 0 {
		return nil
	}
	encrypted := make(map[string][]byte, len(models))
	for kind, model := range models {
		if kind != "text" && kind != "vision" {
			return errors.New("unsupported model setting kind")
		}
		b, err := json.Marshal(model)
		if err != nil {
			return err
		}
		b, err = s.vault.Encrypt(b)
		if err != nil {
			return err
		}
		encrypted[kind] = b
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, kind := range []string{"text", "vision"} {
		if b, ok := encrypted[kind]; ok {
			if _, err = tx.Exec("INSERT INTO secrets VALUES(?,?) ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext", kind, b); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
