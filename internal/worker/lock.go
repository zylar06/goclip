package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Hold an OS lock for the worker lifetime. Unlike a heartbeat, this lock cannot
// expire while a paused process can still write shared AI/media checkpoints.
// The kernel releases it on process exit. Standby workers never touch files.
func acquireWorkerLock(ctx context.Context, dir string) (*os.File, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	file, err := os.OpenFile(filepath.Join(dir, "worker.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	waiting := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		ok, err := tryWorkerLock(file)
		if err != nil {
			return nil, errors.Join(err, file.Close())
		}
		if ok {
			return file, nil
		}
		if !waiting {
			slog.Info("another worker owns checkpoint execution; standing by")
			waiting = true
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-timer.C:
		}
	}
}
