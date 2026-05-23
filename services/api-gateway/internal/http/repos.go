package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// GET /api/repos — list every tracked repository.
func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.db.ListRepos(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_repos_failed",
			"Could not load repositories", "Try refreshing the page.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

// POST /api/repos
//
// Body:
//
//	{
//	  "url": "https://github.com/owner/repo",
//	  "branches":      [...]?,
//	  "history_months": 3|6|12,        // default 6
//	  "github_token":  "ghp_...",       // optional, raises rate limit
//	  "auto_sync":     true             // default true; set false to skip
//	}
//
// Adds the repo to `repos` and (by default) immediately enqueues a
// `collect_history` bg_job so the user doesn't have to click "Sync"
// afterwards. The old behaviour was to leave the repo stuck in
// `idle` — confusing because the UI showed it but no data ever
// arrived.
func (s *Server) addRepo(w http.ResponseWriter, r *http.Request) {
	body := struct {
		URL           string   `json:"url"`
		Branches      []string `json:"branches"`
		HistoryMonths int      `json:"history_months"`
		GithubToken   string   `json:"github_token"`
		AutoSync      *bool    `json:"auto_sync"` // pointer so we can detect "absent"
	}{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body",
			"Request body is not valid JSON",
			"Paste the repository URL into the form and submit again.")
		return
	}

	owner, name, err := store.ParseGithubURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_repo_url",
			err.Error(),
			"Use a URL like https://github.com/owner/repo.")
		return
	}

	repo, err := s.db.AddRepo(r.Context(), store.AddRepoParams{
		Owner:           owner,
		Name:            name,
		TrackedBranches: body.Branches,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "add_repo_failed",
			"Could not save repository", "Retry — if it keeps failing, check the database is up.")
		return
	}

	_ = s.db.RecordActivity(r.Context(), "user", "add_repo", repo.Slug(),
		"repository added", true, nil)

	// Auto-sync: enqueue collect_history unless the caller opted out.
	// Default = on. Months default to BOOTSTRAP_DEFAULT_MONTHS (typically 6).
	autoSync := true
	if body.AutoSync != nil {
		autoSync = *body.AutoSync
	}
	if autoSync {
		months := body.HistoryMonths
		if months != 3 && months != 6 && months != 12 {
			months = s.cfg.BootstrapDefaultMonths
		}
		if _, err := s.db.EnqueueBGJob(r.Context(), store.JobKindCollectHistory, map[string]any{
			"repo":         repo.Slug(),
			"months":       months,
			"github_token": body.GithubToken,
		}); err != nil {
			// Non-fatal — the repo row is saved. User can click "Sync" later.
			writeJSON(w, http.StatusCreated, map[string]any{
				"repo": repo,
				"warning": "repo created but auto-sync could not be queued: " + err.Error(),
			})
			return
		}
	}

	writeJSON(w, http.StatusCreated, repo)
}

// POST /api/repos/{id}/sync
//
// Body (all optional):
//
//	{ "history_months": 6, "github_token": "ghp_..." }
//
// Enqueues a fresh `collect_history` bg_job for the repo. Used by the
// "Sync" button on the dataset card — gives the user a one-click way
// to fetch new runs without re-adding the repo.
//
// Idempotent: enqueuing while one is already queued/running is harmless
// (the collector's UPSERTs make repeated fetches no-ops on rows that
// already exist).
func (s *Server) syncRepo(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed",
			"Could not look up repository", "")
		return
	}

	body := struct {
		HistoryMonths int    `json:"history_months"`
		GithubToken   string `json:"github_token"`
	}{}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body is fine

	months := body.HistoryMonths
	if months != 3 && months != 6 && months != 12 {
		months = s.cfg.BootstrapDefaultMonths
	}

	job, err := s.db.EnqueueBGJob(r.Context(), store.JobKindCollectHistory, map[string]any{
		"repo":         repo.Slug(),
		"months":       months,
		"github_token": body.GithubToken,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "enqueue_sync_failed",
			"Could not enqueue sync job", "Retry in a moment.")
		return
	}

	_ = s.db.RecordActivity(r.Context(), "user", "sync_repo", repo.Slug(),
		"manual sync triggered", true, map[string]any{"bg_job_id": job.ID, "months": months})

	writeJSON(w, http.StatusAccepted, map[string]any{
		"bg_job_id": job.ID,
		"repo_id":   id,
		"message":   "Sync queued — watch the dataset card for progress.",
	})
}
