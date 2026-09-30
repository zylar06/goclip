//go:build !windows

package engine

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Python can leave ffmpeg, MediaPipe and Whisper children behind if only its
// parent is cancelled. Put every engine run in a process group and kill that
// group when the Go task context ends.
func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
