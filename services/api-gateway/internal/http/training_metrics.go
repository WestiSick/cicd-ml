package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// GET /api/training/:id/metrics
//
// Returns the per-iteration metric stream for a training run. The frontend
// hits this on page load to seed the chart, then subscribes to
// /ws/training/:id for live updates from then on.
func (s *Server) listTrainingMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Training id must be numeric", "")
		return
	}
	metrics, err := s.db.ListTrainingMetrics(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "metrics_load_failed",
			"Could not load training metrics", "Refresh the page.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": metrics})
}

// POST /api/internal/training/:id/metric
//
// Internal endpoint — only ml-service should call it. Records one
// iteration row, broadcasts the same event on /ws/training/:id, and
// returns 204. We don't authenticate this for now (single-host compose
// network); when we move to k8s the network policy gates access.
func (s *Server) postTrainingMetric(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Training id must be numeric", "")
		return
	}
	var body struct {
		Iteration int     `json:"iteration"`
		TrainLoss float64 `json:"train_loss"`
		ValMAE    float64 `json:"val_mae"`
		ValRMSE   float64 `json:"val_rmse"`
		ValMAPE   float64 `json:"val_mape"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "Body is not valid JSON", "")
		return
	}
	if err := s.db.InsertTrainingMetric(r.Context(), store.InsertTrainingMetricParams{
		TrainingJobID: id,
		Iteration:     body.Iteration,
		TrainLoss:     body.TrainLoss,
		ValMAE:        body.ValMAE,
		ValRMSE:       body.ValRMSE,
		ValMAPE:       body.ValMAPE,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "metric_persist_failed",
			"Could not persist metric", "")
		return
	}
	s.hub.PublishJSON("training/"+strconv.FormatInt(id, 10), "metric", body)
	w.WriteHeader(http.StatusNoContent)
}
