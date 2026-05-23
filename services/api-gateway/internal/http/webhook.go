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
			if pred, err := s.predictFromWebhook(r.Context(), head.Repository.FullName, head.WorkflowRun.Name, head.WorkflowRun.HeadBranch, head.WorkflowRun.Event); err == nil {
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
func (s *Server) predictFromWebhook(parent context.Context, fullName, workflowName, branch, event string) (ml.PredictFromPayloadResponse, error) {
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
	return s.mlClient.PredictFromPayload(ctx, req)
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
