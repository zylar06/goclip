// Package engine owns the narrow process boundary to the Python AutoClip media
// engine.  The Go service remains responsible for durable state and never lets
// engine output write directly to its database.
package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

const maxLine = 2 << 20

type Request struct {
	Operation string         `json:"operation,omitempty"`
	JobID     string         `json:"job_id"`
	Source    string         `json:"source"`
	Workspace string         `json:"workspace"`
	Duration  float64        `json:"duration"`
	Start     float64        `json:"start,omitempty"`
	End       float64        `json:"end,omitempty"`
	Settings  map[string]any `json:"settings"`
	Export    *ExportRequest `json:"export,omitempty"`
}

type ExportRequest struct {
	Destination  string `json:"destination"`
	CropPath     string `json:"crop_path"`
	CaptionStyle string `json:"caption_style"`
	Ratio        string `json:"ratio"`
	Words        []Word `json:"words"`
}

type Candidate struct {
	ID        string  `json:"id"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	StartWord int     `json:"start_word"`
	EndWord   int     `json:"end_word"`
	Title     string  `json:"title"`
	Hook      string  `json:"hook"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	CropPath  string  `json:"crop_path"`
}

type Word struct {
	Text    string  `json:"text"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
}

type Result struct {
	Candidates []Candidate `json:"candidates"`
	Words      []Word      `json:"words"`
	Output     string      `json:"output"`
}

type event struct {
	Type       string      `json:"type"`
	Stage      string      `json:"stage"`
	Percent    float64     `json:"percent"`
	Message    string      `json:"message"`
	Candidates []Candidate `json:"candidates"`
	Words      []Word      `json:"words"`
	Output     string      `json:"output"`
}

// Runner invokes an explicitly configured interpreter/script pair; it never
// builds a shell command. Progress is monotonic only at the caller boundary,
// because different engine stages naturally restart at zero.
type Runner struct {
	Executable string
	Args       []string
}

func (r Runner) Run(ctx context.Context, request Request, progress func(stage string, percent float64) error) (Result, error) {
	if r.Executable == "" {
		return Result{}, errors.New("python engine executable is required")
	}
	if request.JobID == "" || request.Source == "" || request.Workspace == "" || request.Duration <= 0 || request.Settings == nil {
		return Result{}, errors.New("invalid python engine request")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return Result{}, fmt.Errorf("encode python engine request: %w", err)
	}
	cmd := exec.CommandContext(ctx, r.Executable, r.Args...)
	prepareProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	if err = cmd.Start(); err != nil {
		return Result{}, err
	}
	if _, err = stdin.Write(body); err == nil {
		err = stdin.Close()
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return Result{}, fmt.Errorf("write python engine request: %w", err)
	}
	result, decodeErr := decode(stdout, progress)
	waitErr := cmd.Wait()
	if decodeErr != nil {
		return Result{}, decodeErr
	}
	if waitErr != nil {
		return Result{}, fmt.Errorf("python engine: %w", waitErr)
	}
	if result == nil {
		return Result{}, errors.New("python engine exited without a result")
	}
	return *result, nil
}

func decode(reader io.Reader, progress func(stage string, percent float64) error) (*Result, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4<<10), maxLine)
	var result *Result
	for scanner.Scan() {
		var item event
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return nil, errors.New("python engine emitted invalid JSON")
		}
		switch item.Type {
		case "progress":
			if item.Stage == "" || item.Percent < 0 || item.Percent > 100 {
				return nil, errors.New("python engine emitted invalid progress")
			}
			if progress != nil {
				if err := progress(item.Stage, item.Percent); err != nil {
					return nil, err
				}
			}
		case "result":
			if result != nil {
				return nil, errors.New("python engine emitted more than one result")
			}
			result = &Result{Candidates: item.Candidates, Words: item.Words, Output: item.Output}
		case "error":
			if item.Message == "" {
				return nil, errors.New("python engine failed without an error message")
			}
			return nil, errors.New("python engine: " + item.Message)
		default:
			return nil, errors.New("python engine emitted an unknown event")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read python engine output: %w", err)
	}
	return result, nil
}
