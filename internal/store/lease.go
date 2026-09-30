package store

import (
	"context"
	"database/sql"

	"autoclip-go/internal/domain"
)

// ForTask returns a worker-only view. Every worker transaction is fenced by
// the lease returned by Claim, including metadata/asset writes and heartbeat.
// The view shares the underlying connection; its owner must not Close it.
func (s *Store) ForTask(t domain.Task) *Store {
	view := *s
	view.lease = &t
	return &view
}

func (s *Store) begin() (*sql.Tx, error) {
	return s.beginContext(context.Background())
}

func (s *Store) beginContext(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if s.lease != nil {
		var current domain.Task
		err = decode(tx.QueryRow("SELECT body FROM tasks WHERE id=?", s.lease.ID), &current)
		if err == nil && (s.lease.LeaseID == "" || current.LeaseID != s.lease.LeaseID || current.Status != "running") {
			err = ErrConflict
		}
		if err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	return tx, nil
}

func importReadyTx(tx *sql.Tx, pid string) error {
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE project_id=? AND kind='import' AND status!='completed'", pid).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return ErrConflict
	}
	return nil
}
