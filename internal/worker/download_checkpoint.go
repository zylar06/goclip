package worker

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"autoclip-go/internal/domain"
)

type downloadCheckpoint struct {
	TaskID   string `json:"task_id"`
	URL      string `json:"url"`
	Video    string `json:"video"`
	Subtitle string `json:"subtitle"`
}

func downloadCheckpointPath(dir, taskID string) string {
	return filepath.Join(dir, "import-"+taskID+".json")
}

// This is private preparation evidence, not a public source-ready asset.
// importSource re-probes media and validates cues before atomic publication.
func readDownloadCheckpoint(dir, taskID string, input domain.ImportPayload) (downloadCheckpoint, error) {
	var checkpoint downloadCheckpoint
	file, err := os.Open(downloadCheckpointPath(dir, taskID))
	if err != nil {
		return checkpoint, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 16385))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return checkpoint, err
	}
	if len(data) > 16384 || json.Unmarshal(data, &checkpoint) != nil ||
		checkpoint.TaskID != taskID || checkpoint.URL != input.URL ||
		!filepath.IsLocal(checkpoint.Video) || (checkpoint.Subtitle != "" && !filepath.IsLocal(checkpoint.Subtitle)) ||
		(input.Subtitle != "" && checkpoint.Subtitle != input.Subtitle) {
		return checkpoint, errors.New("download checkpoint is invalid or belongs to another import")
	}
	return checkpoint, nil
}
