// Installer is the glue between an "add repo" UI action and the GitHub
// API call that registers the webhook. It exists as its own type rather
// than living in the HTTP handler because:
//
//   - Three call sites need it (POST /api/repos, POST /api/repos/{id}/webhook,
//     bootstrap orchestrator). Centralising the decision tree —
//     "no PAT? no public URL? upstream repo we don't own?" — keeps the
//     error mapping consistent.
//   - It also reads the persisted PAT from system_state when the caller
//     didn't supply one, matching the same precedence the collector uses.
package github

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Status mirrors the `webhook_status` column on repos.
type Status string

const (
	StatusNotAttempted      Status = "not_attempted"
	StatusInstalled         Status = "installed"
	StatusFailedNoAccess    Status = "failed_no_access"
	StatusFailedUnreachable Status = "failed_unreachable"
	StatusFailedOther       Status = "failed_other"
)

// Result is what InstallWebhook reports back. Status drives the UI
// badge, HookID/CallbackURL drive future delete-by-id calls, Err carries
// the human-readable message for the activity log + tooltip.
type Result struct {
	Status      Status
	HookID      int64
	CallbackURL string
	Err         string
}

// Installer can be mocked in tests by swapping the Client field.
type Installer struct {
	// CallbackURL is the FULL URL GitHub should POST to — typically
	// `${PUBLIC_API_BASE}/webhooks/github`. Computed once at startup.
	CallbackURL string

	// Secret is the value set in GITHUB_WEBHOOK_SECRET, also used for
	// HMAC verification on incoming deliveries. Must match.
	Secret string

	// HTTPTimeout caps the GitHub API call. 10s is generous —
	// list+create is two round trips against api.github.com.
	HTTPTimeout time.Duration
}

func NewInstaller(callbackURL, secret string) *Installer {
	return &Installer{
		CallbackURL: callbackURL,
		Secret:      secret,
		HTTPTimeout: 10 * time.Second,
	}
}

// Install runs the EnsureWebhook flow and returns a Result that the
// caller persists to the `repos` row. It NEVER returns a Go error —
// every outcome is encoded in Result.Status so the caller writes one
// row regardless. The boolean Result.Status == StatusInstalled tells
// the caller whether to show a green badge or a yellow one.
//
// `token` is the GitHub PAT — required. Pass empty string and you'll
// get StatusNotAttempted back without a GitHub call.
func (i *Installer) Install(ctx context.Context, token, owner, repo string) Result {
	if token == "" {
		return Result{
			Status: StatusNotAttempted,
			Err:    "no GitHub token configured — add one in /admin → Settings",
		}
	}
	if !IsReachableCallbackURL(i.CallbackURL) {
		return Result{
			Status: StatusFailedUnreachable,
			Err: "PUBLIC_API_BASE looks like a local URL (" + i.CallbackURL +
				") — GitHub can't reach it. Set PUBLIC_API_BASE to a public URL or use a tunnel.",
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, i.HTTPTimeout)
	defer cancel()

	client := NewClient(token)
	hook, err := client.EnsureWebhook(callCtx, owner, repo, i.CallbackURL, i.Secret)
	if err != nil {
		return classifyError(err, i.CallbackURL)
	}
	return Result{
		Status:      StatusInstalled,
		HookID:      hook.ID,
		CallbackURL: i.CallbackURL,
	}
}

// Remove deletes a previously installed hook on GitHub. Best-effort:
// failures are returned as Result.Err but the caller should still
// clear the local row — the worst case is an orphan hook in the user's
// GitHub repo, which they can delete by hand.
func (i *Installer) Remove(ctx context.Context, token, owner, repo string, hookID int64) Result {
	if token == "" || hookID == 0 {
		return Result{Status: StatusNotAttempted}
	}
	callCtx, cancel := context.WithTimeout(ctx, i.HTTPTimeout)
	defer cancel()
	if err := NewClient(token).DeleteWebhook(callCtx, owner, repo, hookID); err != nil {
		return Result{
			Status: StatusFailedOther,
			Err:    "remove webhook: " + err.Error(),
		}
	}
	return Result{Status: StatusNotAttempted}
}

// classifyError maps a GitHub error to a friendly Status. Specifically:
//
//   - 401 → bad PAT (we'd fail any other call too, so this surfaces
//     once at install time rather than mysteriously failing in collector).
//   - 403/404 → caller doesn't have admin:repo_hook on this repo. This
//     is the common case for OSS upstream repos (vitejs/vite etc.)
//     where the user is just a viewer. Don't retry, don't ask the user
//     to take action — silently mark as "failed_no_access".
//   - 422 → validation failure (events list rejected, ssl config invalid).
//     We log and move on.
//   - 5xx / network → "failed_other", retry on next manual install.
func classifyError(err error, callbackURL string) Result {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return Result{
				Status: StatusFailedOther,
				Err:    "GitHub rejected the PAT — re-enter it in /admin → Settings",
			}
		case 403, 404:
			return Result{
				Status: StatusFailedNoAccess,
				Err:    "PAT has no admin:repo_hook on this repo (you're probably not the owner/maintainer)",
			}
		case 422:
			body := strings.TrimSpace(apiErr.Body)
			if len(body) > 200 {
				body = body[:200] + "…"
			}
			return Result{
				Status: StatusFailedOther,
				Err:    "GitHub rejected the request: " + body,
			}
		}
	}
	return Result{
		Status:      StatusFailedOther,
		Err:         err.Error(),
		CallbackURL: callbackURL,
	}
}
