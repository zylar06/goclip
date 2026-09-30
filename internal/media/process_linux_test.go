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
		exited, e := linuxProcessExited(child, syscall.Kill, os.ReadFile)
		if e != nil {
			t.Fatal(e)
		}
		if exited {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("descendant survived process-group cancellation")
}

// Reading /proc races with reaping: open may succeed and read then return ESRCH.
// Only an absent/dead process is success; permission and I/O failures remain errors.
func linuxProcessExited(pid int, kill func(int, syscall.Signal) error, readFile func(string) ([]byte, error)) (bool, error) {
	if err := kill(pid, 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		return false, err
	}
	status, err := readFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		return false, err
	}
	return strings.Contains(string(status), "State:\tZ"), nil
}

func TestLinuxProcessExited(t *testing.T) {
	for _, tc := range []struct {
		name       string
		killErr    error
		readErr    error
		status     string
		wantExited bool
		wantErr    error
	}{
		{name: "already reaped", killErr: syscall.ESRCH, wantExited: true},
		{name: "proc disappeared before open", readErr: &os.PathError{Op: "open", Path: "/proc/42/status", Err: syscall.ENOENT}, wantExited: true},
		{name: "reaped between open and read", readErr: &os.PathError{Op: "read", Path: "/proc/42/status", Err: syscall.ESRCH}, wantExited: true},
		{name: "zombie awaiting reap", status: "Name:\tsleep\nState:\tZ (zombie)\n", wantExited: true},
		{name: "live descendant", status: "Name:\tsleep\nState:\tS (sleeping)\n"},
		{name: "signal permission denied", killErr: syscall.EPERM, wantErr: syscall.EPERM},
		{name: "proc permission denied", readErr: &os.PathError{Op: "read", Path: "/proc/42/status", Err: syscall.EACCES}, wantErr: syscall.EACCES},
		{name: "proc I/O failure", readErr: &os.PathError{Op: "read", Path: "/proc/42/status", Err: syscall.EIO}, wantErr: syscall.EIO},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := false
			exited, err := linuxProcessExited(42, func(pid int, signal syscall.Signal) error {
				if pid != 42 || signal != 0 {
					t.Fatalf("unexpected process probe: %d %d", pid, signal)
				}
				return tc.killErr
			}, func(path string) ([]byte, error) {
				read = true
				if path != "/proc/42/status" {
					t.Fatalf("unexpected proc path: %s", path)
				}
				return []byte(tc.status), tc.readErr
			})
			if exited != tc.wantExited || !errors.Is(err, tc.wantErr) {
				t.Fatalf("exited=%v err=%v, want exited=%v err=%v", exited, err, tc.wantExited, tc.wantErr)
			}
			if read != (tc.killErr == nil) {
				t.Fatal("proc must only be read after successful signal probe")
			}
		})
	}
}
