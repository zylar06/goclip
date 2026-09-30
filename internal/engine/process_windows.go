package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200}
	cmd.Cancel = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		kill := exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := kill.Run(); err != nil {
			fallback := cmd.Process.Kill()
			if errors.Is(fallback, os.ErrProcessDone) {
				return os.ErrProcessDone
			}
			return errors.Join(err, fallback)
		}
		return nil
	}
}
