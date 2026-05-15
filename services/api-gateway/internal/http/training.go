package http

import (
	"encoding/json"
	"net/http"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// POST /api/training
//
// Body:
//
//	{
//	  "algo":     "xgboost",            // required
//	  "params":   {"max_depth": 6, ...} // optional
//	  "repo_ids": [1, 2],               // optional
//	  "activate": true                  // optional
//	}
//
// Enqueues a `train_model` bg_job. Returns the bg_job id; the frontend
// subscribes to /ws/bg-jobs and follows progress like every other
// long-running task. Same UX as setup or refresh.
func (s *Server) startTraining(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Algo         string         `json:"algo"`
		Params       map[string]any `json:"params"`
		RepoIDs      []int64        `json:"repo_ids"`
		Activate     bool           `json:"activate"`
		Name         string         `json:"name"`
		OptunaTrials int            `json:"optuna_trials"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Could not decode training request", "Re-check the form and resubmit.")
		return
	}
	if body.Algo == "" {
		writeError(w, http.StatusBadRequest, "missing_algo",
			"Algorithm is required", "Pick one of linear / rf / xgboost / lightgbm.")
		return
	}

	job, err := s.db.EnqueueBGJob(r.Context(), store.JobKindTrainModel, body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enqueue_failed",
			"Could not enqueue training job", "Try again — the database may be temporarily busy.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "training_start", body.Algo,
		"training queued", true, map[string]any{"job_id": job.ID})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"bg_job_id": job.ID,
		"message":   "Training queued — watch /ws/bg-jobs for progress.",
	})
}
