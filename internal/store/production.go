package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"autoclip-go/internal/domain"
)

var ErrLegacyCandidateOwnership = errors.New("legacy candidate ownership is ambiguous; existing evidence was preserved; start a new confirmed plan instead of retrying this old workflow")

func saveProjectTx(tx *sql.Tx, p domain.Project) error {
	p.UpdatedAt = domain.Now()
	b, err := encode(p)
	if err != nil {
		return err
	}
	r, err := tx.Exec("UPDATE projects SET body=? WHERE id=?", b, p.ID)
	return affected(r, err)
}

func projectIdleTx(tx *sql.Tx, pid string) error {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND status IN ('running','queued')", pid).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrConflict
	}
	return nil
}

func (s *Store) Plan(pid string) (domain.ProductionPlan, error) {
	p, err := s.Project(pid)
	if err != nil {
		return domain.ProductionPlan{}, err
	}
	if p.Duration <= 0 {
		return domain.ProductionPlan{}, ErrConflict
	}
	var incomplete int
	if err = s.DB.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND kind='import' AND status!='completed'", pid).Scan(&incomplete); err != nil {
		return domain.ProductionPlan{}, err
	}
	if incomplete > 0 {
		return domain.ProductionPlan{}, ErrConflict
	}
	if p.Plan != nil {
		return *p.Plan, nil
	}
	p, err = s.ProjectEvidence(p)
	if err != nil {
		return domain.ProductionPlan{}, err
	}
	return domain.LocalPlan(p, 1), nil
}

func (s *Store) UpdatePlan(pid string, u domain.PlanUpdate) (domain.ProductionPlan, error) {
	baseline, err := s.Plan(pid)
	if err != nil {
		return domain.ProductionPlan{}, err
	}
	tx, err := s.begin()
	if err != nil {
		return domain.ProductionPlan{}, err
	}
	defer tx.Rollback()
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", pid), &p); err != nil {
		return domain.ProductionPlan{}, err
	}
	if err = projectIdleTx(tx, pid); err != nil {
		return domain.ProductionPlan{}, err
	}
	if p.Duration <= 0 {
		return domain.ProductionPlan{}, ErrConflict
	}
	plan := baseline
	if p.Plan != nil {
		plan = *p.Plan
	}
	if u.Revision != plan.Revision {
		return plan, ErrConflict
	}
	u.Options.Confirmed = false
	plan.Revision++
	plan.Status = "awaiting_confirmation"
	plan.Options = u.Options
	plan.AutoExport = u.AutoExport
	p.Plan = &plan
	if err = saveProjectTx(tx, p); err != nil {
		return plan, err
	}
	return plan, tx.Commit()
}

func (s *Store) ConfirmProduction(pid string, revision int) (domain.Workflow, error) {
	baseline, planErr := s.Plan(pid)
	tx, err := s.begin()
	if err != nil {
		return domain.Workflow{}, err
	}
	defer tx.Rollback()
	var existing domain.Workflow
	err = decode(tx.QueryRow("SELECT body FROM workflows WHERE project_id=? AND plan_revision=?", pid, revision), &existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return existing, err
	}
	if planErr != nil {
		return existing, planErr
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", pid), &p); err != nil {
		return existing, err
	}
	if err = projectIdleTx(tx, pid); err != nil {
		return existing, err
	}
	if err = importReadyTx(tx, pid); err != nil {
		return existing, err
	}
	plan := baseline
	if p.Plan != nil {
		plan = *p.Plan
	}
	if p.Duration <= 0 || revision != plan.Revision || plan.Status != "awaiting_confirmation" || len(plan.Options.Goals) == 0 {
		return existing, ErrConflict
	}
	options := plan.Options
	options.Confirmed = true
	now := domain.Now()
	wf := domain.Workflow{ID: domain.ID(), ProjectID: pid, PlanRevision: revision, Options: options, AutoExport: plan.AutoExport, Status: "queued", CreatedAt: now, UpdatedAt: now, Goals: []domain.GoalResult{}}
	for _, g := range options.Goals {
		wf.Goals = append(wf.Goals, domain.GoalResult{Goal: g, Status: "queued", DraftIDs: []string{}, ExportTaskIDs: []string{}})
	}
	b, err := encode(wf)
	if err != nil {
		return wf, err
	}
	if _, err = tx.Exec("INSERT INTO workflows VALUES(?,?,?,?)", wf.ID, pid, revision, b); err != nil {
		return wf, err
	}
	t, err := newTask(pid, "analyze", options)
	if err != nil {
		return wf, err
	}
	t.WorkflowID = wf.ID
	if err = insertTask(tx, t); err != nil {
		return wf, err
	}
	plan.Status = "confirmed"
	p.Plan = &plan
	p.Status = "processing"
	p.Error = ""
	if err = saveProjectTx(tx, p); err != nil {
		return wf, err
	}
	return wf, tx.Commit()
}

// Old projects may have transcript assets but no source metadata. Read their
// evidence without changing saved drafts or guessing how the text was obtained.
func (s *Store) ProjectEvidence(p domain.Project) (domain.Project, error) {
	if p.SubtitleStatus != "" {
		return p, nil
	}
	path, err := s.Asset(p.ID, "subtitles")
	if errors.Is(err, ErrNotFound) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		p.SubtitleStatus = "missing"
		return p, nil
	}
	if err != nil {
		return p, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (10<<20)+1))
	closeErr := f.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return p, err
	}
	var cues []domain.Cue
	if len(data) > 10<<20 || json.Unmarshal(data, &cues) != nil {
		p.SubtitleStatus = "invalid"
		return p, nil
	}
	for _, c := range cues {
		if c.Start < 0 || c.End <= c.Start || c.End > p.Duration+.5 {
			p.SubtitleStatus = "invalid"
			return p, nil
		}
	}
	p.SubtitleStatus = "empty"
	if len(cues) > 0 {
		p.SubtitleStatus = "available"
		p.SubtitleSource = "existing"
	}
	return p, nil
}

func (s *Store) Workflow(id string) (w domain.Workflow, err error) {
	err = decode(s.DB.QueryRow("SELECT body FROM workflows WHERE id=?", id), &w)
	return
}
func (s *Store) Workflows(pid string) ([]domain.Workflow, error) {
	rows, err := s.DB.Query("SELECT body FROM workflows WHERE project_id=? ORDER BY rowid DESC", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Workflow{}
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var w domain.Workflow
		if err = json.Unmarshal([]byte(b), &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func saveWorkflowTx(tx *sql.Tx, w domain.Workflow) error {
	w.UpdatedAt = domain.Now()
	b, err := encode(w)
	if err != nil {
		return err
	}
	r, err := tx.Exec("UPDATE workflows SET body=? WHERE id=?", b, w.ID)
	return affected(r, err)
}

// A workflow never holds a worker lease. Its outcome follows durable children.
func refreshWorkflowTx(tx *sql.Tx, id string) error {
	var w domain.Workflow
	if err := decode(tx.QueryRow("SELECT body FROM workflows WHERE id=?", id), &w); err != nil {
		return err
	}
	active, good, bad := false, false, false
	var analysis domain.Task
	rows, err := tx.Query("SELECT body FROM tasks WHERE project_id=?", w.ProjectID)
	if err != nil {
		return err
	}
	tasks, err := taskRows(rows)
	if err != nil {
		return err
	}
	byID := map[string]domain.Task{}
	for _, t := range tasks {
		if t.WorkflowID == id {
			byID[t.ID] = t
			if t.Kind == "analyze" {
				analysis = t
			}
			if !t.Terminal() {
				active = true
			}
		}
	}
	for i := range w.Goals {
		g := &w.Goals[i]
		if len(g.ExportTaskIDs) > 0 {
			wait, failed, completed := false, false, 0
			for _, tid := range g.ExportTaskIDs {
				t, ok := byID[tid]
				if !ok {
					return fmt.Errorf("workflow references missing export task")
				}
				if !t.Terminal() {
					wait = true
				} else if t.Status != "completed" {
					failed = true
					g.Error = t.Error
				} else {
					completed++
				}
			}
			switch {
			case wait:
				g.Status = "rendering"
			case failed:
				g.Status = "failed"
			default:
				g.Status = "completed"
				g.Error = ""
			}
			if completed > 0 {
				good = true
			}
		} else if len(g.DraftIDs) == 0 {
			switch analysis.Status {
			case "queued", "running":
				g.Status = "analyzing"
			case "failed", "cancelled", "interrupted":
				g.Status = analysis.Status
				if g.Error == "" {
					g.Error = analysis.Error
				}
			}
		}
		if g.Status == "completed" {
			good = true
		}
		if g.Status == "failed" || g.Status == "cancelled" || g.Status == "interrupted" {
			bad = true
		}
	}
	switch {
	case active:
		w.Status = "running"
	case bad && good:
		w.Status = "partial"
	case bad:
		w.Status = "failed"
		if analysis.Status == "cancelled" {
			w.Status = "cancelled"
		} else if analysis.Status == "interrupted" {
			w.Status = "interrupted"
		}
	default:
		w.Status = "completed"
	}
	if err = saveWorkflowTx(tx, w); err != nil {
		return err
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", w.ProjectID), &p); err != nil {
		return err
	}
	switch w.Status {
	case "running", "queued":
		p.Status = "processing"
	case "completed":
		p.Status = "drafts_ready"
		for _, g := range w.Goals {
			if len(g.ExportTaskIDs) > 0 {
				p.Status = "exported"
			}
		}
		p.Error = ""
	default:
		p.Status = w.Status
	}
	return saveProjectTx(tx, p)
}

func syncProjectTx(tx *sql.Tx, t domain.Task) error {
	var p domain.Project
	if err := decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", t.ProjectID), &p); err != nil {
		return err
	}
	if t.Status == "completed" {
		p.Error = ""
		switch t.Kind {
		case "import":
			p.Status = "source_ready"
		case "analyze":
			p.Status = "drafts_ready"
		case "export":
			p.Status = "exported"
		}
	} else if t.Kind != "preview" && t.Kind != "inspect" {
		p.Error = t.Error
		p.Status = t.Status
	}
	return saveProjectTx(tx, p)
}

func finishTaskTx(tx *sql.Tx, t domain.Task, status, message string, retryable bool) error {
	if t.Status != "running" {
		return ErrConflict
	}
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
	if err := saveTask(tx, t, "running"); err != nil {
		return err
	}
	if err := syncProjectTx(tx, t); err != nil {
		return err
	}
	if t.WorkflowID != "" {
		return refreshWorkflowTx(tx, t.WorkflowID)
	}
	return nil
}

func runningTaskTx(tx *sql.Tx, id, pid string) (domain.Task, error) {
	var t domain.Task
	if err := decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", id), &t); err != nil {
		return t, err
	}
	if t.Status != "running" || t.CancelRequested || (pid != "" && t.ProjectID != pid) {
		return t, ErrConflict
	}
	return t, nil
}

func saveDraftsTx(tx *sql.Tx, p domain.Project, drafts []domain.Draft, candidates []domain.Candidate) error {
	for _, d := range drafts {
		d.ProjectID = p.ID
		if err := d.Validate(p.Duration); err != nil {
			return err
		}
		b, err := encode(d)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO drafts VALUES(?,?,?,?)", d.ID, p.ID, d.Revision, b); err != nil {
			return err
		}
	}
	if candidates != nil {
		b, err := encode(candidates)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO candidates VALUES(?,?) ON CONFLICT(project_id) DO UPDATE SET body=excluded.body", p.ID, b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CompleteAnalysis(id, pid string, drafts []domain.Draft, candidates []domain.Candidate) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, pid)
	if err != nil {
		return err
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", pid), &p); err != nil {
		return err
	}
	if err = saveDraftsTx(tx, p, drafts, candidates); err != nil {
		return err
	}
	if err = finishTaskTx(tx, t, "completed", "", false); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteProductionAnalysis(id string, drafts []domain.Draft, candidates []domain.Candidate, results []domain.GoalResult, retryable bool) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, "")
	if err != nil {
		return err
	}
	if t.WorkflowID == "" {
		return ErrConflict
	}
	var w domain.Workflow
	if err = decode(tx.QueryRow("SELECT body FROM workflows WHERE id=?", t.WorkflowID), &w); err != nil {
		return err
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", t.ProjectID), &p); err != nil {
		return err
	}
	candidates, err = mergeWorkflowCandidatesTx(tx, w.ID, candidates)
	if err != nil {
		return err
	}
	if err = saveDraftsTx(tx, p, drafts, candidates); err != nil {
		return err
	}
	byDraft := map[string]domain.Draft{}
	for _, d := range drafts {
		byDraft[d.ID] = d
	}
	for _, r := range results {
		for i := range w.Goals {
			g := &w.Goals[i]
			if g.Goal != r.Goal {
				continue
			}
			if len(g.DraftIDs) > 0 {
				continue
			}
			if r.DraftIDs == nil {
				r.DraftIDs = []string{}
			}
			r.ExportTaskIDs = []string{}
			if r.Status == "completed" && (r.Goal == "content" || w.AutoExport) {
				for _, did := range r.DraftIDs {
					d, ok := byDraft[did]
					if !ok {
						return errors.New("goal references missing draft")
					}
					d.ProjectID = t.ProjectID
					ex, err := newTask(t.ProjectID, "export", domain.ExportPayload{Draft: d})
					if err != nil {
						return err
					}
					ex.WorkflowID = w.ID
					ex.Goal = r.Goal
					if err = insertTask(tx, ex); err != nil {
						return err
					}
					r.ExportTaskIDs = append(r.ExportTaskIDs, ex.ID)
				}
			}
			*g = r
		}
	}
	if err = saveWorkflowTx(tx, w); err != nil {
		return err
	}
	status, message := "completed", ""
	for _, g := range results {
		if g.Status == "failed" {
			status = "failed"
			if message != "" {
				message += "; "
			}
			message += g.Goal + ": " + g.Error
		}
	}
	if err = finishTaskTx(tx, t, status, message, retryable); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteExport(id, pid string, d domain.Draft) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, pid)
	if err != nil {
		return err
	}
	e := domain.Export{TaskID: id, DraftID: d.ID, Revision: d.Revision, Title: d.Title, CreatedAt: domain.Now()}
	b, err := encode(e)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO exports VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET body=excluded.body", id, pid, b); err != nil {
		return err
	}
	if err = finishTaskTx(tx, t, "completed", "", false); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DisableSubtitles(pid string) (int, error) {
	tx, err := s.begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = projectIdleTx(tx, pid); err != nil {
		return 0, err
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", pid), &p); err != nil {
		return 0, err
	}
	rows, err := tx.Query("SELECT body FROM drafts WHERE project_id=?", pid)
	if err != nil {
		return 0, err
	}
	drafts := []domain.Draft{}
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return 0, err
		}
		var d domain.Draft
		if err = json.Unmarshal([]byte(b), &d); err != nil {
			rows.Close()
			return 0, err
		}
		if d.Subtitles {
			drafts = append(drafts, d)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, d := range drafts {
		d.Subtitles = false
		d.Revision++
		d.UpdatedAt = domain.Now()
		b, err := encode(d)
		if err != nil {
			return 0, err
		}
		if _, err = tx.Exec("UPDATE drafts SET revision=?,body=? WHERE id=? AND project_id=?", d.Revision, b, d.ID, pid); err != nil {
			return 0, err
		}
	}
	return len(drafts), tx.Commit()
}

func (s *Store) CompleteInspection(id string, p domain.Project) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, p.ID)
	if err != nil {
		return err
	}
	// Only inspection-owned fields change; do not overwrite concurrent metadata.
	var current domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", p.ID), &current); err != nil {
		return err
	}
	current.Plan = p.Plan
	if err = saveProjectTx(tx, current); err != nil {
		return err
	}
	if err = finishTaskTx(tx, t, "completed", "", false); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompletePreview(id, pid, rel string) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, pid)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO assets VALUES(?,?,?) ON CONFLICT(project_id,kind) DO UPDATE SET path=excluded.path", pid, "preview", rel); err != nil {
		return err
	}
	if err = finishTaskTx(tx, t, "completed", "", false); err != nil {
		return err
	}
	return tx.Commit()
}

// Import artifacts are prepared first, then published together with metadata
// and terminal task state. A failed or interrupted import cannot expose a plan
// that silently replaces uploaded evidence with ASR.
func (s *Store) CompleteImport(id string, p domain.Project, source, subtitles string) error {
	if !filepath.IsLocal(source) || !filepath.IsLocal(subtitles) {
		return errors.New("import assets must be project-relative")
	}
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := runningTaskTx(tx, id, p.ID)
	if err != nil {
		return err
	}
	if err = saveProjectTx(tx, p); err != nil {
		return err
	}
	for kind, path := range map[string]string{"source": source, "subtitles": subtitles} {
		if _, err = tx.Exec("INSERT INTO assets VALUES(?,?,?) ON CONFLICT(project_id,kind) DO UPDATE SET path=excluded.path", p.ID, kind, path); err != nil {
			return err
		}
	}
	if err = finishTaskTx(tx, t, "completed", "", false); err != nil {
		return err
	}
	return tx.Commit()
}

func mergeWorkflowCandidatesTx(tx *sql.Tx, workflow string, incoming []domain.Candidate) ([]domain.Candidate, error) {
	var previous []domain.Candidate
	err := decode(tx.QueryRow("SELECT body FROM workflow_candidates WHERE workflow_id=?", workflow), &previous)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrNotFound) {
		previous, err = legacyWorkflowCandidatesTx(tx, workflow)
		if err != nil {
			return nil, err
		}
	}
	mode := func(id string) string {
		prefix, _, _ := strings.Cut(id, "-")
		return prefix
	}
	replaced := map[string]bool{}
	for _, c := range incoming {
		replaced[mode(c.ID)] = true
	}
	merged := append([]domain.Candidate{}, incoming...)
	for _, c := range previous {
		if !replaced[mode(c.ID)] {
			merged = append(merged, c)
		}
	}
	body, err := encode(merged)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec("INSERT INTO workflow_candidates VALUES(?,?) ON CONFLICT(workflow_id) DO UPDATE SET body=excluded.body", workflow, body)
	return merged, err
}

// Pre-snapshot schema-2 retries can recover project-level evidence only when
// one workflow owns it and an existing successful draft anchors the identity.
// Ambiguous ownership is an explicit error; its transaction preserves old data.
func legacyWorkflowCandidatesTx(tx *sql.Tx, id string) ([]domain.Candidate, error) {
	var w domain.Workflow
	if err := decode(tx.QueryRow("SELECT body FROM workflows WHERE id=?", id), &w); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, goal := range w.Goals {
		ids = append(ids, goal.DraftIDs...)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var previous []domain.Candidate
	err := decode(tx.QueryRow("SELECT body FROM candidates WHERE project_id=?", w.ProjectID), &previous)
	if errors.Is(err, ErrNotFound) || (err == nil && len(previous) == 0) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRow("SELECT count(*) FROM workflows WHERE project_id=?", w.ProjectID).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrLegacyCandidateOwnership
	}
	for _, did := range ids {
		var d domain.Draft
		if err = decode(tx.QueryRow("SELECT body FROM drafts WHERE id=? AND project_id=?", did, w.ProjectID), &d); err != nil {
			return nil, err
		}
		for _, scene := range d.Scenes {
			for _, c := range previous {
				if (c.ID == "subtitle-"+scene.ID || c.ID == "visual-"+scene.ID) &&
					math.Abs(c.Start-scene.Start) <= .001 && math.Abs(c.End-scene.End) <= .001 {
					return previous, nil
				}
			}
		}
	}
	return nil, ErrLegacyCandidateOwnership
}

func (s *Store) RequireSourceReady(pid string) error {
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return importReadyTx(tx, pid)
}

func (s *Store) CompleteTranscript(pid, rel, source string, count int) error {
	if !filepath.IsLocal(rel) || count < 0 {
		return errors.New("invalid transcript publication")
	}
	tx, err := s.begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.lease != nil {
		if _, err = runningTaskTx(tx, s.lease.ID, pid); err != nil {
			return err
		}
	}
	if err = importReadyTx(tx, pid); err != nil {
		return err
	}
	var p domain.Project
	if err = decode(tx.QueryRow("SELECT body FROM projects WHERE id=?", pid), &p); err != nil {
		return err
	}
	p.SubtitleSource = source
	p.SubtitleStatus = "available"
	if count == 0 {
		p.SubtitleStatus = "empty"
	}
	if _, err = tx.Exec("INSERT INTO assets VALUES(?,?,?) ON CONFLICT(project_id,kind) DO UPDATE SET path=excluded.path", pid, "subtitles", rel); err != nil {
		return err
	}
	if err = saveProjectTx(tx, p); err != nil {
		return err
	}
	return tx.Commit()
}
