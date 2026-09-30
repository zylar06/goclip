package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/store"
	"autoclip-go/internal/worker"
)

// Use the real run() entry point in an isolated process: no test signals or
// os.Args changes can affect the parent test runner or an existing deployment.
func TestWorkerHealthProcess(t *testing.T) {
	mode := os.Getenv("AUTOCLIP_TEST_HEALTH_PROCESS")
	if mode == "" {
		return
	}
	os.Args = []string{"autoclip", "worker"}
	err := run()
	if mode == "write-error" {
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) || pathErr.Path != filepath.Join(os.Getenv("AUTOCLIP_DATA_DIR"), "worker.heartbeat") {
			t.Fatalf("run must propagate heartbeat write failure, got %v", err)
		}
		return
	}
	t.Fatalf("standby unexpectedly returned: %v", err)
}

func workerHealthCommand(t *testing.T, ctx context.Context, dir, mode string) *exec.Cmd {
	t.Helper()
	// These tests are intentionally non-parallel. All child state lives in dir.
	t.Setenv("AUTOCLIP_DATA_DIR", dir)
	t.Setenv("AUTOCLIP_TEST_HEALTH_PROCESS", mode)
	for _, key := range []string{"MAX_UPLOAD_BYTES", "MAX_VIDEO_SECONDS", "TASK_TIMEOUT_SECONDS"} {
		t.Setenv(key, "")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestWorkerHealthProcess$")
	cmd.Env = os.Environ()
	return cmd
}

func TestStandbyRunDoesNotRefreshOwnerHealth(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := s.CreateProject(domain.Project{ID: domain.ID()}, domain.ImportPayload{}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	w := worker.Worker{Store: s, Execute: func(ctx context.Context, _ domain.Task, _ domain.ProgressFunc) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("owner did not shut down")
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owner never acquired execution lock")
	}
	// The parent owns the actual OS lock. A stale timestamp models a stalled
	// owner's health; this test does not suspend a real OS process.
	path := filepath.Join(dir, "worker.heartbeat")
	stale := []byte(time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(path, stale, 0600); err != nil {
		t.Fatal(err)
	}
	childCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	out, childErr := workerHealthCommand(t, childCtx, dir, "standby").CombinedOutput()
	if childErr == nil || !errors.Is(childCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("standby did not remain blocked: %v %s", childErr, out)
	}
	if !strings.Contains(string(out), "another worker owns checkpoint execution; standing by") {
		t.Fatalf("child never reached standby; cannot validate ownership: %s", out)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stale, actual) {
		t.Fatalf("standby refreshed health without owning execution lock: stale=%s actual=%s", stale, actual)
	}
}

func TestWorkerRunReportsInitialHeartbeatWriteFailure(t *testing.T) {
	dir := t.TempDir()
	// A directory at the heartbeat filename makes failure deterministic even
	// under privileged test runners; chmod-based failures would not.
	if err := os.Mkdir(filepath.Join(dir, "worker.heartbeat"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if out, err := workerHealthCommand(t, ctx, dir, "write-error").CombinedOutput(); err != nil {
		t.Fatalf("run did not report initial health write error: %v %s", err, out)
	}
}

func TestWorkerRunRefreshesOwnedHealthAndReportsLaterWriteFailure(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := workerHealthCommand(t, ctx, dir, "write-error")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("health child did not exit")
			}
		}
	}()

	path := filepath.Join(dir, "worker.heartbeat")
	var first time.Time
	refreshed := false
	// Bounded probes allow process startup and at least one idle-loop refresh.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			joined = true
			t.Fatalf("worker exited before injected failure: %v %s", err, output.String())
		default:
		}
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err == nil {
			stamp, parseErr := time.Parse(time.RFC3339Nano, string(data))
			// An in-progress WriteFile can briefly expose an empty file.
			if parseErr == nil {
				if first.IsZero() {
					first = stamp
				} else if stamp.After(first) {
					refreshed = true
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !refreshed {
		t.Fatal("owner did not publish and refresh a valid heartbeat")
	}
	// Replace only this test's file, never an existing service's heartbeat.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatalf("run did not propagate later health write error: %v %s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatal("worker did not stop after heartbeat write failure")
	}
}
