package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
)

// installWebhookAsync kicks off an EnsureWebhook call in a background
// goroutine so POST /api/repos / sync handlers don't block on a 1–3s
// GitHub API round-trip. The outcome (success/failure with reason) is
// persisted into the repos row's webhook_* columns; the UI re-renders
// on the next refresh tick.
//
// This is fire-and-forget on purpose. If the goroutine is killed by a
// container restart mid-install, the worst outcome is webhook_status
// stays "not_attempted" until the user clicks the manual install button
// — a graceful degradation rather than a silent stuck request.
func (s *Server) installWebhookAsync(repoID int64, owner, repoName, callerToken string) {
	go func() {
		// Detach from the request context — we want this to run even
		// if the client disconnects after the POST returns. Cap with
		// our own timeout so it can't run forever.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		token := callerToken
		if token == "" {
			persisted, err := s.db.GetGithubPAT(ctx)
			if err == nil {
				token = persisted
			}
		}

		res := s.installer.Install(ctx, token, owner, repoName)
		_ = s.db.SetRepoWebhook(ctx, repoID, res.HookID, res.CallbackURL, string(res.Status), res.Err)

		// Record activity so the user can find out what happened in
		// /admin → Activity log even if they didn't see the toast.
		action := "install_webhook_ok"
		success := true
		if res.Status != gh.StatusInstalled {
			action = "install_webhook_failed"
			success = false
		}
		_ = s.db.RecordActivity(ctx, "system", action, owner+"/"+repoName,
			res.Err, success, map[string]any{
				"status":   string(res.Status),
				"hook_id":  res.HookID,
				"callback": res.CallbackURL,
			})
	}()
}

// POST /api/repos/{id}/webhook
//
// Manual trigger for the same flow that runs automatically when a repo
// is added. Useful when:
//
//   - The user added a repo before configuring a PAT, then later set
//     one in /admin → Settings and wants to retry.
//   - The user revoked the hook on GitHub manually and wants to re-add
//     it (the EnsureWebhook is idempotent — it'll find the URL gone
//     and POST a fresh hook).
//
// Synchronous: caller wants the result reflected in their UI. Caps the
// GitHub call to 10s via the installer's own timeout.
func (s *Server) installRepoWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Repo id must be numeric", "")
		return
	}
	repo, err := s.db.LookupRepoByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "repo_not_found",
				"No repository with that id", "Reload /datasets.")
			return
		}
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed", "", "")
		return
	}

	// Token resolution: persisted PAT from system_state. The HTTP body
	// can carry an inline override if the user wants to install with a
	// one-off token, but the typical path uses the persisted one.
	token, _ := s.db.GetGithubPAT(r.Context())

	res := s.installer.Install(r.Context(), token, repo.Owner, repo.Name)
	if err := s.db.SetRepoWebhook(r.Context(), id, res.HookID, res.CallbackURL, string(res.Status), res.Err); err != nil {
		writeError(w, http.StatusInternalServerError, "persist_failed",
			"Webhook ran but couldn't persist result", "Retry — the GitHub side may have a stale hook now.")
		return
	}
	_ = s.db.RecordActivity(r.Context(), "user", "install_webhook", repo.Slug(),
		res.Err, res.Status == gh.StatusInstalled,
		map[string]any{"status": string(res.Status), "hook_id": res.HookID})

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   string(res.Status),
		"hook_id":  res.HookID,
		"callback": res.CallbackURL,
		"error":    res.Err,
	})
}

// DELETE /api/repos/{id}/webhook
//
// Removes the previously installed hook on GitHub's side and clears the
// local tracking columns. Idempotent — if nothing was installed, just
// clears the row.
//
// Best-effort: if the GitHub delete call fails, we still clear the
// local row so the user can re-install fresh. The worst outcome is an
// orphan hook in the user's GitHub repo that they have to remove by hand.
func (s *Server) removeRepoWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "Repo id must be numeric", "")
		return
	}
	repo, err := s.db.LookupRepoByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "repo_not_found",
				"No repository with that id", "Reload /datasets.")
			return
		}
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed", "", "")
		return
	}

	token, _ := s.db.GetGithubPAT(r.Context())
	var hookID int64
	if repo.WebhookID != nil {
		hookID = *repo.WebhookID
	}
	res := s.installer.Remove(r.Context(), token, repo.Owner, repo.Name, hookID)
	_ = s.db.ClearRepoWebhook(r.Context(), id)
	_ = s.db.RecordActivity(r.Context(), "user", "remove_webhook", repo.Slug(),
		res.Err, res.Err == "", map[string]any{"hook_id": hookID})

	writeJSON(w, http.StatusOK, map[string]any{
		"removed": true,
		"warning": res.Err,
	})
}
