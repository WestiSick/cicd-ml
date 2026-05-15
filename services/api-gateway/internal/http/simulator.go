package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/scheduler"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
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
	if len(req.Strategies) == 0 {
		req.Strategies = scheduler.AvailableStrategies()
	}
	if req.Runners <= 0 {
		req.Runners = 1
	}

	// Default to "last 7 days" when no window was provided.
	end := time.Now().UTC()
	if req.WindowEnd != nil {
		end = *req.WindowEnd
	}
	start := end.Add(-7 * 24 * time.Hour)
	if req.WindowStart != nil {
		start = *req.WindowStart
	}
	if !end.After(start) {
		writeError(w, http.StatusBadRequest, "invalid_window",
			"window_end must be after window_start", "Pick a non-empty time range.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	rawJobs, err := s.db.LoadSimWindow(ctx, start, end, req.RepoIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_window_failed",
			"Could not load jobs in the chosen window", "Try a shorter range or refresh datasets.")
		return
	}
	if len(rawJobs) == 0 {
		writeError(w, http.StatusBadRequest, "no_jobs_in_window",
			"No completed jobs were collected in the chosen window",
			"Extend the time range, or sync more data on /datasets.")
		return
	}

	jobs := projectToSim(rawJobs, time.Duration(req.SLAMainSec)*time.Second, time.Duration(req.SLAFeatureSec)*time.Second)

	cfg := scheduler.SimulatorConfig{Runners: req.Runners}
	weights := s.getCustomWeights(ctx)

	results := []scheduler.Metrics{}
	for _, name := range req.Strategies {
		strat := scheduler.New(name, weights)
		if strat == nil {
			continue
		}
		m := scheduler.Run(strat, jobs, cfg)

		if _, err := s.db.InsertSimRun(ctx, store.InsertSimRunParams{
			Strategy:         m.Strategy,
			WindowStart:      m.WindowStart,
			WindowEnd:        m.WindowEnd,
			Repos:            req.RepoIDs,
			JobsCount:        m.JobsCount,
			MakespanSec:      m.MakespanSec,
			WaitP50Sec:       m.WaitP50Sec,
			WaitP95Sec:       m.WaitP95Sec,
			ThroughputPerMin: m.ThroughputPerMin,
			SLAViolations:    m.SLAViolations,
			Extra: map[string]any{
				"runners":       req.Runners,
				"sla_main_sec":  req.SLAMainSec,
				"sla_feature_sec": req.SLAFeatureSec,
				"wait_mean_sec": m.WaitMeanSec,
			},
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "persist_sim_failed",
				"Could not save simulation results", "Try again — the previous strategy may have been recorded.")
			return
		}
		results = append(results, m)
	}

	_ = s.db.RecordActivity(ctx, "user", "simulator_run", "",
		"simulator completed", true, map[string]any{
			"strategies": req.Strategies,
			"jobs":       len(jobs),
		})

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":    len(jobs),
		"runners": req.Runners,
		"results": results,
	})
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

// projectToSim converts DB rows to scheduler input, filling in:
//   - PredictedSec from the latest prediction, falling back to actual
//     (oracle mode — useful before any ML model is trained).
//   - Deadline from per-branch SLA budgets; zero if both budgets are 0.
func projectToSim(rows []store.SimInputJob, slaMain, slaFeature time.Duration) []scheduler.SimulatorJob {
	out := make([]scheduler.SimulatorJob, 0, len(rows))
	for _, r := range rows {
		if r.ActualSec == nil {
			continue
		}
		actual := float64(*r.ActualSec)
		predicted := actual
		if r.PredictedSec != nil {
			predicted = *r.PredictedSec
		}
		var deadline time.Time
		if slaMain > 0 || slaFeature > 0 {
			budget := slaFeature
			if isMainLike(r.Branch) {
				budget = slaMain
			}
			if budget > 0 {
				deadline = r.ArrivedAt.Add(budget)
			}
		}
		out = append(out, scheduler.SimulatorJob{
			ID:           r.ID,
			Repo:         r.Repo,
			Branch:       r.Branch,
			ArrivedAt:    r.ArrivedAt,
			PredictedSec: predicted,
			ActualSec:    actual,
			Deadline:     deadline,
		})
	}
	return out
}

func isMainLike(branch string) bool {
	switch branch {
	case "main", "master":
		return true
	}
	if len(branch) >= 7 && branch[:7] == "release" {
		return true
	}
	return false
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
