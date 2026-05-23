package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/ml"
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
		Since        string         `json:"since"`
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

// POST /api/training/cv
//
// Synchronous walk-forward cross-validation. Unlike /api/training this
// doesn't enqueue a bg_job — CV is a *single* call that takes 5-30s
// (typical) and the response IS the result the UI needs to render the
// CV table. The frontend treats it like a regular ml-service call.
//
// Body:
//
//	{
//	  "algo":     "xgboost",
//	  "params":   {...},
//	  "repo_ids": [1,2],
//	  "n_splits": 5
//	}
//
// Returns the CV summary: per-fold metrics + mean ± std.
func (s *Server) crossValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Algo    string         `json:"algo"`
		Params  map[string]any `json:"params"`
		RepoIDs []int64        `json:"repo_ids"`
		Since   string         `json:"since"`
		NSplits int            `json:"n_splits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Could not decode CV request", "")
		return
	}
	if body.Algo == "" {
		writeError(w, http.StatusBadRequest, "missing_algo",
			"Algorithm is required", "Pick one of linear / rf / xgboost / lightgbm / mlp.")
		return
	}
	if body.NSplits == 0 {
		body.NSplits = 5
	}

	// 5-minute ceiling — XGBoost on the full dataset with 5 folds is the
	// upper bound; everything else finishes faster.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	resp, err := s.mlClient.CrossValidate(ctx, ml.CVRequest{
		Algo:    body.Algo,
		Params:  body.Params,
		RepoIDs: body.RepoIDs,
		Since:   body.Since,
		NSplits: body.NSplits,
	})
	if err != nil {
		if apiErr, ok := err.(*ml.APIError); ok {
			writeError(w, apiErr.StatusCode, apiErr.Code, apiErr.Message, apiErr.UserAction)
			return
		}
		writeError(w, http.StatusBadGateway, "ml_service_unreachable",
			"Could not reach ml-service", "Check that the ml container is healthy.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "cv_run", body.Algo,
		"cross-validation completed", true,
		map[string]any{"n_splits": resp.NSplits, "mae_mean": resp.MeanMetrics["mae_test_sec"]})

	writeJSON(w, http.StatusOK, resp)
}
