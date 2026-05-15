package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Repo is the canonical "tracked repository" row.
//
// Status transitions: idle → fetching → synced. `error` is terminal until a
// resync clears it. Counts are denormalised (updated by collector) so the
// dataset cards render without a join.
type Repo struct {
	ID              int64     `json:"id"`
	Owner           string    `json:"owner"`
	Name            string    `json:"name"`
	GithubID        *int64    `json:"github_id,omitempty"`
	DefaultBranch   *string   `json:"default_branch,omitempty"`
	TrackedBranches []string  `json:"tracked_branches"`
	Status          string    `json:"status"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	OldestRunAt     *time.Time `json:"oldest_run_at,omitempty"`
	NewestRunAt     *time.Time `json:"newest_run_at,omitempty"`
	RunsCount       int64     `json:"runs_count"`
	JobsCount       int64     `json:"jobs_count"`
	LastError       *string   `json:"last_error,omitempty"`
	IsSeed          bool      `json:"is_seed"`
	AddedAt         time.Time `json:"added_at"`
}

func (r Repo) Slug() string { return r.Owner + "/" + r.Name }

// AddRepoParams is what the API hands to AddRepo. Owner/Name are required;
// the rest carry seed defaults when called from the bootstrap orchestrator.
type AddRepoParams struct {
	Owner           string
	Name            string
	TrackedBranches []string
	IsSeed          bool
}

// ParseGithubURL accepts the forms users typically paste:
//   - "owner/repo"
//   - "https://github.com/owner/repo"
//   - "https://github.com/owner/repo.git"
//   - "git@github.com:owner/repo.git"
//
// Strict parse — we'd rather reject a malformed URL with a clear error than
// silently truncate it.
func ParseGithubURL(s string) (owner, name string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", errors.New("repository URL is empty")
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	// SSH form
	if strings.HasPrefix(s, "git@github.com:") {
		s = strings.TrimPrefix(s, "git@github.com:")
	}
	// HTTPS form
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected owner/repo, got %q", s)
	}
	return parts[0], parts[1], nil
}

// AddRepo inserts a row. On UNIQUE conflict (owner, name) it returns the
// existing row — callers treat "already tracked" as a soft success.
func (d *DB) AddRepo(ctx context.Context, p AddRepoParams) (Repo, error) {
	if p.Owner == "" || p.Name == "" {
		return Repo{}, errors.New("owner and name are required")
	}
	if p.TrackedBranches == nil {
		p.TrackedBranches = []string{}
	}

	row := d.Pool.QueryRow(ctx, `
		INSERT INTO repos (owner, name, tracked_branches, is_seed, status)
		VALUES ($1, $2, $3, $4, 'idle')
		ON CONFLICT (owner, name) DO UPDATE
		  SET is_seed = repos.is_seed OR EXCLUDED.is_seed
		RETURNING id, owner, name, github_id, default_branch, tracked_branches,
		          status, last_synced_at, oldest_run_at, newest_run_at,
		          runs_count, jobs_count, last_error, is_seed, added_at
	`, p.Owner, p.Name, p.TrackedBranches, p.IsSeed)

	return scanRepo(row)
}

// ListRepos returns all tracked repositories, newest first.
func (d *DB) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := d.Pool.Query(ctx, `
		SELECT id, owner, name, github_id, default_branch, tracked_branches,
		       status, last_synced_at, oldest_run_at, newest_run_at,
		       runs_count, jobs_count, last_error, is_seed, added_at
		FROM repos
		ORDER BY added_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(...any) error
}

func scanRepo(s scanner) (Repo, error) {
	var r Repo
	err := s.Scan(
		&r.ID, &r.Owner, &r.Name, &r.GithubID, &r.DefaultBranch,
		&r.TrackedBranches, &r.Status, &r.LastSyncedAt, &r.OldestRunAt,
		&r.NewestRunAt, &r.RunsCount, &r.JobsCount, &r.LastError,
		&r.IsSeed, &r.AddedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Repo{}, err
		}
		return Repo{}, fmt.Errorf("scan repo: %w", err)
	}
	return r, nil
}
