package http

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

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
		State   string `json:"state"`     // ok | degraded | down
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
