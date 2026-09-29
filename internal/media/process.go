package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const logLimit = 64 << 10
const lineLimit = 64 << 10

type command struct {
	exe         string
	args        []string
	dir         string
	timeout     time.Duration
	stdoutLimit int
	line        func(string) error
}

// ToolError excludes argv (which may contain cookies or private file paths).
// Output contains at most the final 64 KiB of the combined native tool logs.
type ToolError struct {
	Tool   string
	Output string
	Err    error
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("media %s: %v\n%s", e.Tool, e.Err, e.Output)
}
func (e *ToolError) Unwrap() error { return e.Err }

type tailBuffer struct{ data []byte }

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= logLimit {
		b.data = append(b.data[:0], p[len(p)-logLimit:]...)
	} else {
		if excess := len(b.data) + len(p) - logLimit; excess > 0 {
			copy(b.data, b.data[excess:])
			b.data = b.data[:len(b.data)-excess]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

// cmd's stdout and stderr goroutines serialize through this mutex. Progress
// callbacks therefore never run concurrently within an operation.
type commandOutput struct {
	mu     sync.Mutex
	tail   tailBuffer
	stdout bytes.Buffer
	limit  int
	line   func(string) error
	err    error
	cancel context.CancelFunc
}

type streamWriter struct {
	out     *commandOutput
	stdout  bool
	pending []byte
	dropped bool
}

func (w *streamWriter) emit() {
	if !w.dropped && len(w.pending) > 0 && w.out.err == nil && w.out.line != nil {
		if err := w.out.line(string(w.pending)); err != nil {
			w.out.err = err
			w.out.cancel()
		}
	}
	w.pending = w.pending[:0]
	w.dropped = false
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.out.mu.Lock()
	defer w.out.mu.Unlock()
	_, _ = w.out.tail.Write(p) // tailBuffer is an in-memory, infallible writer.
	if w.stdout && w.out.limit > 0 {
		if w.out.stdout.Len()+len(p) > w.out.limit {
			if w.out.err == nil {
				w.out.err = errors.New("tool stdout exceeds capture limit")
				w.out.cancel()
			}
		} else {
			_, _ = w.out.stdout.Write(p) // bytes.Buffer.Write cannot fail.
		}
	}
	for _, b := range p {
		if b == '\n' || b == '\r' {
			w.emit()
		} else if len(w.pending) < lineLimit && !w.dropped {
			w.pending = append(w.pending, b)
		} else {
			// Keep draining oversized diagnostic lines without unbounded memory.
			w.dropped = true
		}
	}
	return len(p), nil
}

func run(ctx context.Context, spec command) ([]byte, error) {
	if spec.timeout <= 0 {
		return nil, errors.New("media: command requires a positive timeout")
	}
	timed, stop := context.WithTimeout(ctx, spec.timeout)
	defer stop()
	child, cancel := context.WithCancel(timed)
	defer cancel()
	cmd := exec.CommandContext(child, spec.exe, spec.args...)
	cmd.Dir = spec.dir
	cmd.WaitDelay = 2 * time.Second
	prepareProcess(cmd)
	out := &commandOutput{limit: spec.stdoutLimit, line: spec.line, cancel: cancel}
	stdout, stderr := &streamWriter{out: out, stdout: true}, &streamWriter{out: out}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	stdout.emit()
	stderr.emit()
	// The callback/capture error takes priority over the resulting process kill.
	if out.err != nil {
		err = errors.Join(out.err, err)
	} else if timed.Err() != nil {
		err = errors.Join(timed.Err(), err)
	}
	if err != nil {
		return nil, &ToolError{Tool: filepath.Base(spec.exe), Output: strings.TrimSpace(string(out.tail.data)), Err: err}
	}
	return out.stdout.Bytes(), nil
}

var _ io.Writer = (*streamWriter)(nil)
