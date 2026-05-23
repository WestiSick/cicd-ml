// Webhook installation helpers — the bits of the GitHub API needed to
// turn "user added a repo" into "GitHub now POSTs us workflow_run events".
//
// Why this file is separate from client.go:
//
//	client.go covers READ paths (collector pulls runs/jobs); the auth
//	surface (GET, no body, no rate-limit retry) is trivial. webhook
//	install is WRITE (POST/PATCH/DELETE), needs JSON body marshalling,
//	and surfaces a different class of error (403 = no admin access, vs
//	GET's 403 = rate limit). Mixing them blurred the error handling, so
//	they live apart.
//
// PAT scope requirements:
//
//   - For public repos the user owns / has maintainer access to:
//     `public_repo` is enough.
//   - For private repos: `repo`.
//   - `admin:repo_hook` works for both but isn't strictly required.
//
// For upstream public repos (vitejs/vite, prometheus, etc.) the user
// will NOT have admin access — we'll get 404 and surface that as
// "failed_no_access" without retrying.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Hook is the slim view of /repos/{owner}/{repo}/hooks rows we use.
//
// We don't model every field — only what we need to (a) match an
// existing hook by callback URL and (b) report what's installed back to
// the UI. Hook.Config.Secret is write-only on GitHub's side (returned
// as "********"); we never compare it.
type Hook struct {
	ID     int64      `json:"id"`
	Name   string     `json:"name"`
	Active bool       `json:"active"`
	Events []string   `json:"events"`
	Config HookConfig `json:"config"`
}

type HookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	InsecureSSL string `json:"insecure_ssl"`
	Secret      string `json:"secret,omitempty"`
}

// EnsureWebhook idempotently installs (or updates) the workflow_run
// webhook on the given repo so that GitHub POSTs to `callbackURL`.
//
// Behaviour:
//   - If a hook already exists with the same callback URL, PATCH it to
//     keep events/secret/active up to date and return it.
//   - Otherwise POST a new hook.
//   - Returns *APIError on 4xx/5xx; callers should inspect StatusCode
//     to decide whether the failure is the user's fault (403/404 = no
//     admin access; nothing to retry) vs transient (5xx = retry later).
//
// Events: we register `workflow_run` (covers requested/in_progress/
// completed) and `push` (planned for future commit-features collection).
// Keeping the set narrow reduces noise in the webhook log.
func (c *Client) EnsureWebhook(ctx context.Context, owner, repo, callbackURL, secret string) (*Hook, error) {
	existing, err := c.listHooks(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	desired := HookConfig{
		URL:         callbackURL,
		ContentType: "json",
		InsecureSSL: "0",
		Secret:      secret,
	}
	events := []string{"workflow_run", "push"}

	// 1. Look for an existing hook on the same callback URL.
	for _, h := range existing {
		if h.Config.URL == callbackURL {
			// Idempotent update — re-PATCH active/events/config so even if
			// the user disabled it in GitHub UI or rotated the secret, we
			// flip it back to a known-good state.
			updated, err := c.patchHook(ctx, owner, repo, h.ID, events, desired)
			if err != nil {
				return nil, err
			}
			return updated, nil
		}
	}

	// 2. Create fresh.
	return c.createHook(ctx, owner, repo, events, desired)
}

// DeleteWebhook removes a previously installed hook. Returns nil on
// 404 (already gone) — the caller doesn't care about the difference.
func (c *Client) DeleteWebhook(ctx context.Context, owner, repo string, hookID int64) error {
	path := fmt.Sprintf("/repos/%s/%s/hooks/%d", owner, repo, hookID)
	_, err := c.doWrite(ctx, http.MethodDelete, path, nil, nil)
	if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == http.StatusNotFound {
		return nil
	}
	return err
}

// IsReachableCallbackURL returns false for URLs GitHub can't actually
// hit (localhost, internal docker hostnames). The caller uses this to
// short-circuit auto-install on local dev — there's no point asking
// GitHub to call back to http://api:8080.
//
// Heuristic, not exhaustive: an URL pointing at a private RFC1918 range
// would also be unreachable, but checking that requires DNS resolution.
// We trust the operator to set PUBLIC_API_BASE correctly in prod.
func IsReachableCallbackURL(callbackURL string) bool {
	s := strings.ToLower(strings.TrimSpace(callbackURL))
	if s == "" {
		return false
	}
	for _, marker := range []string{
		"localhost", "127.0.0.1", "0.0.0.0",
		"://api:", "://api-gateway:", "://host.docker.internal",
	} {
		if strings.Contains(s, marker) {
			return false
		}
	}
	// Must look like a URL with scheme.
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// ---- internals -----------------------------------------------------

func (c *Client) listHooks(ctx context.Context, owner, repo string) ([]Hook, error) {
	var out []Hook
	path := fmt.Sprintf("/repos/%s/%s/hooks?per_page=100", owner, repo)
	_, err := c.doWrite(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) createHook(ctx context.Context, owner, repo string, events []string, cfg HookConfig) (*Hook, error) {
	body := map[string]any{
		"name":   "web",
		"active": true,
		"events": events,
		"config": cfg,
	}
	var out Hook
	if _, err := c.doWrite(ctx, http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/hooks", owner, repo), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) patchHook(ctx context.Context, owner, repo string, hookID int64, events []string, cfg HookConfig) (*Hook, error) {
	body := map[string]any{
		"active": true,
		"events": events,
		"config": cfg,
	}
	var out Hook
	if _, err := c.doWrite(ctx, http.MethodPatch,
		fmt.Sprintf("/repos/%s/%s/hooks/%d", owner, repo, hookID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doWrite mirrors c.get but for POST/PATCH/DELETE with optional JSON
// body. Kept here rather than in client.go because the read path uses
// a different signature (returns RateLimit explicitly, no body marshal).
func (c *Client) doWrite(ctx context.Context, method, path string, in any, out any) (RateLimit, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return RateLimit{}, fmt.Errorf("marshal %s: %w", path, err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return RateLimit{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cicd-ml/0.1")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return RateLimit{}, err
	}
	defer resp.Body.Close()

	rate := parseRateLimit(resp.Header)
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return rate, &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
			Rate:       rate,
		}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return rate, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return rate, nil
}
