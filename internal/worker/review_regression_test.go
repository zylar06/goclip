package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"autoclip-go/internal/domain"
	"autoclip-go/internal/media"
	"autoclip-go/internal/store"
)

// Only the explicitly selected test executable handles this offline downloader
// protocol. No shell, network, platform API or live provider is involved.
func TestMain(m *testing.M) {
	if source := os.Getenv("AUTOCLIP_WORKER_FAKE_DOWNLOADER"); source != "" {
		if err := fakeWorkerDownload(source); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeWorkerDownload(source string) error {
	if path := os.Getenv("AUTOCLIP_WORKER_DOWNLOAD_LOG"); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = file.WriteString("download\n")
		if err = errors.Join(err, file.Close()); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.WriteFile("source.mp4", data, 0600); err != nil {
		return err
	}
	subtitle := "1\n00:00:00,000 --> 00:00:01,000\nPLATFORM TEXT\n"
	if os.Getenv("AUTOCLIP_WORKER_BROKEN_PLATFORM") == "1" {
		subtitle = "1\nnot a valid time range\nBROKEN PLATFORM TEXT\n"
	}
	if err = os.WriteFile("source.en.srt", []byte(subtitle), 0600); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, `MEDIA_FILE "source.mp4"`)
	return err
}

func reviewSource(t *testing.T) (string, string, string) {
	t.Helper()
	ffmpeg, ffprobe := productionTools(t)
	path := filepath.Join(t.TempDir(), "source.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", "color=c=red:size=160x90:rate=30:duration=2", "-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-threads", "2", "-pix_fmt", "yuv420p", "-c:a", "aac", path).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	return ffmpeg, ffprobe, path
}

func TestURLImportPreservesUploadedSRTOverPlatform(t *testing.T) {
	checkURLImport(t, false)
}

func TestURLUploadedSRTBypassesMalformedPlatform(t *testing.T) {
	t.Setenv("AUTOCLIP_WORKER_BROKEN_PLATFORM", "1")
	checkURLImport(t, false)
}

func TestURLImportCommitRetryReusesValidatedDownload(t *testing.T) {
	checkURLImport(t, true)
}

func checkURLImport(t *testing.T, failCommit bool) {
	t.Helper()
	ffmpeg, ffprobe, source := reviewSource(t)
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUTOCLIP_WORKER_FAKE_DOWNLOADER", source)
	downloadLog := filepath.Join(t.TempDir(), "downloads.log")
	t.Setenv("AUTOCLIP_WORKER_DOWNLOAD_LOG", downloadLog)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Name: "explicit SRT", URL: "https://youtu.be/abcdefghijk"}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "uploaded.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\nUSER CORRECTED TEXT\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(p, domain.ImportPayload{URL: p.URL, Subtitle: "uploaded.srt"}); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Media: media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe, YTDLP: helper})}
	if failCommit {
		if _, err = s.DB.Exec(`CREATE TRIGGER fail_import_source BEFORE INSERT ON assets WHEN NEW.kind='source' BEGIN SELECT RAISE(ABORT,'injected commit failure'); END;`); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.runTask(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if failCommit {
		failed, err := s.Task(job.ID)
		if err != nil || failed.Status != "failed" {
			t.Fatal("fault injection did not fail import", failed, err)
		}
		if _, err = s.Asset(p.ID, "source"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("incomplete import exposed source", err)
		}
		if _, err = s.DB.Exec("DROP TRIGGER fail_import_source"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Retry(job.ID); err != nil {
			t.Fatal(err)
		}
		job, err = s.Claim(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = w.runTask(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	got, err := w.readCues(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Project(p.ID)
	if err != nil || len(got) != 1 || got[0].Text != "USER CORRECTED TEXT" || current.SubtitleSource != "uploaded" {
		t.Fatal("uploaded SRT was replaced", got, current, err)
	}
	log, err := os.ReadFile(downloadLog)
	if err != nil || string(log) != "download\n" {
		t.Fatal("download repeated after commit failure", string(log), err)
	}
}

func TestInterruptedImportCannotTranscribeAndRetryReusesPreparedSRT(t *testing.T) {
	ffmpeg, ffprobe, source := reviewSource(t)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Name: "crash window", Duration: 2}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "source.mp4"), data, 0600); err != nil {
		t.Fatal(err)
	}
	cues := []domain.Cue{{Start: 0, End: 1, Text: "already parsed uploaded evidence"}}
	if err = atomicJSON(filepath.Join(dir, "subtitles.json"), cues); err != nil {
		t.Fatal(err)
	}
	imp, err := s.CreateProject(p, domain.ImportPayload{Video: "source.mp4", Subtitle: "uploaded.srt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAsset(p.ID, "source", "source.mp4"); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(imp.ID, "interrupted", "crash before asset publication", true); err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Media: media.New(media.Config{FFmpeg: ffmpeg, FFprobe: ffprobe, Whisper: "ASR-MUST-NOT-RUN"})}
	_, err = w.ensureTranscript(context.Background(), p.ID, dir, func(string, *float64) error {
		t.Fatal("incomplete import reached native transcription")
		return nil
	})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatal("expected explicit import-retry requirement", err)
	}
	if _, err = s.Retry(imp.ID); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.runTask(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	got, err := w.readCues(p.ID)
	if err != nil || len(got) != 1 || got[0].Text != cues[0].Text {
		t.Fatal("prepared evidence not recovered", got, err)
	}
}

func TestOSWorkerLockExcludesPausedWriterAndReleases(t *testing.T) {
	dir := t.TempDir()
	owner, err := acquireWorkerLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if next, err := acquireWorkerLock(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		if next != nil {
			next.Close()
		}
		owner.Close()
		t.Fatal("second writer admitted", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := acquireWorkerLock(context.Background(), dir)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestResumedOldWorkerDoesNotFinishNewAttempt(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID()}
	if _, err := s.CreateProject(p, domain.ImportPayload{}); err != nil {
		t.Fatal(err)
	}
	old, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s, Execute: func(_ context.Context, task domain.Task, progress domain.ProgressFunc) error {
		if _, err := s.Recover(time.Now().Add(time.Minute)); err != nil {
			return err
		}
		if _, err := s.Retry(task.ID); err != nil {
			return err
		}
		if _, err := s.Claim(context.Background()); err != nil {
			return err
		}
		return progress("stale-output", nil)
	}}
	if err = w.runTask(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	current, err := s.Task(old.ID)
	if err != nil || current.Status != "running" || current.LeaseID == old.LeaseID || current.Stage == "stale-output" {
		b, _ := json.Marshal(current)
		t.Fatalf("stale worker changed current task: %s %v", b, err)
	}
}

func TestASRCheckpointRecoveryAndMetadataPublicationAreAtomic(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID(), Duration: 2, SubtitleStatus: "missing"}
	dir, err := s.ProjectDir(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(p, domain.ImportPayload{}); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(dir, "subtitles.json"), []domain.Cue{}); err != nil {
		t.Fatal(err)
	}
	if err = s.ForTask(job).CompleteImport(job.ID, p, "source.mp4", "subtitles.json"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Queue(p.ID, "analyze", nil); err != nil {
		t.Fatal(err)
	}
	job, err = s.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cues := []domain.Cue{{Start: 0, End: 1, Text: "already completed ASR"}}
	if err = atomicJSON(filepath.Join(dir, "subtitles-asr.json"), cues); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER fail_transcript BEFORE UPDATE ON projects BEGIN SELECT RAISE(ABORT,'injected metadata failure'); END;`); err != nil {
		t.Fatal(err)
	}
	w := Worker{Store: s.ForTask(job), Media: media.New(media.Config{Whisper: "MUST-NOT-RUN"})}
	progress := func(string, *float64) error {
		t.Fatal("checkpoint recovery repeated ASR")
		return nil
	}
	if _, err = w.ensureTranscript(context.Background(), p.ID, dir, progress); err == nil {
		t.Fatal("fault injection did not fire")
	}
	path, err := s.Asset(p.ID, "subtitles")
	if err != nil || filepath.Base(path) != "subtitles.json" {
		t.Fatal("reference escaped failed metadata transaction", path, err)
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_transcript"); err != nil {
		t.Fatal(err)
	}
	got, err := w.ensureTranscript(context.Background(), p.ID, dir, progress)
	if err != nil || len(got) != 1 || got[0].Text != cues[0].Text {
		t.Fatal("valid orphan ASR not recovered", got, err)
	}
	current, err := s.Project(p.ID)
	if err != nil || current.SubtitleSource != "asr" || current.SubtitleStatus != "available" {
		t.Fatal("metadata did not publish with checkpoint", current, err)
	}
}

func TestHealthWriteFailureCancelsTaskAndStopsWorker(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := domain.Project{ID: domain.ID()}
	job, err := s.CreateProject(p, domain.ImportPayload{})
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("health publication failed")
	writes := 0
	w := Worker{Store: s, HealthBeat: func() error {
		writes++
		if writes > 1 {
			return fault
		}
		return nil
	}, Execute: func(ctx context.Context, _ domain.Task, _ domain.ProgressFunc) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = w.Run(ctx); !errors.Is(err, fault) {
		t.Fatal("health error swallowed", err)
	}
	current, err := s.Task(job.ID)
	if err != nil || current.Status != "failed" || current.Error != fault.Error() {
		t.Fatal("task not stopped explicitly", current, err)
	}
}
