// Storage helpers for the `repo_calibration` table — per-(repo, workflow)
// EMA-corrected multipliers applied on top of model predictions.
//
// Update path: webhook handler at workflow_run.completed when both
// `predicted_sec` (remembered from .requested) and `actual_sec` (derived
// from run_started_at → updated_at) are non-zero. We compute
// `ratio = actual / predicted` and feed it through an exponential
// moving average so the factor adapts quickly to recent observations
// without overshooting on a single outlier.
//
// Read path: predict_from_webhook multiplies the model's raw output by
// the persisted factor right before the broadcast. New (repo, workflow)
// pairs with fewer than `MinObservationsForCalibration` data points
// are returned as 1.0 — the model's raw prediction is honest by
// default, calibration kicks in only after we've seen the slice
// behave consistently.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CalibrationAlpha — EMA learning rate. 0.2 gives ~5-observation
// effective window: a string of 5 in-a-row warm cache deploys nudges
// the factor most of the way to its new value, but a single 200s
// outlier among 4×20s deploys lifts the factor only ~16%.
//
// Lower α (0.05) is more stable but slower to adapt to genuine drift
// (e.g. CI infrastructure change). 0.2 is a defensible default for
// thesis demo; the value is exposed in CalibrationConfig so the
// /admin Settings page could later let the user tune it.
const CalibrationAlpha = 0.2

// MinObservationsForCalibration — predict-time threshold. Below this
// the helper returns factor=1.0 so a single 10× outlier (199s among
// 20s peers) doesn't tank every subsequent prediction.
const MinObservationsForCalibration = 3

// CalibrationFactorMin / Max — safety clamp on the persisted factor.
// EMA + outliers can in theory drift to extreme values; we cap at
// [0.25, 4.0] so even a perfectly biased model can only shift the
// prediction by ±4×. Anything past that is more likely a sign of a
// broken pipeline than legitimate signal.
const (
	CalibrationFactorMin = 0.25
	CalibrationFactorMax = 4.0
)

// CalibrationRow is the persisted form. Pointer fields are nullable
// because a row may exist with n_observations=0 in theory; in practice
// we never write that state but the type honestly reflects the schema.
type CalibrationRow struct {
	RepoID           int64     `json:"repo_id"`
	WorkflowName     string    `json:"workflow_name"`
	Factor           float64   `json:"factor"`
	NObservations    int       `json:"n_observations"`
	LastActualSec    *float64  `json:"last_actual_sec,omitempty"`
	LastPredictedSec *float64  `json:"last_predicted_sec,omitempty"`
	LastRatio        *float64  `json:"last_ratio,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// UpdateCalibration applies one EMA step for the (repo, workflow)
// slice. Returns the factor AFTER the update so callers can log /
// surface it without a follow-up read.
//
// Idempotency: not idempotent (each call advances the EMA). The
// caller (webhook .completed handler) is responsible for not invoking
// twice for the same delivery — `recentPredictions.Forget` after a
// successful match is the natural gate.
//
// Outlier guard: `actualSec` or `predictedSec` ≤ 0 → no-op. Both
// signals are required for a meaningful ratio.
func (d *DB) UpdateCalibration(ctx context.Context, repoID int64, workflowName string, actualSec, predictedSec float64) (float64, error) {
	if workflowName == "" || repoID == 0 {
		return 1.0, nil
	}
	if actualSec <= 0 || predictedSec <= 0 {
		return 1.0, nil
	}

	ratio := actualSec / predictedSec
	// Outlier clamp on the incoming observation — a 50× promotion
	// from a single bad CI run would dominate the EMA for many cycles.
	if ratio < CalibrationFactorMin {
		ratio = CalibrationFactorMin
	} else if ratio > CalibrationFactorMax {
		ratio = CalibrationFactorMax
	}

	// Read-modify-write inside a transaction so a concurrent
	// .completed for the same (repo, workflow) — rare but possible —
	// doesn't lose an update via last-write-wins.
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 1.0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		prevFactor float64
		prevN      int
	)
	row := tx.QueryRow(ctx, `
		SELECT factor, n_observations
		FROM repo_calibration
		WHERE repo_id = $1 AND workflow_name = $2
		FOR UPDATE
	`, repoID, workflowName)
	err = row.Scan(&prevFactor, &prevN)
	if errors.Is(err, pgx.ErrNoRows) {
		prevFactor = 1.0
		prevN = 0
	} else if err != nil {
		return 1.0, err
	}

	// EMA: warm-start at the observed ratio so the first observation
	// snaps the factor close to reality rather than crawling from 1.0
	// over 5 cycles. After n_observations ≥ 1 we use the normal blend.
	var newFactor float64
	if prevN == 0 {
		newFactor = ratio
	} else {
		newFactor = CalibrationAlpha*ratio + (1-CalibrationAlpha)*prevFactor
	}
	if newFactor < CalibrationFactorMin {
		newFactor = CalibrationFactorMin
	} else if newFactor > CalibrationFactorMax {
		newFactor = CalibrationFactorMax
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO repo_calibration (
			repo_id, workflow_name, factor, n_observations,
			last_actual_sec, last_predicted_sec, last_ratio, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (repo_id, workflow_name) DO UPDATE SET
			factor             = EXCLUDED.factor,
			n_observations     = EXCLUDED.n_observations,
			last_actual_sec    = EXCLUDED.last_actual_sec,
			last_predicted_sec = EXCLUDED.last_predicted_sec,
			last_ratio         = EXCLUDED.last_ratio,
			updated_at         = now()
	`, repoID, workflowName, newFactor, prevN+1, actualSec, predictedSec, ratio)
	if err != nil {
		return 1.0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 1.0, err
	}
	return newFactor, nil
}

// GetCalibrationFactor returns the calibration multiplier for a
// (repo, workflow) slice. Returns 1.0 (no calibration) when:
//   - the slice has no row at all,
//   - the slice has fewer than MinObservationsForCalibration data
//     points (single outlier protection),
//   - lookup fails for any reason.
//
// Never returns an error — callers want a multiplier to multiply by,
// and 1.0 is the safe identity.
func (d *DB) GetCalibrationFactor(ctx context.Context, repoID int64, workflowName string) float64 {
	if repoID == 0 || workflowName == "" {
		return 1.0
	}
	var factor float64
	var n int
	err := d.Pool.QueryRow(ctx, `
		SELECT factor, n_observations
		FROM repo_calibration
		WHERE repo_id = $1 AND workflow_name = $2
	`, repoID, workflowName).Scan(&factor, &n)
	if err != nil {
		// Includes ErrNoRows — silently fall back to no calibration.
		return 1.0
	}
	if n < MinObservationsForCalibration {
		return 1.0
	}
	if factor < CalibrationFactorMin || factor > CalibrationFactorMax {
		// Shouldn't happen — write path clamps — but defend in depth.
		return 1.0
	}
	return factor
}

// ListCalibrations returns every (repo, workflow) row joined with the
// repo slug, for the /admin → Calibrations page. Ordered by most
// recently updated so the user sees freshly-touched slices first.
func (d *DB) ListCalibrations(ctx context.Context) ([]CalibrationListRow, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT r.owner, r.name, c.repo_id, c.workflow_name,
		       c.factor, c.n_observations,
		       c.last_actual_sec, c.last_predicted_sec, c.last_ratio,
		       c.updated_at
		FROM repo_calibration c
		JOIN repos r ON r.id = c.repo_id
		ORDER BY c.updated_at DESC
		LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CalibrationListRow{}
	for rows.Next() {
		var r CalibrationListRow
		if err := rows.Scan(
			&r.Owner, &r.Name, &r.RepoID, &r.WorkflowName,
			&r.Factor, &r.NObservations,
			&r.LastActualSec, &r.LastPredictedSec, &r.LastRatio,
			&r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CalibrationListRow is the projection ListCalibrations returns —
// includes the repo slug so the UI doesn't need a second query.
type CalibrationListRow struct {
	Owner            string    `json:"owner"`
	Name             string    `json:"name"`
	RepoID           int64     `json:"repo_id"`
	WorkflowName     string    `json:"workflow_name"`
	Factor           float64   `json:"factor"`
	NObservations    int       `json:"n_observations"`
	LastActualSec    *float64  `json:"last_actual_sec,omitempty"`
	LastPredictedSec *float64  `json:"last_predicted_sec,omitempty"`
	LastRatio        *float64  `json:"last_ratio,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}
