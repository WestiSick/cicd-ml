package http

import (
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/datasets/{id}/export.csv
//
// Streams one CSV row per job for the repo. Columns are the same
// numeric + categorical signals the ML pipeline sees, plus the actual
// duration. Lets the user open the dataset in Excel / pandas without
// writing any SQL.
//
// We stream row-by-row through pgx rather than buffering — even at our
// scale (~50K jobs) a CSV is small (~5MB), but streaming keeps memory
// flat for repos that grow into the millions later.
func (s *Server) exportDatasetCSV(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Dataset id must be numeric", "")
		return
	}

	repo, err := s.db.LookupRepoByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "repo_not_found",
				"No repository with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed", "", "")
		return
	}

	rows, err := s.db.Pool.Query(r.Context(), `
		SELECT
		  j.id,
		  j.name,
		  j.duration_sec,
		  j.runner_name,
		  j.runner_group,
		  j.steps_count,
		  j.status,
		  j.conclusion,
		  j.started_at,
		  j.completed_at,
		  w.workflow_name,
		  w.head_branch,
		  w.head_sha,
		  w.event,
		  w.actor,
		  w.created_at,
		  c.files_changed,
		  c.additions,
		  c.deletions
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		LEFT JOIN commits c  ON w.head_sha = c.sha
		WHERE w.repo_id = $1
		ORDER BY w.created_at DESC
	`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv_query_failed",
			"Could not load jobs for export", "Try again — the table may be temporarily locked.")
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+repo.Owner+"_"+repo.Name+`_jobs.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Header — keep field names stable across exports so downstream
	// notebooks don't break when we add columns.
	_ = cw.Write([]string{
		"job_id", "job_name", "duration_sec",
		"runner_name", "runner_group", "steps_count",
		"status", "conclusion",
		"started_at", "completed_at",
		"workflow_name", "head_branch", "head_sha", "event", "actor",
		"run_created_at",
		"commit_files_changed", "commit_additions", "commit_deletions",
	})

	for rows.Next() {
		var (
			jobID                                           int64
			jobName                                         string
			duration                                        *int
			runnerName, runnerGroup                         *string
			steps                                           *int
			status, conclusion                              *string
			startedAt, completedAt, createdAt               *time.Time
			workflowName, headBranch, headSHA, event, actor *string
			filesChanged, additions, deletions              *int
		)
		if err := rows.Scan(
			&jobID, &jobName, &duration,
			&runnerName, &runnerGroup, &steps,
			&status, &conclusion,
			&startedAt, &completedAt,
			&workflowName, &headBranch, &headSHA, &event, &actor,
			&createdAt,
			&filesChanged, &additions, &deletions,
		); err != nil {
			continue
		}
		_ = cw.Write([]string{
			strconv.FormatInt(jobID, 10),
			jobName,
			intOrEmpty(duration),
			strOrEmpty(runnerName),
			strOrEmpty(runnerGroup),
			intOrEmpty(steps),
			strOrEmpty(status),
			strOrEmpty(conclusion),
			timeOrEmpty(startedAt),
			timeOrEmpty(completedAt),
			strOrEmpty(workflowName),
			strOrEmpty(headBranch),
			strOrEmpty(headSHA),
			strOrEmpty(event),
			strOrEmpty(actor),
			timeOrEmpty(createdAt),
			intOrEmpty(filesChanged),
			intOrEmpty(additions),
			intOrEmpty(deletions),
		})
	}
}

// GET /api/simulator/runs/{id}/export.csv
//
// Streams the rows of one sim_runs entry as CSV. Specifically, it
// exports the per-strategy metrics block stored in `sim_runs.extra`
// plus the canonical columns — a flat-table view that pgfplotstable or
// pandas can ingest directly for thesis Chapter 4.
func (s *Server) exportSimRunCSV(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Simulation id must be numeric", "")
		return
	}
	run, err := s.db.GetSimRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "sim_run_not_found",
				"No simulation with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "sim_lookup_failed", "", "")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="sim_run_`+strconv.FormatInt(id, 10)+"_"+run.Strategy+`.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"strategy", "window_start", "window_end", "jobs_count",
		"makespan_sec", "wait_p50_sec", "wait_p95_sec",
		"throughput_per_min", "sla_violations",
	})
	_ = cw.Write([]string{
		run.Strategy,
		run.WindowStart.UTC().Format(time.RFC3339),
		run.WindowEnd.UTC().Format(time.RFC3339),
		strconv.Itoa(run.JobsCount),
		floatPtrOrEmpty(run.MakespanSec, 1),
		floatPtrOrEmpty(run.WaitP50Sec, 1),
		floatPtrOrEmpty(run.WaitP95Sec, 1),
		floatPtrOrEmpty(run.ThroughputPerMin, 3),
		intPtrOrEmpty(run.SLAViolations),
	})
}

func floatPtrOrEmpty(f *float64, prec int) string {
	if f == nil {
		return ""
	}
	return strconv.FormatFloat(*f, 'f', prec, 64)
}
func intPtrOrEmpty(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

// ---- value formatters ----

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
func intOrEmpty(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}
func timeOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
