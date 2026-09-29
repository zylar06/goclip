package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"autoclip-go/internal/domain"
	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("state or revision conflict")
var ErrNotFound = errors.New("not found")

type Store struct {
	DB    *sql.DB
	Dir   string
	vault *Vault
}

func Open(dir string) (*Store, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(dir, "autoclip.db"))+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Dir: dir}
	var schemaVersion int
	if err = db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	if schemaVersion > 1 {
		db.Close()
		return nil, errors.New("database schema is newer than this application; restore a matching backup to downgrade")
	}
	if _, err = db.Exec(`
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, kind TEXT NOT NULL, status TEXT NOT NULL, heartbeat TEXT NOT NULL, body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS task_queue ON tasks(status,heartbeat);
CREATE TABLE IF NOT EXISTS drafts(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, revision INTEGER NOT NULL, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS candidates(project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS exports(task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS secrets(name TEXT PRIMARY KEY, ciphertext BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS assets(project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, kind TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(project_id,kind));
PRAGMA user_version=1;`); err != nil {
		db.Close()
		return nil, err
	}
	keyPath := filepath.Join(dir, "master.key")
	if _, statErr := os.Stat(keyPath); errors.Is(statErr, os.ErrNotExist) {
		var secretCount int
		if err = db.QueryRow("SELECT count(*) FROM secrets").Scan(&secretCount); err != nil {
			db.Close()
			return nil, err
		}
		if secretCount > 0 {
			db.Close()
			return nil, errors.New("master.key is missing but encrypted secrets exist; restore the matching key")
		}
	} else if statErr != nil {
		db.Close()
		return nil, statErr
	}
	s.vault, err = OpenVault(keyPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) ProjectDir(id string) (string, error) {
	if !domain.ValidID(id) {
		return "", errors.New("invalid project id")
	}
	return filepath.Join(s.Dir, "projects", id), nil
}
func encode(v any) (string, error) { b, e := json.Marshal(v); return string(b), e }
func decode(row *sql.Row, v any) error {
	var b string
	if err := row.Scan(&b); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return json.Unmarshal([]byte(b), v)
}
func newTask(pid, kind string, payload any) (domain.Task, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return domain.Task{}, err
	}
	now := domain.Now()
	return domain.Task{ID: domain.ID(), ProjectID: pid, Kind: kind, Status: "queued", Stage: "queued", CompletedSteps: []string{}, Heartbeat: now, CreatedAt: now, UpdatedAt: now, Payload: b}, nil
}
func insertTask(tx *sql.Tx, t domain.Task) error {
	b, err := encode(t)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO tasks VALUES(?,?,?,?,?,?)", t.ID, t.ProjectID, t.Kind, t.Status, t.Heartbeat, b)
	return err
}
func (s *Store) CreateProject(p domain.Project, payload domain.ImportPayload) (domain.Task, error) {
	t, err := newTask(p.ID, "import", payload)
	if err != nil {
		return t, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	b, err := encode(p)
	if err != nil {
		return t, err
	}
	if _, err = tx.Exec("INSERT INTO projects VALUES(?,?)", p.ID, b); err != nil {
		return t, err
	}
	if err = insertTask(tx, t); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) Projects() ([]domain.Project, error) {
	rows, err := s.DB.Query("SELECT body FROM projects ORDER BY rowid DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var p domain.Project
		if err = json.Unmarshal([]byte(b), &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Project(id string) (p domain.Project, err error) {
	err = decode(s.DB.QueryRow("SELECT body FROM projects WHERE id=?", id), &p)
	return
}
func (s *Store) UpdateProject(p domain.Project) error {
	p.UpdatedAt = domain.Now()
	b, err := encode(p)
	if err != nil {
		return err
	}
	r, err := s.DB.Exec("UPDATE projects SET body=? WHERE id=?", b, p.ID)
	return affected(r, err)
}
func affected(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) Queue(pid, kind string, payload any) (domain.Task, error) {
	t, err := newTask(pid, kind, payload)
	if err != nil {
		return t, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	// Project-level serialization prevents analysis/render/delete races.
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND status IN ('queued','running')", pid).Scan(&n); err != nil {
		return t, err
	}
	if n > 0 {
		return t, ErrConflict
	}
	if err = insertTask(tx, t); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) Task(id string) (t domain.Task, err error) {
	err = decode(s.DB.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t)
	return
}
func taskRows(rows *sql.Rows) ([]domain.Task, error) {
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var t domain.Task
		if err := json.Unmarshal([]byte(b), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Tasks(pid string) ([]domain.Task, error) {
	rows, err := s.DB.Query("SELECT body FROM tasks WHERE project_id=? ORDER BY rowid DESC", pid)
	if err != nil {
		return nil, err
	}
	return taskRows(rows)
}
func saveTask(tx *sql.Tx, t domain.Task, oldStatus string) error {
	t.UpdatedAt = domain.Now()
	b, err := encode(t)
	if err != nil {
		return err
	}
	r, err := tx.Exec("UPDATE tasks SET status=?,heartbeat=?,body=? WHERE id=? AND status=?", t.Status, t.Heartbeat, b, t.ID, oldStatus)
	return affected(r, err)
}
func (s *Store) Claim(ctx context.Context) (domain.Task, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback()
	var t domain.Task
	err = decode(tx.QueryRow("SELECT body FROM tasks WHERE status='queued' AND NOT EXISTS(SELECT 1 FROM tasks WHERE status='running') ORDER BY rowid LIMIT 1"), &t)
	if err != nil {
		return t, err
	}
	t.Status = "running"
	t.Heartbeat = domain.Now()
	t.UpdatedAt = t.Heartbeat
	if err = saveTask(tx, t, "queued"); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) Progress(id, stage string, percent *float64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var t domain.Task
	if err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return err
	}
	if t.Status != "running" || t.CancelRequested {
		return ErrConflict
	}
	if stage != "" && stage != t.Stage {
		if t.Stage != "" && t.Stage != "queued" {
			t.CompletedSteps = append(t.CompletedSteps, t.Stage)
		}
		t.Stage = stage
	}
	t.Progress = percent
	t.Heartbeat = domain.Now()
	if err = saveTask(tx, t, "running"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Heartbeat(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var t domain.Task
	if err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return err
	}
	if t.Status != "running" || t.CancelRequested {
		return ErrConflict
	}
	t.Heartbeat = domain.Now()
	if err = saveTask(tx, t, "running"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Finish(id, status, message string, retryable bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var t domain.Task
	if err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return err
	}
	if t.Status != "running" {
		return ErrConflict
	}
	// Cancellation and completion serialize in this transaction. A cancellation
	// accepted before completion must not subsequently become a successful task.
	if t.CancelRequested {
		status, message, retryable = "cancelled", "Cancelled by user", true
	}
	t.Status = status
	t.Error = message
	t.Retryable = retryable
	t.Heartbeat = domain.Now()
	if status == "completed" {
		v := 100.0
		t.Progress = &v
		t.CompletedSteps = append(t.CompletedSteps, t.Stage)
	}
	if err = saveTask(tx, t, "running"); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Cancel(id string) (domain.Task, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback()
	var t domain.Task
	if err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return t, err
	}
	if t.Terminal() {
		return t, ErrConflict
	}
	old := t.Status
	if t.Status == "running" {
		t.CancelRequested = true
	} else {
		t.Status = "cancelled"
		t.Retryable = true
		t.Error = "Cancelled by user"
	}
	t.Heartbeat = domain.Now()
	if err = saveTask(tx, t, old); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) Retry(id string) (domain.Task, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback()
	var t domain.Task
	if err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return t, err
	}
	if !t.Terminal() || !t.Retryable {
		return t, ErrConflict
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND status IN ('running','queued')", t.ProjectID).Scan(&n); err != nil {
		return t, err
	}
	if n != 0 {
		return t, ErrConflict
	}
	old := t.Status
	t.Status = "queued"
	t.Error = ""
	t.Retryable = false
	t.CancelRequested = false
	t.Heartbeat = domain.Now()
	if err = saveTask(tx, t, old); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) Recover(before time.Time) (int, error) {
	rows, err := s.DB.Query("SELECT body FROM tasks WHERE status='running' AND heartbeat<?", before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	tasks, err := taskRows(rows)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tasks {
		oldHeartbeat := t.Heartbeat
		t.Status = "interrupted"
		t.Error = "Worker heartbeat expired; review before retrying paid requests"
		t.Retryable = true
		t.UpdatedAt = domain.Now()
		b, e := encode(t)
		if e != nil {
			return n, e
		}
		r, e := s.DB.Exec("UPDATE tasks SET status='interrupted',body=? WHERE id=? AND status='running' AND heartbeat=?", b, t.ID, oldHeartbeat)
		if e != nil {
			return n, e
		}
		count, e := r.RowsAffected()
		if e != nil {
			return n, e
		}
		n += int(count)
	}
	return n, nil
}
func (s *Store) Drafts(pid string) ([]domain.Draft, error) {
	rows, err := s.DB.Query("SELECT body FROM drafts WHERE project_id=? ORDER BY rowid", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Draft{}
	for rows.Next() {
		var b string
		var d domain.Draft
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Draft(pid, id string) (d domain.Draft, err error) {
	err = decode(s.DB.QueryRow("SELECT body FROM drafts WHERE id=? AND project_id=?", id, pid), &d)
	return
}
func (s *Store) SaveDraft(pid string, d domain.Draft, create bool) (domain.Draft, error) {
	p, err := s.Project(pid)
	if err != nil {
		return d, err
	}
	d.ProjectID = pid
	if err = d.Validate(p.Duration); err != nil {
		return d, err
	}
	d.UpdatedAt = domain.Now()
	old := d.Revision
	if create {
		d.Revision = 1
	} else {
		d.Revision++
	}
	b, err := encode(d)
	if err != nil {
		return d, err
	}
	if create {
		_, err = s.DB.Exec("INSERT INTO drafts VALUES(?,?,?,?)", d.ID, pid, d.Revision, b)
	} else {
		var r sql.Result
		r, err = s.DB.Exec("UPDATE drafts SET revision=?,body=? WHERE id=? AND project_id=? AND revision=?", d.Revision, b, d.ID, pid, old)
		err = affected(r, err)
	}
	return d, err
}
func (s *Store) SaveAnalysis(taskID, pid string, drafts []domain.Draft, candidates []domain.Candidate) error {
	p, err := s.Project(pid)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow("SELECT status FROM tasks WHERE id=?", taskID).Scan(&status); err != nil {
		return err
	}
	if status != "running" {
		return ErrConflict
	}
	for _, d := range drafts {
		d.ProjectID = pid
		if err = d.Validate(p.Duration); err != nil {
			return fmt.Errorf("model draft: %w", err)
		}
		b, err := encode(d)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO drafts VALUES(?,?,?,?)", d.ID, pid, d.Revision, b); err != nil {
			return err
		}
	}
	b, err := encode(candidates)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO candidates VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET body=excluded.body", pid, b); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Candidates(pid string) (out []domain.Candidate, err error) {
	out = []domain.Candidate{}
	err = decode(s.DB.QueryRow("SELECT body FROM candidates WHERE project_id=?", pid), &out)
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	return
}
func (s *Store) QueueExport(pid, id string, revision int) (domain.Task, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return domain.Task{}, err
	}
	defer tx.Rollback()
	var d domain.Draft
	if err = decode(tx.QueryRow("SELECT body FROM drafts WHERE id=? AND project_id=?", id, pid), &d); err != nil {
		return domain.Task{}, err
	}
	if d.Revision != revision {
		return domain.Task{}, ErrConflict
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND status IN ('queued','running')", pid).Scan(&n); err != nil {
		return domain.Task{}, err
	}
	if n > 0 {
		return domain.Task{}, ErrConflict
	}
	t, err := newTask(pid, "export", domain.ExportPayload{Draft: d})
	if err != nil {
		return t, err
	}
	if err = insertTask(tx, t); err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (s *Store) SaveExport(taskID, pid string, d domain.Draft) error {
	e := domain.Export{TaskID: taskID, DraftID: d.ID, Revision: d.Revision, Title: d.Title, CreatedAt: domain.Now()}
	b, err := encode(e)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO exports VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET body=excluded.body", taskID, pid, b)
	return err
}
func (s *Store) Exports(pid string) ([]domain.Export, error) {
	rows, err := s.DB.Query("SELECT e.body FROM exports e JOIN tasks t ON e.task_id=t.id WHERE e.project_id=? AND t.status='completed' ORDER BY e.rowid DESC", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Export{}
	for rows.Next() {
		var b string
		var e domain.Export
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) DeleteProject(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND status IN ('queued','running')", id).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	r, err := tx.Exec("DELETE FROM projects WHERE id=?", id)
	if err = affected(r, err); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PutSecret(name string, plaintext []byte) error {
	b, err := s.vault.Encrypt(plaintext)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO secrets VALUES(?,?) ON CONFLICT(name) DO UPDATE SET ciphertext=excluded.ciphertext", name, b)
	return err
}
func (s *Store) Secret(name string) ([]byte, error) {
	var b []byte
	err := s.DB.QueryRow("SELECT ciphertext FROM secrets WHERE name=?", name).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.vault.Decrypt(b)
}
func (s *Store) DeleteSecret(name string) error {
	_, err := s.DB.Exec("DELETE FROM secrets WHERE name=?", name)
	return err
}
func (s *Store) Model(kind string) (m domain.ModelSettings, err error) {
	b, err := s.Secret(kind)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return
}
func (s *Store) SetAsset(pid, kind, path string) error {
	if !filepath.IsLocal(path) {
		return errors.New("asset path must be project-relative")
	}
	_, err := s.DB.Exec("INSERT INTO assets VALUES(?,?,?) ON CONFLICT(project_id,kind) DO UPDATE SET path=excluded.path", pid, kind, path)
	return err
}
func (s *Store) Asset(pid, kind string) (string, error) {
	var rel string
	err := s.DB.QueryRow("SELECT path FROM assets WHERE project_id=? AND kind=?", pid, kind).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !filepath.IsLocal(rel) {
		return "", errors.New("invalid stored asset path")
	}
	dir, err := s.ProjectDir(pid)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, rel), nil
}
