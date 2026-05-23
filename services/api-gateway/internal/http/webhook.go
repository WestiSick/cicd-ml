package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ml"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
	"github.com/rs/zerolog/log"
)

// POST /webhooks/github
//
// Accepts any GitHub webhook event. We log every delivery into webhook_events
// for debugging (visible at /admin → Webhooks) and react to a small subset:
//
//   - workflow_run    : enqueue/refresh queue state and broadcast on /ws/queue
//   - ping            : record + 200, used by GitHub to verify the URL
//
// HMAC validation:
//   - GITHUB_WEBHOOK_SECRET empty (dev) → bypass
//   - Otherwise compare X-Hub-Signature-256, reject 401 on mismatch
//
// The handler intentionally returns 200 for unrecognised events — refusing
// would cause GitHub to retry, polluting the log without benefit.
func (s *Server) handleGithubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := gh.DrainBody(r.Body, 4<<20) // 4 MB ceiling
	if err != nil {
		writeError(w, http.StatusBadRequest, "webhook_body",
			"Could not read webhook body", "Retry — if it persists, check the proxy in front of the API.")
		return
	}

	signature := r.Header.Get("X-Hub-Signature-256")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	hmacValid := gh.VerifySignature(s.cfg.GithubWebhookSecret, body, signature)

	// Pull repo full name out of the payload for diagnostics.
	var head struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		WorkflowRun struct {
			ID           int64      `json:"id"`
			Status       string     `json:"status"`
			Conclusion   string     `json:"conclusion"`
			HeadBranch   string     `json:"head_branch"`
			HeadSHA      string     `json:"head_sha"`
			Name         string     `json:"name"`
			Event        string     `json:"event"`
			RunStartedAt *time.Time `json:"run_started_at"`
			UpdatedAt    *time.Time `json:"updated_at"`
			CreatedAt    *time.Time `json:"created_at"`
		} `json:"workflow_run"`
	}
	_ = json.NewDecoder(bytes.NewReader(body)).Decode(&head)

	errMsg := ""
	if !hmacValid {
		errMsg = "HMAC mismatch"
	}
	if err := s.db.RecordWebhook(r.Context(), store.RecordWebhookParams{
		DeliveryID: deliveryID,
		EventType:  eventType,
		Repo:       head.Repository.FullName,
		HMACValid:  hmacValid,
		Payload:    body,
		Error:      errMsg,
	}); err != nil {
		log.Warn().Err(err).Msg("record webhook")
	}

	if !hmacValid {
		writeError(w, http.StatusUnauthorized, "hmac_invalid",
			"Signature did not match the configured secret",
			"Update GITHUB_WEBHOOK_SECRET on the API to match the one on the GitHub webhook.")
		return
	}

	switch eventType {
	case "ping":
		writeJSON(w, http.StatusOK, map[string]string{"pong": deliveryID})
		return

	case "workflow_run":
		// On a brand-new run (status=requested/queued) we don't yet have a
		// row in `jobs` — but the dashboard should still show a card with
		// a predicted_sec. We call the ml-service "from-payload" endpoint
		// which scores from the webhook payload directly. Best-effort: if
		// there's no active model (fresh install) we still broadcast the
		// event so the live feed isn't blank.
		payload := map[string]any{
			"repo":       head.Repository.FullName,
			"run_id":     head.WorkflowRun.ID,
			"workflow":   head.WorkflowRun.Name,
			"branch":     head.WorkflowRun.HeadBranch,
			"head_sha":   head.WorkflowRun.HeadSHA,
			"status":     head.WorkflowRun.Status,
			"conclusion": head.WorkflowRun.Conclusion,
			"event":      head.WorkflowRun.Event,
		}
		if head.WorkflowRun.Status == "requested" || head.WorkflowRun.Status == "queued" || head.WorkflowRun.Status == "in_progress" {
			// Best-effort: pull the commit's per-file diff into the DB
			// before predicting so ml-service can use the commit-content
			// features (backend_files / frontend_files / is_docs_only).
			// Bounded by a short timeout — if GitHub is slow or rate-
			// limited we still produce a prediction, just without commit-
			// content fidelity (falls back to repo/branch averages).
			owner, name := splitFullName(head.Repository.FullName)
			s.ensureCommitForWebhook(r.Context(), owner, name, head.WorkflowRun.HeadSHA)

			if pred, err := s.predictFromWebhook(r.Context(), head.Repository.FullName, head.WorkflowRun.Name, head.WorkflowRun.HeadBranch, head.WorkflowRun.Event, head.WorkflowRun.HeadSHA); err == nil {
				payload["predicted_sec"] = pred.PredictedSec
				payload["model_id"] = pred.ModelID
				payload["model_algo"] = pred.ModelAlgo
				// Stash the prediction so the matching `completed` event a
				// few seconds-to-minutes later can compute δ-error without
				// a DB round-trip. Keyed by (run_id, repo) — the same pair
				// that arrives on every workflow_run.* event.
				s.recentPredictions.Remember(head.Repository.FullName, head.WorkflowRun.ID, pred.PredictedSec, pred.ModelID, pred.ModelAlgo)
			} else if mlErr, ok := err.(*ml.APIError); !ok || mlErr.Code != "no_active_model" {
				// Log unexpected errors. "no_active_model" is the normal
				// fresh-install case and not worth warning about.
				log.Warn().Err(err).Str("repo", head.Repository.FullName).Msg("webhook predict")
			}
		}

		// On `completed` we compute the actual workflow-level duration from
		// run_started_at → updated_at (both come on the webhook payload, no
		// extra GitHub API call). If we remember an earlier prediction for
		// the same run_id, we attach predicted_sec + delta_pct so the
		// dashboard can show the live ROC of model accuracy.
		//
		// Why workflow-level and not job-level: jobs require a follow-up
		// GET /actions/runs/{id}/jobs call (extra rate-limit cost + 1–2s
		// latency added to the webhook ack). Workflow-level is "close
		// enough" for the live demo; the collector still backfills job-level
		// data within seconds for the /datasets and /experiments scatter.
		if head.WorkflowRun.Status == "completed" {
			if head.WorkflowRun.RunStartedAt != nil && head.WorkflowRun.UpdatedAt != nil {
				actualSec := head.WorkflowRun.UpdatedAt.Sub(*head.WorkflowRun.RunStartedAt).Seconds()
				if actualSec > 0 {
					payload["actual_sec"] = actualSec
					if remembered, ok := s.recentPredictions.Get(head.Repository.FullName, head.WorkflowRun.ID); ok {
						payload["predicted_sec"] = remembered.PredictedSec
						payload["model_id"] = remembered.ModelID
						payload["model_algo"] = remembered.ModelAlgo
						// Δ as % of actual (signed). Negative = model
						// over-predicted; positive = under-predicted.
						if actualSec > 0 {
							payload["delta_pct"] = 100.0 * (remembered.PredictedSec - actualSec) / actualSec
						}
						s.recentPredictions.Forget(head.Repository.FullName, head.WorkflowRun.ID)
					}
				}
			}
		}

		// Broadcast on /ws/queue so /dashboard's live feed shows the event
		// even before the run is fully persisted. The collector picks up
		// the same run via its normal sync path within seconds.
		s.hub.PublishJSON("queue", "workflow_run."+head.WorkflowRun.Status, payload)
		writeJSON(w, http.StatusOK, map[string]string{"accepted": "workflow_run"})
		return
	}

	// Unrecognised — acknowledge so GitHub doesn't retry.
	writeJSON(w, http.StatusOK, map[string]string{"accepted": "ignored"})
}

// predictFromWebhook calls ml-service with a 3-second budget. We don't
// want a slow ml-service to delay GitHub's webhook ack — if predict
// doesn't return promptly the broadcast simply omits predicted_sec and
// the dashboard still gets the event. The collector will produce a
// proper prediction later when the run is persisted.
//
// headSHA, when non-empty, lets ml-service load this commit's per-file
// diff (commits + commit_files) and feed bucket counts into the model.
// That's what turns "average repo prediction" into "this push is
// backend-heavy, predict longer" — see ensureCommitForWebhook for the
// DB-population side of the contract.
func (s *Server) predictFromWebhook(parent context.Context, fullName, workflowName, branch, event, headSHA string) (ml.PredictFromPayloadResponse, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	owner, name := splitFullName(fullName)
	req := ml.PredictFromPayloadRequest{
		RepoOwner: owner,
		RepoName:  name,
	}
	if workflowName != "" {
		req.WorkflowName = &workflowName
	}
	if branch != "" {
		req.HeadBranch = &branch
	}
	if event != "" {
		req.Event = &event
	}
	if headSHA != "" {
		req.HeadSHA = &headSHA
	}
	return s.mlClient.PredictFromPayload(ctx, req)
}

// ensureCommitForWebhook synchronously fetches the commit's per-file
// diff from GitHub and persists it so the matching predict_from_payload
// can read commit-content features. Best-effort: any failure (no SHA,
// no PAT, rate-limit, network) is logged at warn level and the function
// returns — the caller still gets a prediction, just without bucket-
// level fidelity.
//
// Idempotent via the collector's CommitExists check: if the commit is
// already in the DB (matrix builds, re-runs, or the collector beat us
// to it) the GitHub call is skipped and we return immediately.
//
// We bound the GitHub call at 2s. That leaves headroom inside the
// outer 30s GitHub webhook ack deadline for the subsequent predict
// (3s budget). Real-world latency from us-east → api.github.com sits
// around 200-400ms for a single GET /commits/{sha}, so 2s is a generous
// ceiling that still keeps webhook turnaround under a second on the
// happy path.
func (s *Server) ensureCommitForWebhook(parent context.Context, owner, name, headSHA string) {
	if headSHA == "" || owner == "" || name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()

	exists, err := s.db.CommitExists(ctx, headSHA)
	if err != nil {
		log.Warn().Err(err).Str("sha", headSHA).Msg("webhook: commit_exists check failed")
		return
	}
	if exists {
		// Already populated by an earlier webhook for the same SHA or by
		// the collector. commit_files rows came in via the same path so
		// the predict will see them.
		return
	}

	token, _ := s.db.GetGithubPAT(ctx)
	client := gh.NewClient(token)
	c, _, cerr := client.GetCommit(ctx, owner, name, headSHA)
	if cerr != nil {
		// Non-fatal — predict will just lack commit-content fidelity.
		// Common reasons: anonymous rate-limit (no PAT configured), 404
		// on a deleted force-push, or context deadline exceeded.
		log.Warn().Err(cerr).Str("repo", owner+"/"+name).Str("sha", headSHA).
			Msg("webhook: GetCommit failed; predicting without commit-content features")
		return
	}

	// Look up repo_id so the UpsertCommit row has the right FK. If the
	// repo isn't tracked yet (webhook from a repo we never added) we
	// skip persistence — predicting on an untracked repo is already a
	// degenerate case.
	repo, err := s.db.LookupRepo(ctx, owner, name)
	if err != nil {
		log.Warn().Err(err).Str("repo", owner+"/"+name).
			Msg("webhook: repo not tracked; skipping commit persist")
		return
	}

	if err := s.db.UpsertCommit(ctx, store.UpsertCommitParams{
		SHA:          c.SHA,
		RepoID:       repo.ID,
		Author:       c.Author.Login,
		Message:      truncateWebhookMessage(c.CommitDetail.Message, 280),
		FilesChanged: len(c.Files),
		Additions:    c.Stats.Additions,
		Deletions:    c.Stats.Deletions,
		CommittedAt:  zeroTimeToNil(c.CommitDetail.Author.Date),
	}); err != nil {
		log.Warn().Err(err).Str("sha", headSHA).Msg("webhook: upsert commit failed")
		return
	}
	if len(c.Files) > 0 {
		rows := make([]store.CommitFileParams, 0, len(c.Files))
		for _, f := range c.Files {
			rows = append(rows, store.CommitFileParams{
				Filename:  f.Filename,
				Status:    f.Status,
				Additions: f.Additions,
				Deletions: f.Deletions,
			})
		}
		if err := s.db.BulkInsertCommitFiles(ctx, c.SHA, rows); err != nil {
			log.Warn().Err(err).Str("sha", headSHA).Int("files", len(rows)).
				Msg("webhook: bulk insert commit_files failed")
		}
	}
}

// truncateWebhookMessage mirrors collector's truncateMessage. Inlined
// here to avoid pulling a cross-package import for one helper. 280
// chars matches a Twitter-era rule-of-thumb for summary length —
// enough for diagnostics, slim enough to keep the row cheap.
func truncateWebhookMessage(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// zeroTimeToNil mirrors collector's zeroToNil for time.Time — returns
// nil when t.IsZero() so we store NULL in committed_at rather than the
// unix epoch.
func zeroTimeToNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func splitFullName(full string) (owner, name string) {
	if i := strings.Index(full, "/"); i > 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

// io.ReadAll wrapper kept here to avoid pulling "io" into other files that
// only use it for this purpose. Trivial, but reduces blast radius.
var _ io.ReadCloser = (io.ReadCloser)(nil)
