package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// BGJob — a row in bg_jobs. The frontend reads these directly (over
// REST + WebSocket) to render every long-running operation in the UI:
// data collection, feature computation, model training, simulation.
//
// Workers update `progress` / `total` / `message` / `logs_tail` as they
// go; the api-gateway broadcasts every change on /ws/bg-jobs. The whole
// system rides on this single contract — no per-feature progress endpoints.
type BGJob struct {
	ID         int64           `json:"id"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	Progress   int             `json:"progress"`
	Total      int             `json:"total"`
	Message    *string         `json:"message,omitempty"`
	LogsTail   *string         `json:"logs_tail,omitempty"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

// Job kinds — keep in sync with the comment in 00001_init.sql.
const (
	JobKindCollectHistory  = "collect_history"
	JobKindComputeFeatures = "compute_features"
	JobKindTrainModel      = "train_model"
	JobKindSimulate        = "simulate"
	JobKindRefresh         = "refresh"
	JobKindBootstrap       = "bootstrap"
)

const (
	JobStatusQueued    = "queued"
	JobStatusRunning   = "running"
	JobStatusDone      = "done"
	JobStatusFailed    = "failed"
	JobStatusCancelled = "cancelled"
)

// CancelBGJob marks a bg_job as cancelled. Only queued/running jobs are
// eligible — a done/failed job can't be retroactively cancelled. Returns
// (cancelled bool, error): false means the row exists but was in a
// terminal state, true means we flipped it.
//
// Workers cooperatively check the cancel flag on each progress callback
// (see internal/bgjobs/worker.go) and bail out cleanly with a partial
// "cancelled" status.
func (d *DB) CancelBGJob(ctx context.Context, id int64) (bool, error) {
	tag, err := d.Pool.Exec(ctx, `
		UPDATE bg_jobs
		SET status = 'cancelled',
		    finished_at = now(),
		    message = COALESCE(message, '') || ' [cancelled by user]'
		WHERE id = $1 AND status IN ('queued','running')
	`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// EnqueueBGJob inserts a row in `queued` state. The returned ID is what
// the worker uses to claim and update the row.
func (d *DB) EnqueueBGJob(ctx context.Context, kind string, payload any) (BGJob, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return BGJob{}, fmt.Errorf("marshal payload: %w", err)
	}
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO bg_jobs (kind, payload)
		VALUES ($1, $2)
		RETURNING id, kind, payload, status, progress, total, message,
		          logs_tail, error, created_at, started_at, finished_at
	`, kind, raw)
	return scanBGJob(row)
}

// ClaimNextBGJob picks the oldest queued row and flips it to running atomically.
// Returns pgx.ErrNoRows if there's nothing to do — the caller polls.
//
// A SKIP LOCKED clause means multiple workers can run in parallel safely.
// Equivalent to ClaimNextBGJobByKinds(ctx, nil); kept for callers that
// don't care about kind partitioning.
func (d *DB) ClaimNextBGJob(ctx context.Context) (BGJob, error) {
	return d.ClaimNextBGJobByKinds(ctx, nil)
}

// ClaimNextBGJobByKinds is the kind-filtered variant used by the parallel
// runner. Each worker pool restricts itself to one kind class so a
// rate-limited GitHub ingest doesn't block training requests behind it.
//
// `kinds == nil` (or empty) means "any kind" — the no-filter behaviour.
func (d *DB) ClaimNextBGJobByKinds(ctx context.Context, kinds []string) (BGJob, error) {
	query := `
		WITH next AS (
			SELECT id FROM bg_jobs
			WHERE status = 'queued'
			%s
			ORDER BY created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE bg_jobs
		SET status = 'running', started_at = now()
		WHERE id IN (SELECT id FROM next)
		RETURNING id, kind, payload, status, progress, total, message,
		          logs_tail, error, created_at, started_at, finished_at
	`
	var row pgx.Row
	if len(kinds) == 0 {
		row = d.Pool.QueryRow(ctx, fmt.Sprintf(query, ""))
	} else {
		row = d.Pool.QueryRow(ctx, fmt.Sprintf(query, "AND kind = ANY($1)"), kinds)
	}
	return scanBGJob(row)
}

// UpdateBGJobProgress patches progress fields. Pass -1 for fields you
// don't want to change. Message and logsTail are *replaced* (no append) —
// the worker is expected to track its own tail buffer.
func (d *DB) UpdateBGJobProgress(ctx context.Context, id int64, progress, total int, message, logsTail string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE bg_jobs SET
		  progress = COALESCE(NULLIF($2, -1), progress),
		  total    = COALESCE(NULLIF($3, -1), total),
		  message  = COALESCE(NULLIF($4, ''), message),
		  logs_tail = COALESCE(NULLIF($5, ''), logs_tail)
		WHERE id = $1
	`, id, progress, total, message, logsTail)
	return err
}

func (d *DB) MarkBGJobDone(ctx context.Context, id int64, message string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE bg_jobs SET
		  status = 'done',
		  finished_at = now(),
		  message = COALESCE(NULLIF($2, ''), message),
		  progress = total
		WHERE id = $1
	`, id, message)
	return err
}

func (d *DB) MarkBGJobFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE bg_jobs SET
		  status = 'failed',
		  finished_at = now(),
		  error = $2
		WHERE id = $1
	`, id, errMsg)
	return err
}

// GetBGJob returns a single row by id.
func (d *DB) GetBGJob(ctx context.Context, id int64) (BGJob, error) {
	row := d.Pool.QueryRow(ctx, `
		SELECT id, kind, payload, status, progress, total, message,
		       logs_tail, error, created_at, started_at, finished_at
		FROM bg_jobs WHERE id = $1
	`, id)
	return scanBGJob(row)
}

// ListBGJobs returns recent jobs, optionally filtered by status.
func (d *DB) ListBGJobs(ctx context.Context, status string, limit int) ([]BGJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = d.Pool.Query(ctx, `
			SELECT id, kind, payload, status, progress, total, message,
			       logs_tail, error, created_at, started_at, finished_at
			FROM bg_jobs ORDER BY created_at DESC LIMIT $1
		`, limit)
	} else {
		rows, err = d.Pool.Query(ctx, `
			SELECT id, kind, payload, status, progress, total, message,
			       logs_tail, error, created_at, started_at, finished_at
			FROM bg_jobs WHERE status = $1 ORDER BY created_at DESC LIMIT $2
		`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BGJob{}
	for rows.Next() {
		j, err := scanBGJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func scanBGJob(s scanner) (BGJob, error) {
	var j BGJob
	if err := s.Scan(
		&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Progress, &j.Total,
		&j.Message, &j.LogsTail, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BGJob{}, err
		}
		return BGJob{}, fmt.Errorf("scan bg_job: %w", err)
	}
	return j, nil
}
