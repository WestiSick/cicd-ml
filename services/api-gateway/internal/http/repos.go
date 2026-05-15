package http

import (
	"encoding/json"
	"net/http"

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
// Body: {"url": "https://github.com/owner/repo", "branches": [...]?}
func (s *Server) addRepo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL      string   `json:"url"`
		Branches []string `json:"branches"`
	}
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

	writeJSON(w, http.StatusCreated, repo)
}
