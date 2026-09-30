package engine

import (
	"strings"
	"testing"
)

func TestDecodeAcceptsProgressAndOneResult(t *testing.T) {
	var stages []string
	result, err := decode(strings.NewReader("{\"type\":\"progress\",\"stage\":\"transcribe\",\"percent\":50}\n{\"type\":\"result\",\"candidates\":[{\"id\":\"clip-1\",\"start\":1,\"end\":2}],\"words\":[]}\n"), func(stage string, percent float64) error {
		stages = append(stages, stage)
		return nil
	})
	if err != nil || result == nil || len(result.Candidates) != 1 || len(stages) != 1 {
		t.Fatalf("result=%+v stages=%v err=%v", result, stages, err)
	}
}

func TestDecodeRejectsUnknownAndDuplicateResults(t *testing.T) {
	for _, raw := range []string{
		"{\"type\":\"debug\"}\n",
		"{\"type\":\"result\"}\n{\"type\":\"result\"}\n",
		"{\"type\":\"progress\",\"stage\":\"x\",\"percent\":101}\n",
	} {
		if _, err := decode(strings.NewReader(raw), nil); err == nil {
			t.Fatalf("accepted invalid protocol: %s", raw)
		}
	}
}
