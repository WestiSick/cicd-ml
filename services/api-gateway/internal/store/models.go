package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Model is the surfaced shape of one row in `models`. The frontend's
// /experiments page reads these directly.
type Model struct {
	ID                int64           `json:"id"`
	Name              string          `json:"name"`
	Algo              string          `json:"algo"`
	Params            json.RawMessage `json:"params"`
	Metrics           json.RawMessage `json:"metrics"`
	FeatureImportance json.RawMessage `json:"feature_importance"`
	ArtifactPath      *string         `json:"artifact_path,omitempty"`
	TrainingJobID     *int64          `json:"training_job_id,omitempty"`
	FeatureVersion    int             `json:"feature_version"`
	IsActive          bool            `json:"is_active"`
	TrainedAt         time.Time       `json:"trained_at"`
}

func (d *DB) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, name, algo, params, metrics, feature_importance, artifact_path,
		       training_job_id, feature_version, is_active, trained_at
		FROM models
		ORDER BY trained_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Model{}
	for rows.Next() {
		var m Model
		if err := rows.Scan(
			&m.ID, &m.Name, &m.Algo, &m.Params, &m.Metrics, &m.FeatureImportance,
			&m.ArtifactPath, &m.TrainingJobID, &m.FeatureVersion, &m.IsActive, &m.TrainedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetModel returns one row by id, or pgx.ErrNoRows when missing.
func (d *DB) GetModel(ctx context.Context, id int64) (Model, error) {
	row := d.Pool.QueryRow(ctx, `
		SELECT id, name, algo, params, metrics, feature_importance, artifact_path,
		       training_job_id, feature_version, is_active, trained_at
		FROM models WHERE id = $1
	`, id)
	var m Model
	err := row.Scan(
		&m.ID, &m.Name, &m.Algo, &m.Params, &m.Metrics, &m.FeatureImportance,
		&m.ArtifactPath, &m.TrainingJobID, &m.FeatureVersion, &m.IsActive, &m.TrainedAt,
	)
	return m, err
}

// PredictedActualPoint is one (actual, predicted) row for the scatter plot.
type PredictedActualPoint struct {
	JobID        int64   `json:"job_id"`
	Repo         string  `json:"repo"`
	JobName      string  `json:"job_name"`
	ActualSec    int     `json:"actual_sec"`
	PredictedSec float64 `json:"predicted_sec"`
}

// ListPredictedActual returns up to `limit` (predicted, actual) pairs for
// the given model. Used by the model-detail scatter plot. The join is
// keyed on `predictions.job_id = jobs.id` and filters out rows where
// the ground-truth duration is missing (jobs still in flight).
func (d *DB) ListPredictedActual(ctx context.Context, modelID int64, limit int) ([]PredictedActualPoint, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT j.id,
		       r.owner || '/' || r.name AS repo,
		       j.name AS job_name,
		       j.duration_sec AS actual,
		       p.predicted_sec AS predicted
		FROM predictions p
		JOIN jobs j           ON p.job_id = j.id
		JOIN workflow_runs w  ON j.run_id = w.id
		JOIN repos r          ON w.repo_id = r.id
		WHERE p.model_id = $1
		  AND j.duration_sec IS NOT NULL
		  AND j.duration_sec > 0
		ORDER BY p.predicted_sec ASC
		LIMIT $2
	`, modelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PredictedActualPoint{}
	for rows.Next() {
		var p PredictedActualPoint
		if err := rows.Scan(&p.JobID, &p.Repo, &p.JobName, &p.ActualSec, &p.PredictedSec); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteModel removes the row and (via FK CASCADE) every prediction the
// model ever made. Returns the count of predictions deleted so the UI can
// show "removed XGBoost #42 (1,204 predictions)".
//
// Refuses to delete the currently active model — the user must Activate a
// different one first. The check is a small race (between Get and Delete)
// but the worst outcome is "no active model" which the UI handles.
func (d *DB) DeleteModel(ctx context.Context, id int64) (predDeleted int64, err error) {
	var isActive bool
	if err := d.Pool.QueryRow(ctx, `SELECT is_active FROM models WHERE id = $1`, id).Scan(&isActive); err != nil {
		return 0, err
	}
	if isActive {
		return 0, errors.New("cannot delete the currently active model")
	}

	var preds int64
	_ = d.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM predictions WHERE model_id = $1`, id).Scan(&preds)
	if _, err := d.Pool.Exec(ctx, `DELETE FROM models WHERE id = $1`, id); err != nil {
		return 0, err
	}
	return preds, nil
}

// SetActiveModel atomically marks one model as active and all others as
// inactive. The schema's partial unique index makes this safe under
// concurrent activation attempts — losers see a unique-violation error
// and can retry.
func (d *DB) SetActiveModel(ctx context.Context, id int64) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE models SET is_active = FALSE WHERE is_active = TRUE`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE models SET is_active = TRUE WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
