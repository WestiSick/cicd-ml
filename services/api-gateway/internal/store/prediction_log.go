// Storage helpers for the `prediction_log` table — persistent record of
// what the user saw on /dashboard at workflow_run.completed time:
// predicted (calibrated), predicted_raw (model output before calibration),
// actual, calibration factor, model id/algo, plus enough metadata to
// trace back to the repo / workflow / commit.
//
// Populated by the webhook handler — see ensureCommitForWebhook's
// neighbouring block in webhook.go. Read by /api/queue/history.
package store

import (
	"context"
	"time"
)

// InsertPredictionLogParams is the slim row payload. Pointer types are
// used where the column is nullable (predicted/actual can both be nil
// when the webhook fired without an active model or with timing
// anomalies that produce actual_sec ≤ 0).
type InsertPredictionLogParams struct {
	RunID             int64
	Repo              string  // canonical "owner/name"
	Workflow          string
	HeadBranch        string
	HeadSHA           string
	Event             string
	Conclusion        string
	PredictedSec      *float64
	PredictedRawSec   *float64
	CalibrationFactor *float64
	ActualSec         *float64
	DeltaPct          *float64
	ModelID           int64
	ModelAlgo         string
	CompletedAt       time.Time
}

// InsertPredictionLog upserts a row keyed by run_id. Repeated webhook
// deliveries for the same run (GitHub retries) refresh the row rather
// than spam the table.
//
// Returns the inserted/updated row's id so callers (currently webhook
// handler) can log it for traceability.
func (d *DB) InsertPredictionLog(ctx context.Context, p InsertPredictionLogParams) (int64, error) {
	var id int64
	err := d.Pool.QueryRow(ctx, `
		INSERT INTO prediction_log (
			run_id, repo, workflow, head_branch, head_sha, event, conclusion,
			predicted_sec, predicted_raw_sec, calibration_factor,
			actual_sec, delta_pct,
			model_id, model_algo, completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7,
		        $8, $9, $10,
		        $11, $12,
		        $13, $14, $15)
		ON CONFLICT (run_id) DO UPDATE SET
			repo               = EXCLUDED.repo,
			workflow           = EXCLUDED.workflow,
			head_branch        = EXCLUDED.head_branch,
			head_sha           = EXCLUDED.head_sha,
			event              = EXCLUDED.event,
			conclusion         = EXCLUDED.conclusion,
			predicted_sec      = COALESCE(EXCLUDED.predicted_sec,      prediction_log.predicted_sec),
			predicted_raw_sec  = COALESCE(EXCLUDED.predicted_raw_sec,  prediction_log.predicted_raw_sec),
			calibration_factor = COALESCE(EXCLUDED.calibration_factor, prediction_log.calibration_factor),
			actual_sec         = COALESCE(EXCLUDED.actual_sec,         prediction_log.actual_sec),
			delta_pct          = COALESCE(EXCLUDED.delta_pct,          prediction_log.delta_pct),
			model_id           = COALESCE(NULLIF(EXCLUDED.model_id, 0), prediction_log.model_id),
			model_algo         = COALESCE(NULLIF(EXCLUDED.model_algo, ''), prediction_log.model_algo),
			completed_at       = EXCLUDED.completed_at
		RETURNING id
	`,
		p.RunID, p.Repo, nilIfEmpty(p.Workflow), nilIfEmpty(p.HeadBranch),
		nilIfEmpty(p.HeadSHA), nilIfEmpty(p.Event), nilIfEmpty(p.Conclusion),
		p.PredictedSec, p.PredictedRawSec, p.CalibrationFactor,
		p.ActualSec, p.DeltaPct,
		p.ModelID, nilIfEmpty(p.ModelAlgo), p.CompletedAt,
	).Scan(&id)
	return id, err
}

// ListPredictionLogParams — filter set for the /api/queue/history
// endpoint. All fields optional; zero/empty values disable the filter.
//
// MinAbsDeltaPct lets the user surface "the misses": e.g. 30 shows
// only rows where |delta_pct| ≥ 30%. Useful for spotting systematic
// errors without scrolling past hundreds of well-predicted rows.
type ListPredictionLogParams struct {
	Repo           string    // exact "owner/name" match
	Since          time.Time // only rows with completed_at >= Since (zero = no filter)
	MinAbsDeltaPct float64   // |delta_pct| filter (0 = disabled)
	Limit          int       // capped at 2000
}

// PredictionLogRow — the projection the API returns. Pointer floats
// stay nullable so the JSON serialisation correctly omits absent
// values (e.g. delta_pct can be NULL when actual_sec was 0).
type PredictionLogRow struct {
	ID                int64      `json:"id"`
	RunID             int64      `json:"run_id"`
	Repo              string     `json:"repo"`
	Workflow          *string    `json:"workflow,omitempty"`
	HeadBranch        *string    `json:"head_branch,omitempty"`
	HeadSHA           *string    `json:"head_sha,omitempty"`
	Event             *string    `json:"event,omitempty"`
	Conclusion        *string    `json:"conclusion,omitempty"`
	PredictedSec      *float64   `json:"predicted_sec,omitempty"`
	PredictedRawSec   *float64   `json:"predicted_raw_sec,omitempty"`
	CalibrationFactor *float64   `json:"calibration_factor,omitempty"`
	ActualSec         *float64   `json:"actual_sec,omitempty"`
	DeltaPct          *float64   `json:"delta_pct,omitempty"`
	ModelID           *int64     `json:"model_id,omitempty"`
	ModelAlgo         *string    `json:"model_algo,omitempty"`
	CompletedAt       time.Time  `json:"completed_at"`
}

// ListPredictionLog returns rows matching the filter, newest first.
// Hard-capped at 2000 — multi-year dumps belong in a CSV export, not
// the UI, but 2000 covers the full thesis demo window (~1000 synthetic
// rows + headroom).
func (d *DB) ListPredictionLog(ctx context.Context, p ListPredictionLogParams) ([]PredictionLogRow, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 2000 {
		limit = 2000
	}

	// Build WHERE clause dynamically. Keeping it as a single query
	// (rather than several pre-baked variants) is cleaner; the planner
	// uses the indexes either way.
	sql := `
		SELECT id, run_id, repo, workflow, head_branch, head_sha, event, conclusion,
		       predicted_sec, predicted_raw_sec, calibration_factor,
		       actual_sec, delta_pct,
		       model_id, model_algo, completed_at
		FROM prediction_log
		WHERE 1=1`
	args := []any{}

	if p.Repo != "" {
		args = append(args, p.Repo)
		sql += " AND repo = $" + itoa(len(args))
	}
	if !p.Since.IsZero() {
		args = append(args, p.Since)
		sql += " AND completed_at >= $" + itoa(len(args))
	}
	if p.MinAbsDeltaPct > 0 {
		args = append(args, p.MinAbsDeltaPct)
		sql += " AND delta_pct IS NOT NULL AND abs(delta_pct) >= $" + itoa(len(args))
	}

	args = append(args, limit)
	sql += " ORDER BY completed_at DESC LIMIT $" + itoa(len(args))

	rows, err := d.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PredictionLogRow{}
	for rows.Next() {
		var r PredictionLogRow
		if err := rows.Scan(
			&r.ID, &r.RunID, &r.Repo, &r.Workflow, &r.HeadBranch, &r.HeadSHA, &r.Event, &r.Conclusion,
			&r.PredictedSec, &r.PredictedRawSec, &r.CalibrationFactor,
			&r.ActualSec, &r.DeltaPct,
			&r.ModelID, &r.ModelAlgo, &r.CompletedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// itoa is a tiny non-allocating int-to-string for the WHERE-clause
// builder. Avoids pulling strconv into this file for the one use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
