package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

	// ML service — short HTTP GET /healthz. Down = ml container offline
	// or wedged; degraded = reachable but slow / non-200.
	{
		c := comp{Name: "ml-service", State: "ok"}
		httpCtx, httpCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		req, _ := http.NewRequestWithContext(httpCtx, http.MethodGet, s.cfg.MLBaseURL+"/healthz", nil)
		resp, err := http.DefaultClient.Do(req)
		httpCancel()
		if err != nil {
			c.State = "down"
			c.Message = err.Error()
		} else {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				c.State = "degraded"
				c.Message = fmt.Sprintf("HTTP %d from /healthz", resp.StatusCode)
			} else {
				c.Message = "healthz=200"
			}
		}
		out = append(out, c)
	}

	// Redis — PING via a one-shot TCP-friendly client. If Redis is down
	// the simulator (future Redis sorted-set work) won't function;
	// today's pipeline is fine without it but we surface the state so
	// the user sees a yellow chip rather than mysterious errors later.
	{
		c := comp{Name: "redis", State: "ok"}
		err := pingRedis(ctx, s.cfg.RedisAddr)
		if err != nil {
			c.State = "down"
			c.Message = err.Error()
		} else {
			c.Message = "PING=PONG"
		}
		out = append(out, c)
	}

	// bg_jobs worker — proxy via "any running or recently done job":
	// if the DB has activity in the last 5 minutes, runner is alive.
	// In multi-binary mode this still reflects collector + simulator
	// because they write to the same bg_jobs table.
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

	// bg-jobs paused flag — when an operator hits "Pause workers" on
	// /admin, we surface this as a degraded chip so it's obvious why
	// nothing's progressing.
	{
		paused, _ := s.db.GetBGJobsPaused(ctx)
		if paused {
			out = append(out, comp{
				Name:    "bg-jobs runner",
				State:   "degraded",
				Message: "paused by operator (Resume on /admin)",
			})
		}
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

// POST /api/admin/bg-jobs/pause
// POST /api/admin/bg-jobs/resume
//
// Operator-level kill switch for the background-job runner. Useful when
// the user wants to stop ongoing pulls (rate-limited GitHub fetch, long
// training) without restarting the containers. Workers honour the flag
// cooperatively at every poll tick — see internal/bgjobs.dispatchOnce.
//
// In-flight jobs continue running; only the claim cycle stops. That's
// the safest behaviour: a half-applied train_model is more disruptive
// than waiting a few seconds for it to finish before the pause takes
// effect.
func (s *Server) pauseBGRunner(w http.ResponseWriter, r *http.Request) {
	if err := s.db.SetBGJobsPaused(r.Context(), true); err != nil {
		writeError(w, http.StatusInternalServerError, "pause_failed",
			"Could not pause the bg-jobs runner", "Retry.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "bg_runner_pause", "",
		"bg-jobs runner paused", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"paused": true})
}

func (s *Server) resumeBGRunner(w http.ResponseWriter, r *http.Request) {
	if err := s.db.SetBGJobsPaused(r.Context(), false); err != nil {
		writeError(w, http.StatusInternalServerError, "resume_failed",
			"Could not resume the bg-jobs runner", "Retry.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "bg_runner_resume", "",
		"bg-jobs runner resumed", true, nil)
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

// pingRedis is a one-off `PING` over a raw TCP connection — avoids
// pulling in the go-redis client just for a health check. Returns nil
// when the server replied "+PONG\r\n", or an error otherwise.
func pingRedis(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 1500 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if !deadline.IsZero() {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	}
	// Inline RESP — `*1\r\n$4\r\nPING\r\n`.
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		return err
	}
	buf := make([]byte, 16)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if n < 5 || string(buf[:5]) != "+PONG" {
		return fmt.Errorf("unexpected reply: %q", string(buf[:n]))
	}
	return nil
}
