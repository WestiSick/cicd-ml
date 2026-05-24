package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// GET /api/queue/history?limit=200&repo=owner/name&hours=168&min_abs_delta=30
//
// Persistent log of every workflow_run.completed the webhook handler
// observed — predicted vs actual side-by-side, the calibration factor
// in effect at the time, and the resulting δ%. Backs the /history
// page on the frontend.
//
// Filters:
//   limit          1..500            default 100
//   repo           "owner/name"      exact match, optional
//   hours          1..720 (30 days)  default 168 (7 days). 0 = all
//   min_abs_delta  0..1000           |delta_pct| floor for "show me the misses"
//
// The 30-day ceiling is a guardrail — the JSON response stays under
// ~200KB even at 500 rows × max-everything, no risk of accidental
// browser-killing fetches. For longer windows export CSV.
func (s *Server) queueHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 100
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	hours := 168
	if v := q.Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 720 {
			hours = n
		}
	}

	minAbsDelta := 0.0
	if v := q.Get("min_abs_delta"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 && n <= 1000 {
			minAbsDelta = n
		}
	}

	params := store.ListPredictionLogParams{
		Repo:           q.Get("repo"),
		MinAbsDeltaPct: minAbsDelta,
		Limit:          limit,
	}
	if hours > 0 {
		params.Since = time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	}

	rows, err := s.db.ListPredictionLog(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history_query_failed",
			"Could not load prediction history", "Try refreshing the page.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rows":  rows,
		"limit": limit,
		"hours": hours,
	})
}
