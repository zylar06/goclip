package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"autoclip-go/internal/domain"
)

func setup(t *testing.T) (*Store, domain.Project, domain.Task) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	p := domain.Project{ID: domain.ID(), Name: "中文", Duration: 100, CreatedAt: domain.Now(), UpdatedAt: domain.Now(), Status: "importing"}
	task, e := s.CreateProject(p, domain.ImportPayload{Video: "source.mp4"})
	if e != nil {
		t.Fatal(e)
	}
	return s, p, task
}
func TestQueueLifecycleAndRecovery(t *testing.T) {
	s, p, task := setup(t)
	if _, e := s.Queue(p.ID, "analyze", nil); !errors.Is(e, ErrConflict) {
		t.Fatalf("must serialize: %v", e)
	}
	got, e := s.Claim(context.Background())
	if e != nil || got.ID != task.ID {
		t.Fatal(got, e)
	}
	if _, e = s.Claim(context.Background()); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = s.Progress(task.ID, "subtitles", nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Recover(time.Now().Add(time.Minute)); e != nil {
		t.Fatal(e)
	}
	got, e = s.Task(task.ID)
	if e != nil || got.Status != "interrupted" || !got.Retryable {
		t.Fatal(got, e)
	}
	if _, e = s.Retry(task.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Cancel(task.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Finish(task.ID, "completed", "", false); !errors.Is(e, ErrConflict) {
		t.Fatal("cancelled job completed", e)
	}
}
func TestRevisionsFrozenExportAndPersistence(t *testing.T) {
	s, p, task := setup(t)
	if _, e := s.Claim(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := s.Finish(task.ID, "completed", "", false); e != nil {
		t.Fatal(e)
	}
	d := domain.NewDraft("标题", []domain.Scene{{ID: domain.ID(), Start: 1, End: 10}})
	d, e := s.SaveDraft(p.ID, d, true)
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.QueueExport(p.ID, d.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	d.Title = "changed"
	next, e := s.SaveDraft(p.ID, d, false)
	if e != nil || next.Revision != 2 {
		t.Fatal(next, e)
	}
	if _, e = s.SaveDraft(p.ID, d, false); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var payload domain.ExportPayload
	if e = json.Unmarshal(job.Payload, &payload); e != nil {
		t.Fatal(e)
	}
	if payload.Draft.Title != "标题" || payload.Draft.Revision != 1 {
		t.Fatal("snapshot changed")
	}
	second, e := Open(s.Dir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	got, e := second.Draft(p.ID, d.ID)
	if e != nil || got.Title != "changed" {
		t.Fatal(got, e)
	}
	if e = s.DeleteProject(p.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("active project deleted", e)
	}
}
func TestSecretsEncryptedAndKeyDurable(t *testing.T) {
	s, _, _ := setup(t)
	secret := []byte("super-secret-cookie-key")
	if e := s.PutSecret("text", secret); e != nil {
		t.Fatal(e)
	}
	var encrypted []byte
	if e := s.DB.QueryRow("SELECT ciphertext FROM secrets WHERE name='text'").Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(encrypted, secret) {
		t.Fatal("plaintext stored")
	}
	v, e := OpenVault(filepath.Join(s.Dir, "master.key"))
	if e != nil {
		t.Fatal(e)
	}
	got, e := v.Decrypt(encrypted)
	if e != nil || !bytes.Equal(got, secret) {
		t.Fatal(e)
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, e = v.Decrypt(encrypted); e == nil {
		t.Fatal("tamper accepted")
	}
	if _, e = os.Stat(filepath.Join(s.Dir, "master.key")); e != nil {
		t.Fatal(e)
	}
}

func TestMissingKeyDoesNotSilentlyReplaceSecrets(t *testing.T) {
	s, _, _ := setup(t)
	if e := s.PutSecret("text", []byte("secret")); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(s.Dir, "master.key")); e != nil {
		t.Fatal(e)
	}
	if reopened, e := Open(s.Dir); e == nil {
		reopened.Close()
		t.Fatal("generated replacement key for existing ciphertext")
	}
	if _, e := os.Stat(filepath.Join(s.Dir, "master.key")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("replacement key written", e)
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	s, _, _ := setup(t)
	if _, e := s.DB.Exec("PRAGMA user_version=99"); e != nil {
		t.Fatal(e)
	}
	if reopened, e := Open(s.Dir); e == nil {
		reopened.Close()
		t.Fatal("unsupported schema accepted")
	}
}

func TestAcceptedCancellationWinsLateCompletion(t *testing.T) {
	s, _, job := setup(t)
	if _, e := s.Claim(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Cancel(job.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.Finish(job.ID, "completed", "", false); e != nil {
		t.Fatal(e)
	}
	task, e := s.Task(job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if task.Status != "cancelled" || !task.Retryable {
		t.Fatal("cancelled task falsely completed", task)
	}
}
