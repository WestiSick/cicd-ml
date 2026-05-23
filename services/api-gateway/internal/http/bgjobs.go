package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/bg-jobs?status=running&limit=100
func (s *Server) listBGJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	jobs, err := s.db.ListBGJobs(r.Context(), status, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_bg_jobs_failed",
			"Could not load background jobs", "Try refreshing the page.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

// GET /api/bg-jobs/:id
func (s *Server) getBGJob(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Background job id must be numeric", "")
		return
	}

	job, err := s.db.GetBGJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "bg_job_not_found",
				"No background job with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "get_bg_job_failed",
			"Could not load background job", "")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// POST /api/bg-jobs/{id}/cancel
//
// Marks the job as cancelled. The worker that owns it checks the cancel
// flag at each progress callback (cooperative cancellation) and bails out
// cleanly. Queued-but-not-started jobs are cancelled immediately and
// never picked up. Already-done/failed jobs return a 409.
func (s *Server) cancelBGJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Background job id must be numeric", "")
		return
	}
	flipped, err := s.db.CancelBGJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cancel_failed",
			"Could not cancel job", "Retry — the job may have finished.")
		return
	}
	if !flipped {
		writeError(w, http.StatusConflict, "not_cancellable",
			"Job already finished or never existed",
			"Reload the page to see the current status.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "cancel_bg_job", strconv.FormatInt(id, 10),
		"job cancelled", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

// GET /api/activity?limit=100
func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.db.ListActivity(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_activity_failed",
			"Could not load activity log", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
