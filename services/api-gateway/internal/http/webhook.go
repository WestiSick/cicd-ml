package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
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
		// Broadcast on /ws/queue so /dashboard's live feed shows the event
		// even before the run is fully persisted. The collector picks up
		// the same run via its normal sync path within seconds.
		s.hub.PublishJSON("queue", "workflow_run."+head.WorkflowRun.Status, map[string]any{
			"repo":        head.Repository.FullName,
			"run_id":      head.WorkflowRun.ID,
			"workflow":    head.WorkflowRun.Name,
			"branch":      head.WorkflowRun.HeadBranch,
			"head_sha":    head.WorkflowRun.HeadSHA,
			"status":      head.WorkflowRun.Status,
			"conclusion":  head.WorkflowRun.Conclusion,
		})
		writeJSON(w, http.StatusOK, map[string]string{"accepted": "workflow_run"})
		return
	}

	// Unrecognised — acknowledge so GitHub doesn't retry.
	writeJSON(w, http.StatusOK, map[string]string{"accepted": "ignored"})
}

// io.ReadAll wrapper kept here to avoid pulling "io" into other files that
// only use it for this purpose. Trivial, but reduces blast radius.
var _ io.ReadCloser = (io.ReadCloser)(nil)
