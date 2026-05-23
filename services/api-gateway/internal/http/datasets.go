package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/datasets
//
// Summary across the whole dataset — feeds the /datasets page header card.
// Cheap aggregates that don't require touching the jobs table at row level.
// Per-repo stats are still in the listRepos response (denormalised counts).
func (s *Server) datasetsSummary(w http.ResponseWriter, r *http.Request) {
	var (
		repoCount int
		runCount  int64
		jobCount  int64
	)
	row := s.db.Pool.QueryRow(r.Context(), `
		SELECT
			(SELECT COUNT(*) FROM repos),
			COALESCE((SELECT SUM(runs_count) FROM repos), 0),
			COALESCE((SELECT SUM(jobs_count) FROM repos), 0)
	`)
	if err := row.Scan(&repoCount, &runCount, &jobCount); err != nil {
		writeError(w, http.StatusInternalServerError, "datasets_summary_failed",
			"Could not load dataset summary", "Try refreshing the page.")
		return
	}

	// Feature coverage: how many jobs have a row in `features`. The /datasets
	// page warns when coverage is far below jobs_count (means a feature
	// rebuild is pending).
	var featuresCount int64
	_ = s.db.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM features`).Scan(&featuresCount)

	writeJSON(w, http.StatusOK, map[string]any{
		"repo_count":     repoCount,
		"run_count":      runCount,
		"job_count":      jobCount,
		"features_count": featuresCount,
	})
}

// GET /api/datasets/{id}
//
// Per-repo dataset detail: distribution of durations, top workflows,
// success/fail counts, branch breakdown, time coverage. Powers the
// `/datasets/:id` page in the frontend.
//
// Single query per metric — Postgres is fast enough at this scale that
// caching would just add a cache-invalidation problem. The collector
// updates the denormalised counts on `repos` separately for the listing.
func (s *Server) datasetDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Dataset id must be numeric", "Check the URL — should be /datasets/<numeric-id>.")
		return
	}

	// 1. Repo header — also serves as existence check.
	repo, err := s.db.LookupRepoByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "repo_not_found",
				"No repository with that id", "Reload /datasets to refresh the list.")
			return
		}
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed",
			"Could not load repository", "Try again in a second.")
		return
	}

	ctx := r.Context()
	out := map[string]any{
		"repo":              repo,
		"duration_buckets":  s.datasetDurationHistogram(ctx, id),
		"top_workflows":     s.datasetTopWorkflows(ctx, id, 10),
		"top_jobs":          s.datasetTopJobs(ctx, id, 10),
		"branch_breakdown":  s.datasetBranchBreakdown(ctx, id, 8),
		"conclusion_counts": s.datasetConclusionCounts(ctx, id),
	}
	writeJSON(w, http.StatusOK, out)
}

// Duration histogram: log-binned because CI durations span 4+ orders of
// magnitude. Buckets: <10s, 10-30s, 30-60s, 1-2m, 2-5m, 5-10m, 10-30m, 30m+.
// The thesis Chapter 3 cites this distribution; keep bucket boundaries
// stable across runs.
func (s *Server) datasetDurationHistogram(ctx context.Context, repoID int64) []map[string]any {
	buckets := []struct {
		Label  string
		Lo, Hi int
	}{
		{"<10s", 0, 10},
		{"10–30s", 10, 30},
		{"30–60s", 30, 60},
		{"1–2m", 60, 120},
		{"2–5m", 120, 300},
		{"5–10m", 300, 600},
		{"10–30m", 600, 1800},
		{"30m+", 1800, 1 << 30},
	}
	out := make([]map[string]any, 0, len(buckets))
	for _, b := range buckets {
		var c int64
		_ = s.db.Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM jobs j
			JOIN workflow_runs w ON j.run_id = w.id
			WHERE w.repo_id = $1
			  AND j.duration_sec IS NOT NULL
			  AND j.duration_sec >= $2 AND j.duration_sec < $3
		`, repoID, b.Lo, b.Hi).Scan(&c)
		out = append(out, map[string]any{
			"label": b.Label,
			"lo":    b.Lo,
			"hi":    b.Hi,
			"count": c,
		})
	}
	return out
}

func (s *Server) datasetTopWorkflows(ctx context.Context, repoID int64, k int) []map[string]any {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT w.workflow_name,
		       COUNT(j.id)                                         AS runs,
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY j.duration_sec), 0)  AS p50,
		       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY j.duration_sec), 0) AS p95
		FROM workflow_runs w
		JOIN jobs j ON j.run_id = w.id
		WHERE w.repo_id = $1
		  AND j.duration_sec IS NOT NULL
		GROUP BY w.workflow_name
		ORDER BY runs DESC
		LIMIT $2
	`, repoID, k)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name string
		var runs int64
		var p50, p95 float64
		if err := rows.Scan(&name, &runs, &p50, &p95); err == nil {
			out = append(out, map[string]any{
				"name": name, "runs": runs, "p50_sec": p50, "p95_sec": p95,
			})
		}
	}
	return out
}

func (s *Server) datasetTopJobs(ctx context.Context, repoID int64, k int) []map[string]any {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT j.name,
		       COUNT(*)                                             AS runs,
		       COALESCE(AVG(j.duration_sec), 0)                     AS mean_sec,
		       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY j.duration_sec), 0) AS p50_sec
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		WHERE w.repo_id = $1
		  AND j.duration_sec IS NOT NULL
		GROUP BY j.name
		ORDER BY runs DESC
		LIMIT $2
	`, repoID, k)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name string
		var runs int64
		var mean, p50 float64
		if err := rows.Scan(&name, &runs, &mean, &p50); err == nil {
			out = append(out, map[string]any{
				"name": name, "runs": runs, "mean_sec": mean, "p50_sec": p50,
			})
		}
	}
	return out
}

func (s *Server) datasetBranchBreakdown(ctx context.Context, repoID int64, k int) []map[string]any {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT w.head_branch,
		       COUNT(j.id) AS runs,
		       COALESCE(AVG(j.duration_sec), 0) AS mean_sec
		FROM workflow_runs w
		JOIN jobs j ON j.run_id = w.id
		WHERE w.repo_id = $1
		  AND w.head_branch IS NOT NULL
		  AND j.duration_sec IS NOT NULL
		GROUP BY w.head_branch
		ORDER BY runs DESC
		LIMIT $2
	`, repoID, k)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var branch string
		var runs int64
		var mean float64
		if err := rows.Scan(&branch, &runs, &mean); err == nil {
			out = append(out, map[string]any{
				"branch": branch, "runs": runs, "mean_sec": mean,
			})
		}
	}
	return out
}

func (s *Server) datasetConclusionCounts(ctx context.Context, repoID int64) map[string]int64 {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT COALESCE(j.conclusion, 'unknown'), COUNT(*)
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		WHERE w.repo_id = $1
		GROUP BY j.conclusion
	`, repoID)
	if err != nil {
		return map[string]int64{}
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var k string
		var c int64
		if err := rows.Scan(&k, &c); err == nil {
			out[k] = c
		}
	}
	return out
}

// GET /api/queue
//
// Snapshot of the in-flight queue (jobs scored by the active model but not
// yet completed). The /dashboard page subscribes to /ws/queue for live
// updates; this REST handler is for the initial render and for clients
// that don't speak WebSocket.
func (s *Server) queueSnapshot(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	type queueRow struct {
		JobID        int64    `json:"job_id"`
		Repo         string   `json:"repo"`
		Workflow     string   `json:"workflow"`
		JobName      string   `json:"job_name"`
		HeadBranch   *string  `json:"head_branch,omitempty"`
		HeadSHA      *string  `json:"head_sha,omitempty"`
		Status       string   `json:"status"`
		Conclusion   *string  `json:"conclusion,omitempty"`
		PredictedSec *float64 `json:"predicted_sec,omitempty"`
		ActualSec    *int     `json:"actual_sec,omitempty"`
	}

	rows, err := s.db.Pool.Query(r.Context(), `
		SELECT
			j.id,
			r.owner || '/' || r.name AS repo,
			w.workflow_name,
			j.name,
			w.head_branch,
			w.head_sha,
			j.status,
			j.conclusion,
			p.predicted_sec,
			j.duration_sec
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		JOIN repos r         ON w.repo_id = r.id
		LEFT JOIN LATERAL (
			SELECT predicted_sec FROM predictions p2
			WHERE p2.job_id = j.id
			ORDER BY p2.made_at DESC
			LIMIT 1
		) p ON TRUE
		WHERE j.status IN ('queued','in_progress','requested','waiting')
		   OR (j.status = 'completed' AND j.completed_at > now() - interval '5 minutes')
		ORDER BY COALESCE(j.started_at, w.created_at) DESC
		LIMIT $1
	`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "queue_snapshot_failed",
			"Could not load queue snapshot", "Refresh the page.")
		return
	}
	defer rows.Close()

	out := []queueRow{}
	for rows.Next() {
		var q queueRow
		if err := rows.Scan(
			&q.JobID, &q.Repo, &q.Workflow, &q.JobName,
			&q.HeadBranch, &q.HeadSHA, &q.Status, &q.Conclusion,
			&q.PredictedSec, &q.ActualSec,
		); err == nil {
			out = append(out, q)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue": out})
}
