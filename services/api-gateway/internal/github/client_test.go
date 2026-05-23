package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Replace the package-level baseURL via a test server. We do this by
// shadowing the client's HTTP and rewriting URLs.

func TestGetRepo_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/repos/vitejs/vite" {
			t.Fatalf("unexpected path: %s", got)
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			t.Fatalf("missing or wrong auth header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4982")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		_, _ = w.Write([]byte(`{"id":42,"default_branch":"main"}`))
	}))
	defer srv.Close()

	c := NewClient("tok123")
	// Point the client at our test server by overriding the transport URL.
	c.HTTP.Transport = rewriteTo(srv.URL)

	repo, rl, err := c.GetRepo(context.Background(), "vitejs", "vite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.ID != 42 {
		t.Fatalf("id=%d", repo.ID)
	}
	if rl.Remaining != 4982 || rl.Limit != 5000 {
		t.Fatalf("rate parse failed: %+v", rl)
	}
}

func TestGetRepo_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	defer srv.Close()

	c := NewClient("")
	c.HTTP.Transport = rewriteTo(srv.URL)

	_, _, err := c.GetRepo(context.Background(), "x", "y")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if !apiErr.IsRateLimited() {
		t.Fatalf("expected IsRateLimited true; got %+v", apiErr)
	}
}

func TestListWorkflowRuns_Pagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		_, _ = w.Write([]byte(`{"total_count":2,"workflow_runs":[
			{"id":1,"head_sha":"a","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z","status":"completed"},
			{"id":2,"head_sha":"b","created_at":"2025-01-02T00:00:00Z","updated_at":"2025-01-02T00:00:00Z","status":"completed"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient("")
	c.HTTP.Transport = rewriteTo(srv.URL)

	page, _, err := c.ListWorkflowRuns(context.Background(), "x", "y", 1, ">=2024-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.WorkflowRuns) != 2 || page.TotalCount != 2 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

// rewriteTo returns an http.RoundTripper that rewrites every request URL
// to the given test server. Cleaner than mutating the package-level
// baseURL constant.
type rewriter struct {
	target string
	rt     http.RoundTripper
}

func (r *rewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := req.URL.Parse(r.target + req.URL.Path + "?" + req.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.Host = u.Host
	return r.rt.RoundTrip(req)
}

func rewriteTo(target string) http.RoundTripper {
	return &rewriter{target: target, rt: http.DefaultTransport}
}
