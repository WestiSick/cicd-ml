package store

import (
	"context"
	"encoding/json"
	"time"
)

// SimRun is one row of sim_runs. Returned by the simulator endpoint and
// rendered on /simulator. The thesis Chapter 4 graphs are read straight
// from these rows — there is no other source of truth.
type SimRun struct {
	ID               int64           `json:"id"`
	Strategy         string          `json:"strategy"`
	WindowStart      time.Time       `json:"window_start"`
	WindowEnd        time.Time       `json:"window_end"`
	Repos            []int64         `json:"repos"`
	JobsCount        int             `json:"jobs_count"`
	MakespanSec      *float64        `json:"makespan_sec,omitempty"`
	WaitP50Sec       *float64        `json:"wait_p50_sec,omitempty"`
	WaitP95Sec       *float64        `json:"wait_p95_sec,omitempty"`
	ThroughputPerMin *float64        `json:"throughput_per_min,omitempty"`
	SLAViolations    *int            `json:"sla_violations,omitempty"`
	Extra            json.RawMessage `json:"extra"`
	CreatedAt        time.Time       `json:"created_at"`
}

type InsertSimRunParams struct {
	Strategy         string
	WindowStart      time.Time
	WindowEnd        time.Time
	Repos            []int64
	JobsCount        int
	MakespanSec      float64
	WaitP50Sec       float64
	WaitP95Sec       float64
	ThroughputPerMin float64
	SLAViolations    int
	Extra            any // JSON-marshalled and stored verbatim
}

// InsertSimRun persists one simulation outcome. Returns the row id.
func (d *DB) InsertSimRun(ctx context.Context, p InsertSimRunParams) (int64, error) {
	extraRaw := []byte("{}")
	if p.Extra != nil {
		if b, err := json.Marshal(p.Extra); err == nil {
			extraRaw = b
		}
	}
	if p.Repos == nil {
		p.Repos = []int64{}
	}
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO sim_runs (
		    strategy, window_start, window_end, repos, jobs_count,
		    makespan_sec, wait_p50_sec, wait_p95_sec, throughput_per_min,
		    sla_violations, extra
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id
	`, p.Strategy, p.WindowStart, p.WindowEnd, p.Repos, p.JobsCount,
		p.MakespanSec, p.WaitP50Sec, p.WaitP95Sec, p.ThroughputPerMin,
		p.SLAViolations, extraRaw)
	var id int64
	return id, row.Scan(&id)
}

// ListSimRuns returns recent simulation outcomes, newest first.
func (d *DB) ListSimRuns(ctx context.Context, limit int) ([]SimRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT id, strategy, window_start, window_end, repos, jobs_count,
		       makespan_sec, wait_p50_sec, wait_p95_sec, throughput_per_min,
		       sla_violations, extra, created_at
		FROM sim_runs ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SimRun{}
	for rows.Next() {
		var s SimRun
		if err := rows.Scan(
			&s.ID, &s.Strategy, &s.WindowStart, &s.WindowEnd, &s.Repos, &s.JobsCount,
			&s.MakespanSec, &s.WaitP50Sec, &s.WaitP95Sec, &s.ThroughputPerMin,
			&s.SLAViolations, &s.Extra, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SimInputJob is the projection over jobs ⨝ workflow_runs ⨝ predictions
// that the simulator endpoint loads from the database. Kept here (not in
// the scheduler package) because pulling it depends on the store/DB types.
type SimInputJob struct {
	ID           int64
	Repo         string
	Branch       string
	ArrivedAt    time.Time
	PredictedSec *float64 // may be NULL until ML is wired
	ActualSec    *int
}

// LoadSimWindow pulls every COMPLETED job that arrived inside the window
// (and optionally inside the given repo set). Falls back to actual_duration
// as the prediction when no ML prediction is available (oracle simulation).
//
// We require both ActualSec and ArrivedAt to be present — running the
// simulator on partial jobs would produce nonsense metrics.
func (d *DB) LoadSimWindow(ctx context.Context, start, end time.Time, repoIDs []int64) ([]SimInputJob, error) {
	args := []any{start, end}
	repoFilter := ""
	if len(repoIDs) > 0 {
		repoFilter = " AND w.repo_id = ANY($3)"
		args = append(args, repoIDs)
	}
	q := `
		SELECT j.id, r.owner || '/' || r.name AS repo,
		       COALESCE(w.head_branch, ''), w.created_at,
		       p.predicted_sec, j.duration_sec
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		JOIN repos r ON w.repo_id = r.id
		LEFT JOIN LATERAL (
		    SELECT predicted_sec FROM predictions p2
		    WHERE p2.job_id = j.id
		    ORDER BY made_at DESC LIMIT 1
		) p ON TRUE
		WHERE w.created_at >= $1 AND w.created_at < $2
		  AND j.duration_sec IS NOT NULL` + repoFilter + `
		ORDER BY w.created_at ASC
	`
	rows, err := d.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SimInputJob{}
	for rows.Next() {
		var j SimInputJob
		if err := rows.Scan(&j.ID, &j.Repo, &j.Branch, &j.ArrivedAt, &j.PredictedSec, &j.ActualSec); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
