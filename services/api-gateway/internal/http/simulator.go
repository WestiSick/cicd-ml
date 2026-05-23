package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/scheduler"
	"github.com/buzdin/cicd-ml/api-gateway/internal/simrun"
)

// POST /api/simulator/run
//
// Request body:
//
//	{
//	  "window_start": "2026-04-01T00:00:00Z",
//	  "window_end":   "2026-05-01T00:00:00Z",
//	  "repo_ids":     [1, 2],            // optional, empty = all
//	  "strategies":   ["fifo","sjf","edf","custom"],
//	  "runners":      2,                  // optional, default 1
//	  "sla_main_sec":   600,              // optional deadline budget for main/release
//	  "sla_feature_sec":3600              // optional deadline budget for feature/dev/*
//	}
//
// Response: an array of Metrics, one per requested strategy.
//
// Synchronous on purpose: even 10k jobs is sub-second in the event-driven
// engine. If we ever blow past that, we'll move it into bg_jobs and stream
// progress — but premature optimisation here would just add latency for
// the common case.
func (s *Server) runSimulator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WindowStart   *time.Time `json:"window_start"`
		WindowEnd     *time.Time `json:"window_end"`
		RepoIDs       []int64    `json:"repo_ids"`
		Strategies    []string   `json:"strategies"`
		Runners       int        `json:"runners"`
		SLAMainSec    int        `json:"sla_main_sec"`
		SLAFeatureSec int        `json:"sla_feature_sec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Could not decode simulator request", "Re-check the form and resubmit.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	params := simrun.NewParams(simrun.Params{
		WindowStart:   coalesceTime(req.WindowStart),
		WindowEnd:     coalesceTime(req.WindowEnd),
		RepoIDs:       req.RepoIDs,
		Strategies:    req.Strategies,
		Runners:       req.Runners,
		SLAMainSec:    req.SLAMainSec,
		SLAFeatureSec: req.SLAFeatureSec,
		CustomWeights: s.getCustomWeights(ctx),
	})

	out, err := simrun.Run(ctx, s.db, params)
	if err != nil {
		// Map errors to user-actionable envelopes.
		var iw *simrun.InvalidWindowError
		switch {
		case errors.As(err, &iw):
			writeError(w, http.StatusBadRequest, "invalid_window",
				err.Error(), "Pick a non-empty time range.")
		case errors.As(err, new(simrun.ErrNoJobs)):
			writeError(w, http.StatusBadRequest, "no_jobs_in_window",
				"No completed jobs were collected in the chosen window",
				"Extend the time range, or sync more data on /datasets.")
		default:
			writeError(w, http.StatusInternalServerError, "simulate_failed",
				"Simulator run failed", "Try a shorter range or rerun in a few seconds.")
		}
		return
	}

	_ = s.db.RecordActivity(ctx, "user", "simulator_run", "",
		"simulator completed", true, map[string]any{
			"strategies": params.Strategies,
			"jobs":       out.JobsRun,
		})

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":    out.JobsRun,
		"runners": params.Runners,
		"results": out.Metrics,
	})
}

// coalesceTime turns a possibly-nil pointer into a value or zero time.
// Used to translate the HTTP request's optional fields into the
// non-pointer Params shape simrun expects.
func coalesceTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// GET /api/simulator/runs?limit=100
func (s *Server) listSimRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.db.ListSimRuns(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_sim_runs_failed",
			"Could not load past simulations", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": rows})
}

// GET /api/simulator/strategies
func (s *Server) listStrategies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"strategies": scheduler.AvailableStrategies(),
	})
}

// getCustomWeights reads the configured weights from system_state. Falls
// back to defaults if the row is missing or malformed.
func (s *Server) getCustomWeights(ctx context.Context) scheduler.CustomWeights {
	var raw []byte
	err := s.db.Pool.QueryRow(ctx, `SELECT value FROM system_state WHERE key = 'custom_weights'`).Scan(&raw)
	if err != nil {
		return scheduler.DefaultCustomWeights()
	}
	var w scheduler.CustomWeights
	if err := json.Unmarshal(raw, &w); err != nil {
		return scheduler.DefaultCustomWeights()
	}
	if w.ShortJob == 0 && w.Deadline == 0 && w.Branch == 0 {
		return scheduler.DefaultCustomWeights()
	}
	return w
}
