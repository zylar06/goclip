package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLinuxCancellationKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child := 0
	_, err := run(ctx, command{exe: "/bin/sh", args: []string{"-c", "sleep 60 & echo $!; wait"},
		timeout: 5 * time.Second, line: func(line string) error {
			pid, e := strconv.Atoi(strings.TrimSpace(line))
			if e != nil {
				return e
			}
			child = pid
			cancel()
			return nil
		}})
	if !errors.Is(err, context.Canceled) || child == 0 {
		t.Fatalf("group test failed: child=%d err=%v", child, err)
	}
	// A killed child may briefly remain a zombie until its new parent reaps it.
	for attempt := 0; attempt < 10; attempt++ {
		if e := syscall.Kill(child, 0); errors.Is(e, syscall.ESRCH) {
			return
		}
		status, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", child))
		if errors.Is(e, os.ErrNotExist) || strings.Contains(string(status), "State:\tZ") {
			return
		}
		if e != nil {
			t.Fatal(e)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("descendant survived process-group cancellation")
}
