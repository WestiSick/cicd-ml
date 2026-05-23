package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// Recognised scheduler strategy ids. Kept in sync with
// services/scheduler/internal/strategy (Go) and frontend hard-coded UI
// labels. Adding a new strategy means appending here AND wiring it on
// the scheduler side.
var allowedStrategies = map[string]bool{
	"fifo":   true,
	"sjf":    true,
	"edf":    true,
	"custom": true,
}

// POST /api/admin/settings
//
// Body (all fields optional — only those provided are updated):
//
//	{
//	  "active_strategy": "sjf",
//	  "custom_weights": {"short_job": 1.2, "deadline_proximity": 0.4, "branch_importance": 0.5},
//	  "github_token":  "ghp_xxx"   // empty string clears
//	}
//
// Returns the resulting SystemState — same shape /system/state returns,
// so the caller can update its react-query cache without a refetch.
//
// GitHub token storage: persisted in system_state with key=github_pat,
// base64-encoded. Real encryption would need a KMS; for this single-tenant
// thesis tool the threat model (an attacker with DB access) already implies
// total compromise.
func (s *Server) updateAdminSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActiveStrategy *string              `json:"active_strategy"`
		CustomWeights  *store.CustomWeights `json:"custom_weights"`
		GithubToken    *string              `json:"github_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Could not parse settings", "Send a JSON object with optional fields.")
		return
	}

	if body.ActiveStrategy != nil {
		if !allowedStrategies[*body.ActiveStrategy] {
			writeError(w, http.StatusBadRequest, "unknown_strategy",
				"Strategy must be one of: fifo, sjf, edf, custom",
				"Pick a supported strategy from the dropdown.")
			return
		}
		if err := s.db.SetActiveStrategy(r.Context(), *body.ActiveStrategy); err != nil {
			writeError(w, http.StatusInternalServerError, "save_strategy_failed",
				"Could not persist strategy", "Retry — the previous setting is unchanged.")
			return
		}
		_ = s.db.RecordActivity(r.Context(), "user", "set_strategy", *body.ActiveStrategy,
			"strategy changed", true, nil)
	}
	if body.CustomWeights != nil {
		if err := s.db.SetCustomWeights(r.Context(), *body.CustomWeights); err != nil {
			writeError(w, http.StatusInternalServerError, "save_weights_failed",
				"Could not persist custom weights", "Retry.")
			return
		}
		_ = s.db.RecordActivity(r.Context(), "user", "set_weights", "",
			"custom weights updated", true, nil)
	}
	if body.GithubToken != nil {
		if err := s.db.SetGithubPAT(r.Context(), *body.GithubToken); err != nil {
			writeError(w, http.StatusInternalServerError, "save_token_failed",
				"Could not store GitHub token", "Retry.")
			return
		}
		_ = s.db.RecordActivity(r.Context(), "user", "set_github_token", "",
			"github token updated", true, nil)
	}

	state, err := s.db.GetSystemState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_reload_failed",
			"Saved settings but could not reload state",
			"Refresh the page to see updated values.")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// GET /api/admin/webhooks?limit=50
//
// Returns the most recent webhook deliveries with HMAC verification result.
// Backs /admin → Webhooks in the UI; lets the user debug missing pushes
// without digging into container logs.
func (s *Server) listAdminWebhooks(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.db.ListRecentWebhooks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_webhooks_failed",
			"Could not load webhook history", "Try refreshing the page.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// GET /api/admin/health
//
// Aggregates per-component status so /admin → System health renders a
// single page. The frontend's HealthDot also calls this (indirectly via
// useHealth) to colour the topbar indicator.
//
// We intentionally probe inline with short timeouts — caching would
// trade freshness for nothing meaningful at this scale (one user).
func (s *Server) systemHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	type comp struct {
		Name    string `json:"name"`
		State   string `json:"state"` // ok | degraded | down
		Message string `json:"message,omitempty"`
	}
	out := []comp{}

	// Postgres
	{
		err := s.db.Pool.Ping(ctx)
		c := comp{Name: "postgres", State: "ok"}
		if err != nil {
			c.State = "down"
			c.Message = err.Error()
		}
		out = append(out, c)
	}

	// API itself is up if we got here.
	out = append(out, comp{Name: "api-gateway", State: "ok"})

	// bg_jobs worker — proxy via "any running or recently done job":
	// if the DB has activity in the last 5 minutes, runner is alive.
	{
		var recent int
		err := s.db.Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM bg_jobs
			WHERE COALESCE(started_at, created_at) > now() - interval '5 minutes'
		`).Scan(&recent)
		c := comp{Name: "bg-jobs runner", State: "ok"}
		if err != nil {
			c.State = "degraded"
			c.Message = "could not query bg_jobs"
		} else if recent == 0 {
			c.Message = "idle (no recent activity)"
		} else {
			c.Message = "active"
		}
		out = append(out, c)
	}

	// Aggregate.
	overall := "ok"
	for _, c := range out {
		if c.State == "down" {
			overall = "down"
			break
		}
		if c.State == "degraded" {
			overall = "degraded"
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"state":      overall,
		"components": out,
		"time":       time.Now().UTC().Format(time.RFC3339),
	})
}
