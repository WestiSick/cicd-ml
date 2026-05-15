package store

import (
	"context"
	"time"
)

// UpsertWorkflowRun inserts or updates a single run keyed by (repo_id, run_id).
//
// The collector calls this for every page of GitHub results. Idempotent —
// running the same fetch twice produces the same DB state.
func (d *DB) UpsertWorkflowRun(ctx context.Context, repoID int64, in UpsertWorkflowRunParams) (int64, error) {
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO workflow_runs (
		    repo_id, run_id, workflow_name, head_branch, head_sha,
		    event, status, conclusion, actor, created_at, run_started_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (repo_id, run_id) DO UPDATE SET
		    workflow_name = EXCLUDED.workflow_name,
		    status        = EXCLUDED.status,
		    conclusion    = EXCLUDED.conclusion,
		    run_started_at = EXCLUDED.run_started_at,
		    updated_at    = EXCLUDED.updated_at
		RETURNING id
	`,
		repoID, in.RunID, nilIfEmpty(in.WorkflowName), nilIfEmpty(in.HeadBranch),
		in.HeadSHA, nilIfEmpty(in.Event), nilIfEmpty(in.Status), nilIfEmpty(in.Conclusion),
		nilIfEmpty(in.Actor), in.CreatedAt, in.RunStartedAt, in.UpdatedAt,
	)
	var id int64
	return id, row.Scan(&id)
}

type UpsertWorkflowRunParams struct {
	RunID         int64
	WorkflowName  string
	HeadBranch    string
	HeadSHA       string
	Event         string
	Status        string
	Conclusion    string
	Actor         string
	CreatedAt     time.Time
	RunStartedAt  *time.Time
	UpdatedAt     time.Time
}

// UpsertJob keyed by (run_id, name, github_job_id). Returns the local id.
func (d *DB) UpsertJob(ctx context.Context, runDBID int64, in UpsertJobParams) (int64, error) {
	row := d.Pool.QueryRow(ctx, `
		INSERT INTO jobs (
		    run_id, github_job_id, name, status, conclusion,
		    started_at, completed_at, duration_sec,
		    runner_name, runner_group, labels, steps_count
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (run_id, name, github_job_id) DO UPDATE SET
		    status       = EXCLUDED.status,
		    conclusion   = EXCLUDED.conclusion,
		    started_at   = EXCLUDED.started_at,
		    completed_at = EXCLUDED.completed_at,
		    duration_sec = EXCLUDED.duration_sec,
		    runner_name  = EXCLUDED.runner_name,
		    runner_group = EXCLUDED.runner_group,
		    labels       = EXCLUDED.labels,
		    steps_count  = EXCLUDED.steps_count
		RETURNING id
	`,
		runDBID, in.GithubJobID, in.Name, nilIfEmpty(in.Status), nilIfEmpty(in.Conclusion),
		in.StartedAt, in.CompletedAt, in.DurationSec,
		nilIfEmpty(in.RunnerName), nilIfEmpty(in.RunnerGroup), in.Labels, in.StepsCount,
	)
	var id int64
	return id, row.Scan(&id)
}

type UpsertJobParams struct {
	GithubJobID int64
	Name        string
	Status      string
	Conclusion  string
	StartedAt   *time.Time
	CompletedAt *time.Time
	DurationSec *int
	RunnerName  string
	RunnerGroup string
	Labels      []string
	StepsCount  *int
}

// UpdateRepoSyncCounters refreshes the denormalised totals shown in the
// /datasets cards. Called periodically by the collector at the end of
// each page, not after every row.
func (d *DB) UpdateRepoSyncCounters(ctx context.Context, repoID int64) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE repos r SET
		  runs_count   = COALESCE((SELECT COUNT(*) FROM workflow_runs WHERE repo_id = r.id), 0),
		  jobs_count   = COALESCE((SELECT COUNT(*) FROM jobs j JOIN workflow_runs w ON j.run_id = w.id WHERE w.repo_id = r.id), 0),
		  oldest_run_at = (SELECT MIN(created_at) FROM workflow_runs WHERE repo_id = r.id),
		  newest_run_at = (SELECT MAX(created_at) FROM workflow_runs WHERE repo_id = r.id),
		  last_synced_at = now()
		WHERE r.id = $1
	`, repoID)
	return err
}

// SetRepoStatus drives the chip on the dataset card.
func (d *DB) SetRepoStatus(ctx context.Context, repoID int64, status string, lastError string) error {
	_, err := d.Pool.Exec(ctx, `
		UPDATE repos SET status = $2, last_error = NULLIF($3, '') WHERE id = $1
	`, repoID, status, lastError)
	return err
}

// LookupRepo finds a tracked repo by owner/name. Returns pgx.ErrNoRows if missing.
func (d *DB) LookupRepo(ctx context.Context, owner, name string) (Repo, error) {
	row := d.Pool.QueryRow(ctx, `
		SELECT id, owner, name, github_id, default_branch, tracked_branches,
		       status, last_synced_at, oldest_run_at, newest_run_at,
		       runs_count, jobs_count, last_error, is_seed, added_at
		FROM repos WHERE owner = $1 AND name = $2
	`, owner, name)
	return scanRepo(row)
}

// nilIfEmpty turns "" into nil so NULLABLE columns store NULL instead of
// empty strings — keeps queries like `WHERE conclusion IS NULL` honest.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
