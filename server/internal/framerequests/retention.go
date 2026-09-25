package framerequests

import (
	"context"
	"database/sql"
	"log"
	"time"
)

type Retention struct {
	DB        *sql.DB
	Store     Store
	Interval  time.Duration
	BatchSize int
}

func (r Retention) Run(ctx context.Context) error {
	if r.DB == nil {
		return nil
	}
	batch := r.BatchSize
	if batch < 1 || batch > 500 {
		batch = 100
	}
	now := time.Now().UTC()
	if _, err := r.DB.ExecContext(ctx, `UPDATE frame_requests SET state='pruned',terminal_reason='retention_expired',cleanup_state=CASE WHEN storage_id IS NULL THEN 'not_required' ELSE 'pending' END,cleanup_next_attempt_at=?,updated_at=? WHERE state IN ('requested','claimed','uploaded') AND expires_at<?`, now, now, now); err != nil {
		return err
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT request_id,user_external_uid,storage_id,cleanup_attempts FROM frame_requests WHERE state IN ('pruned','offline','failed','expired','cancelled') AND cleanup_state IN ('pending','failed') AND cleanup_next_attempt_at IS NOT NULL AND cleanup_next_attempt_at<=? ORDER BY cleanup_next_attempt_at ASC LIMIT ?`, now, batch)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, uid, storageID string
		var attempts int
		if err := rows.Scan(&id, &uid, &storageID, &attempts); err != nil {
			return err
		}
		if r.Store == nil {
			continue
		}
		if err := r.Store.Delete(ctx, uid, storageID); err != nil {
			next := now.Add(retryDelay(attempts))
			_, updateErr := r.DB.ExecContext(ctx, `UPDATE frame_requests SET cleanup_state='failed',cleanup_attempts=cleanup_attempts+1,cleanup_next_attempt_at=?,updated_at=? WHERE request_id=? AND user_external_uid=? AND cleanup_state IN ('pending','failed')`, next, now, id, uid)
			if updateErr != nil {
				return updateErr
			}
			continue
		}
		if _, err := r.DB.ExecContext(ctx, `UPDATE frame_requests SET cleanup_state='deleted',cleanup_attempts=cleanup_attempts+1,cleanup_next_attempt_at=NULL,updated_at=? WHERE request_id=? AND user_external_uid=? AND cleanup_state IN ('pending','failed')`, now, id, uid); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = r.DB.ExecContext(ctx, `DELETE FROM frame_requests WHERE state IN ('pruned','offline','failed','expired','cancelled') AND cleanup_state IN ('not_required','deleted') AND expires_at<?`, now.Add(-24*time.Hour))
	return err
}

func (r Retention) RunLoop(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	for {
		if err := r.Run(ctx); err != nil {
			log.Printf("frame request retention: %v", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 16 {
		attempts = 16
	}
	return time.Duration(1<<attempts) * time.Second
}
