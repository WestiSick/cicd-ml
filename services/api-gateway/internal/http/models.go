package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/models — list trained models, newest first.
func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.db.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_models_failed",
			"Could not load models", "Try refreshing the page.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// POST /api/models/{id}/activate — set as the active model used by the
// scheduler and the simulator's PredictedSec input.
func (s *Server) activateModel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Model id must be numeric", "")
		return
	}
	if _, err := s.db.GetModel(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "model_not_found",
				"No model with that id", "Reload /experiments.")
			return
		}
		writeError(w, http.StatusInternalServerError, "model_lookup_failed",
			"Could not fetch model", "")
		return
	}
	if err := s.db.SetActiveModel(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "activate_failed",
			"Could not activate model", "Retry — the previous active model is still set.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "activate_model", strconv.FormatInt(id, 10),
		"model activated", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"active_model_id": id})
}

// GET /api/models/{id} — single model with full feature_importance.
//
// Separate endpoint (vs ListModels) keeps the list response slim. The
// detail page is the only place the full importance dict is needed.
func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Model id must be numeric", "")
		return
	}
	m, err := s.db.GetModel(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "model_not_found", "No model with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "model_lookup_failed", "Could not load model", "")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// GET /api/models/{id}/feature-importance?top=20
//
// Returns the top-K features by importance value, sorted descending.
// Used by the horizontal bar chart on the model detail page.
func (s *Server) getModelFeatureImportance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Model id must be numeric", "")
		return
	}
	topK := 20
	if q := r.URL.Query().Get("top"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			topK = n
		}
	}
	m, err := s.db.GetModel(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "model_not_found", "No model with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "model_lookup_failed", "", "")
		return
	}

	importance := map[string]float64{}
	_ = json.Unmarshal(m.FeatureImportance, &importance)

	type item struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	}
	items := make([]item, 0, len(importance))
	for k, v := range importance {
		items = append(items, item{Name: k, Value: v})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Value > items[j].Value })
	if len(items) > topK {
		items = items[:topK]
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": items})
}

// DELETE /api/models/{id}
//
// Removes the model row and (via FK CASCADE) every prediction it ever
// made. The active model can't be deleted — the UI grays the button.
// Best-effort: the artifact file on disk is removed too; failure there is
// logged but doesn't roll back the DB delete (a stale .joblib is harmless).
func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Model id must be numeric", "")
		return
	}
	m, err := s.db.GetModel(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "model_not_found", "No model with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "model_lookup_failed", "", "")
		return
	}
	preds, err := s.db.DeleteModel(r.Context(), id)
	if err != nil {
		if err.Error() == "cannot delete the currently active model" {
			writeError(w, http.StatusConflict, "model_is_active",
				"Cannot delete the currently active model",
				"Activate a different model first, then try deleting.")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete_model_failed",
			"Could not delete model", "Retry — the predictions may be partially gone.")
		return
	}
	// Best-effort artifact cleanup. The path is stored relative to models_dir.
	if m.ArtifactPath != nil && *m.ArtifactPath != "" {
		full := filepath.Join(s.cfg.ModelsDir, *m.ArtifactPath)
		_ = os.Remove(full) // ignore errors; logged via activity
	}
	_ = s.db.RecordActivity(r.Context(), "user", "delete_model", strconv.FormatInt(id, 10),
		"model deleted", true, map[string]any{"predictions_deleted": preds})
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":             true,
		"predictions_deleted": preds,
	})
}

// GET /api/models/{id}/download
//
// Streams the joblib artifact for a model. The Content-Disposition
// suggests a filename like "model_42_xgboost.joblib" so a user clicking
// Download in the UI gets something meaningful in their Downloads folder.
//
// Auth: same as the rest of /api — single-user JWT. The artifact is just
// the trained estimator + the FeatureSchema; nothing user-private.
func (s *Server) downloadModelArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Model id must be numeric", "")
		return
	}
	m, err := s.db.GetModel(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "model_not_found", "No model with that id", "")
			return
		}
		writeError(w, http.StatusInternalServerError, "model_lookup_failed", "", "")
		return
	}
	if m.ArtifactPath == nil || *m.ArtifactPath == "" {
		writeError(w, http.StatusNotFound, "artifact_missing",
			"This model has no artifact on disk",
			"Retrain the model — the .joblib was never persisted.")
		return
	}
	full := filepath.Join(s.cfg.ModelsDir, *m.ArtifactPath)
	f, err := os.Open(full)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact_file_missing",
			"Artifact file not found on disk",
			"Retrain the model — the file was deleted out-of-band.")
		return
	}
	defer f.Close()
	st, _ := f.Stat()

	suggested := "model_" + strconv.FormatInt(id, 10) + "_" + m.Algo + ".joblib"
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+suggested+`"`)
	if st != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	if _, err := io.Copy(w, f); err != nil {
		// Client likely disconnected; nothing actionable.
		return
	}
}

// GET /api/models/{id}/predicted-vs-actual?limit=1000
//
// Returns (actual, predicted) pairs for every job the model scored that
// also has a known duration. The frontend plots these as a scatter; a
// perfect model would have all points on y=x.
func (s *Server) getModelPredictedVsActual(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Model id must be numeric", "")
		return
	}
	limit := 1000
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}
	points, err := s.db.ListPredictedActual(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "predicted_actual_failed",
			"Could not load predictions", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"points": points})
}
