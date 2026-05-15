package store

import (
	"context"
	"time"
)

// TrainingIterationMetric is one row of `training_metrics`. Written by
// ml-service while a training run is in progress, read by the frontend's
// live training page (`/experiments/jobs/:id`).
//
// We model this as an append-only stream — never UPDATE rows. The
// (training_job_id, iteration) primary key prevents duplicates if the
// ml-service retries; ON CONFLICT DO NOTHING handles that gracefully.
type TrainingIterationMetric struct {
	TrainingJobID int64     `json:"training_job_id"`
	Iteration     int       `json:"iteration"`
	TrainLoss     *float64  `json:"train_loss,omitempty"`
	ValMAE        *float64  `json:"val_mae,omitempty"`
	ValRMSE       *float64  `json:"val_rmse,omitempty"`
	ValMAPE       *float64  `json:"val_mape,omitempty"`
	Ts            time.Time `json:"ts"`
}

type InsertTrainingMetricParams struct {
	TrainingJobID int64
	Iteration     int
	TrainLoss     float64 // 0 → stored as NULL
	ValMAE        float64
	ValRMSE       float64
	ValMAPE       float64
}

// InsertTrainingMetric appends one row. 0-valued metrics map to NULL so
// algorithms that don't expose them (e.g. sklearn LinearRegression without
// per-iteration callbacks) leave the column blank instead of polluting
// the chart with zero baselines.
//
// IMPORTANT: parameters are explicitly cast to DOUBLE PRECISION. Without
// the cast pgx infers $n's type from `NULLIF($n, 0)` — the integer literal
// `0` makes it pick INTEGER, and float64(1.444) silently truncates to 1.
// Discovered the hard way while wiring up the training metrics stream.
func (d *DB) InsertTrainingMetric(ctx context.Context, p InsertTrainingMetricParams) error {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO training_metrics
		    (training_job_id, iteration, train_loss, val_mae, val_rmse, val_mape)
		VALUES ($1, $2,
		        NULLIF($3::DOUBLE PRECISION, 0.0),
		        NULLIF($4::DOUBLE PRECISION, 0.0),
		        NULLIF($5::DOUBLE PRECISION, 0.0),
		        NULLIF($6::DOUBLE PRECISION, 0.0))
		ON CONFLICT (training_job_id, iteration) DO NOTHING
	`, p.TrainingJobID, p.Iteration, p.TrainLoss, p.ValMAE, p.ValRMSE, p.ValMAPE)
	return err
}

// ListTrainingMetrics returns the metric stream for one training run,
// ordered by iteration. Used both by the live page (after initial REST
// load) and the post-training "Compare with active" view.
func (d *DB) ListTrainingMetrics(ctx context.Context, trainingJobID int64) ([]TrainingIterationMetric, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT training_job_id, iteration, train_loss, val_mae, val_rmse, val_mape, ts
		FROM training_metrics
		WHERE training_job_id = $1
		ORDER BY iteration ASC
	`, trainingJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrainingIterationMetric{}
	for rows.Next() {
		var m TrainingIterationMetric
		if err := rows.Scan(&m.TrainingJobID, &m.Iteration, &m.TrainLoss, &m.ValMAE, &m.ValRMSE, &m.ValMAPE, &m.Ts); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
