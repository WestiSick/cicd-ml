package http

import (
	"encoding/json"
	"net/http"

	"github.com/buzdin/cicd-ml/api-gateway/internal/bootstrap"
)

// POST /api/setup/start
//
// Body shape mirrors the form in /setup. Returns the bootstrap bg_job id
// so the frontend can subscribe to /ws/bg-jobs and follow progress.
func (s *Server) startSetup(w http.ResponseWriter, r *http.Request) {
	var req bootstrap.SetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Setup form payload was not valid JSON",
			"Reload the page and try again.")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_setup",
			err.Error(),
			"Adjust the form and resubmit.")
		return
	}

	jobID, err := s.orches.Start(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "setup_failed",
			"Could not start setup: "+err.Error(),
			"Check the API logs and try again. If a repo URL looks wrong, fix it on the form.")
		return
	}

	_ = s.db.RecordActivity(r.Context(), "user", "setup_start", "",
		"initial setup queued", true, map[string]any{"job_id": jobID})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"bg_job_id": jobID,
		"message":   "Setup queued — watch /ws/bg-jobs for progress.",
	})
}
