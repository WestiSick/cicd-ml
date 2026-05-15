// Package github is a tiny client for the three endpoints the collector
// actually uses:
//
//   GET /repos/{owner}/{repo}
//   GET /repos/{owner}/{repo}/actions/runs?per_page=100&page=N
//   GET /repos/{owner}/{repo}/actions/runs/{run_id}/jobs
//
// We do NOT use google/go-github — its build pulls dozens of MB of types
// for every API surface, almost none of which we need. The endpoints we
// hit are stable and small; hand-rolled types are cheaper and clearer.
//
// Token handling: an empty token still works (60 req/h limit, useful for
// smoke tests). With a PAT the limit is 5000 req/h. Both share the same
// code path — the Authorization header is just omitted when empty.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const baseURL = "https://api.github.com"

type Client struct {
	HTTP  *http.Client
	Token string
}

func NewClient(token string) *Client {
	return &Client{
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Token: token,
	}
}

// RateLimit captures the headers GitHub returns on every response so the
// collector (and UI) can show "4982/5000 remaining, reset in 23m".
type RateLimit struct {
	Limit     int
	Remaining int
	ResetAt   time.Time
}

// Repo is the slim view of /repos/{owner}/{repo} we actually need.
type Repo struct {
	ID            int64  `json:"id"`
	DefaultBranch string `json:"default_branch"`
}

// WorkflowRun mirrors the fields used downstream. JSON tags align with
// the GitHub API so json.Unmarshal Just Works.
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	RunStartedAt *time.Time `json:"run_started_at"`
	WorkflowID   int64      `json:"workflow_id"`
}

// WorkflowRunsPage is one page of /actions/runs.
type WorkflowRunsPage struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []WorkflowRun `json:"workflow_runs"`
}

// Job is one row of /actions/runs/{id}/jobs. Note that GitHub uses
// `started_at` / `completed_at` (no `_sec` suffix on duration — derive
// it from the deltas).
type Job struct {
	ID           int64      `json:"id"`
	RunID        int64      `json:"run_id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"`
	Conclusion   string     `json:"conclusion"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	RunnerName   string     `json:"runner_name"`
	RunnerGroup  string     `json:"runner_group_name"`
	Labels       []string   `json:"labels"`
	Steps        []struct{} `json:"steps"`
}

type JobsPage struct {
	TotalCount int   `json:"total_count"`
	Jobs       []Job `json:"jobs"`
}

// Error from a 4xx/5xx with the body's "message" attached.
type APIError struct {
	StatusCode int
	Body       string
	Rate       RateLimit
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github api: %d: %s", e.StatusCode, e.Body)
}

// IsRateLimited returns true for both 403/429 with remaining=0 — both
// indicate "wait until reset" rather than retry immediately.
func (e *APIError) IsRateLimited() bool {
	return (e.StatusCode == 403 || e.StatusCode == 429) && e.Rate.Remaining == 0
}

func (c *Client) get(ctx context.Context, path string, out any) (RateLimit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return RateLimit{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cicd-ml/0.1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return RateLimit{}, err
	}
	defer resp.Body.Close()

	rate := parseRateLimit(resp.Header)
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return rate, &APIError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			Rate:       rate,
		}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return rate, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return rate, nil
}

func parseRateLimit(h http.Header) RateLimit {
	var rl RateLimit
	if v := h.Get("X-RateLimit-Limit"); v != "" {
		rl.Limit, _ = strconv.Atoi(v)
	}
	if v := h.Get("X-RateLimit-Remaining"); v != "" {
		rl.Remaining, _ = strconv.Atoi(v)
	}
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil {
			rl.ResetAt = time.Unix(ts, 0)
		}
	}
	return rl
}

// GetRepo fetches metadata for a single repository.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (Repo, RateLimit, error) {
	var r Repo
	rl, err := c.get(ctx, fmt.Sprintf("/repos/%s/%s", owner, name), &r)
	return r, rl, err
}

// ListWorkflowRuns fetches one page. Use `created` to narrow to a date range:
// e.g. `>=2024-11-14`. Returns (page, rate, err).
func (c *Client) ListWorkflowRuns(
	ctx context.Context, owner, name string, page int, createdFilter string,
) (WorkflowRunsPage, RateLimit, error) {
	q := url.Values{}
	q.Set("per_page", "100")
	q.Set("page", strconv.Itoa(page))
	if createdFilter != "" {
		q.Set("created", createdFilter)
	}
	var p WorkflowRunsPage
	rl, err := c.get(ctx, fmt.Sprintf("/repos/%s/%s/actions/runs?%s", owner, name, q.Encode()), &p)
	return p, rl, err
}

// ListRunJobs fetches every job for a run (paginated, but rarely more than
// one page; we follow Link headers via the page param if needed).
func (c *Client) ListRunJobs(ctx context.Context, owner, name string, runID int64) ([]Job, RateLimit, error) {
	var (
		all  []Job
		rate RateLimit
	)
	for page := 1; ; page++ {
		var p JobsPage
		rl, err := c.get(ctx,
			fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs?per_page=100&page=%d", owner, name, runID, page),
			&p)
		if err != nil {
			return nil, rl, err
		}
		rate = rl
		all = append(all, p.Jobs...)
		if len(p.Jobs) < 100 {
			break
		}
		if page >= 10 {
			// Safety valve — pathological matrix jobs aside, runs almost
			// never exceed a few hundred jobs.
			break
		}
	}
	return all, rate, nil
}

// Sentinel error to differentiate "not found" cleanly at call sites.
var ErrNotFound = errors.New("github: not found")
