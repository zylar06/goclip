package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"autoclip-go/internal/domain"
)

// Increment when prompts, stage schemas or deterministic quality rules change.
const pipelineVersion = "autoclip-ai-1"

type checkpoint struct {
	Version   string          `json:"version"`
	Stage     string          `json:"stage"`
	InputHash string          `json:"input_hash"`
	Previous  string          `json:"previous"`
	DataHash  string          `json:"data_hash"`
	UpdatedAt string          `json:"updated_at"`
	Data      json.RawMessage `json:"data"`
}

type stageRunner struct {
	dir      string
	input    string
	previous string
	progress domain.ProgressFunc
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newRunner(ctx context.Context, client *Client, input any, dir string, progress domain.ProgressFunc) (*stageRunner, error) {
	if err := client.ready(ctx); err != nil {
		return nil, err
	}
	data, err := json.Marshal(struct {
		Version  string `json:"version"`
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
		Input    any    `json:"input"`
	}{pipelineVersion, client.endpoint, client.settings.Model, input})
	if err != nil {
		return nil, invalid("Cannot fingerprint analysis input.")
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, invalid("Cannot create checkpoint directory; check local permissions.")
		}
	}
	return &stageRunner{dir: dir, input: digest(data), progress: progress}, nil
}

func (r *stageRunner) notify(ctx context.Context, stage string, percent float64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if r.progress != nil {
		if err := r.progress(stage, &percent); err != nil {
			return atStage(stage, err)
		}
	}
	return contextError(ctx)
}

func stage[T any](ctx context.Context, r *stageRunner, name string, before, after float64,
	produce func() (T, error), validate func(T) error) (T, error) {
	var value T
	if err := r.notify(ctx, name, before); err != nil {
		return value, atStage(name, err)
	}
	cached := false
	var data []byte
	if r.dir != "" {
		path := filepath.Join(r.dir, name+".json")
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > maxResponseBytes {
				return value, atStage(name, invalid("Checkpoint is not a bounded regular JSON file; use a fresh checkpoint directory."))
			}
			file, err := os.Open(path)
			if err != nil {
				return value, atStage(name, invalid("Cannot read checkpoint; check directory permissions."))
			}
			raw, readErr := io.ReadAll(io.LimitReader(file, maxResponseBytes+1))
			closeErr := file.Close()
			if readErr != nil || closeErr != nil || len(raw) > maxResponseBytes {
				return value, atStage(name, invalid("Cannot read bounded checkpoint JSON."))
			}
			var cp checkpoint
			if err := decodeJSON(string(raw), &cp); err != nil {
				return value, atStage(name, invalid("Checkpoint JSON is corrupt; use a fresh directory for an explicit retry."))
			}
			if cp.Version != pipelineVersion || cp.Stage != name || cp.InputHash != r.input ||
				cp.Previous != r.previous || cp.DataHash != digest(cp.Data) || cp.UpdatedAt == "" {
				return value, atStage(name, invalid("Checkpoint is stale or corrupt; use a fresh directory for changed input, settings or options."))
			}
			if err := decodeJSON(string(cp.Data), &value); err != nil {
				return value, atStage(name, invalid("Checkpoint stage schema is invalid; no paid request was made."))
			}
			cached, data = true, cp.Data
		} else if !errors.Is(err, os.ErrNotExist) {
			return value, atStage(name, invalid("Cannot inspect checkpoint; check directory permissions."))
		}
	}
	if !cached {
		var err error
		value, err = produce()
		if err != nil {
			return value, atStage(name, err)
		}
	}
	if err := contextError(ctx); err != nil {
		return value, atStage(name, err)
	}
	if err := validate(value); err != nil {
		return value, atStage(name, err)
	}
	if !cached {
		var err error
		data, err = json.Marshal(value)
		if err != nil {
			return value, atStage(name, invalid("Cannot encode verified stage JSON."))
		}
		if r.dir != "" {
			cp := checkpoint{pipelineVersion, name, r.input, r.previous, digest(data), domain.Now(), data}
			raw, err := json.Marshal(cp)
			if err != nil || len(raw) > maxResponseBytes {
				return value, atStage(name, invalid("Verified stage exceeds checkpoint size budget."))
			}
			if err := writeCheckpoint(ctx, filepath.Join(r.dir, name+".json"), raw); err != nil {
				return value, atStage(name, err)
			}
		}
	}
	r.previous = digest([]byte(r.previous + ":" + name + ":" + digest(data)))
	if err := r.notify(ctx, name, after); err != nil {
		return value, atStage(name, err)
	}
	return value, nil
}

func writeCheckpoint(ctx context.Context, path string, data []byte) (result error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".ai-stage-*.tmp")
	if err != nil {
		return invalid("Cannot create checkpoint temporary file.")
	}
	closed, committed := false, false
	defer func() {
		if !closed {
			if err := f.Close(); err != nil {
				result = errors.Join(result, invalid("Cannot close checkpoint temporary file."))
			}
		}
		if !committed {
			if err := os.Remove(f.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, invalid("Cannot clean up checkpoint temporary file."))
			}
		}
	}()
	if _, err := f.Write(data); err != nil {
		return invalid("Cannot write checkpoint.")
	}
	if err := f.Sync(); err != nil {
		return invalid("Cannot sync checkpoint.")
	}
	err = f.Close()
	closed = true
	if err != nil {
		return invalid("Cannot close checkpoint.")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return invalid("Cannot publish checkpoint with a same-directory rename.")
	}
	committed = true
	return nil
}
