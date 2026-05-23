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
			ID         int64  `json:"id"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadBranch string `json:"head_branch"`
			HeadSHA    string `json:"head_sha"`
			Name       string `json:"name"`
			Event      string `json:"event"`
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
			} else if mlErr, ok := err.(*ml.APIError); !ok || mlErr.Code != "no_active_model" {
				// Log unexpected errors. "no_active_model" is the normal
				// fresh-install case and not worth warning about.
				log.Warn().Err(err).Str("repo", head.Repository.FullName).Msg("webhook predict")
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
