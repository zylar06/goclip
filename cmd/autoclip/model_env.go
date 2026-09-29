package main

import (
	"fmt"
	"strings"

	"autoclip-go/internal/ai"
	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
)

// Only web imports startup settings; workers read the shared encrypted store.
// All groups are validated before any write. Empty groups preserve saved values;
// configured groups replace the entire tuple, never borrowing a saved API key.
func applyModelEnvironment(s *store.Store, mode string, getenv func(string) string) error {
	if mode != "web" {
		return nil
	}
	models := make(map[string]domain.ModelSettings)
	for _, kind := range []string{"text", "vision"} {
		prefix := "AUTOCLIP_" + strings.ToUpper(kind) + "_"
		base, model, key := getenv(prefix+"BASE_URL"), getenv(prefix+"MODEL"), getenv(prefix+"API_KEY")
		if base == "" && model == "" && key == "" {
			continue
		}
		m := domain.ModelSettings{
			BaseURL: strings.TrimRight(strings.TrimSpace(base), "/"),
			Model:   strings.TrimSpace(model),
			APIKey:  strings.TrimSpace(key),
		}
		if m.BaseURL == "" || m.Model == "" {
			return fmt.Errorf("%sBASE_URL and %sMODEL are both required when configuring this environment group", prefix, prefix)
		}
		if err := ai.ValidateSettings(m); err != nil {
			// Never include input, provider response or secret in startup errors.
			return fmt.Errorf("invalid %s model environment: check base URL, model identifier and API key format", kind)
		}
		models[kind] = m
	}
	return s.PutModels(models)
}
