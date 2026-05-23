// Storage helpers for the `commits` table — diff stats per commit SHA.
//
// Populated by the collector after every workflow_run upsert: we look up
// the head_sha and (if not already cached) fetch the commit detail from
// GitHub. Joined into the feature dataframe in ml-service so the
// regression has access to `files_changed`, `additions`, `deletions`.
package store

import (
	"context"
	"time"
)

// UpsertCommitParams is the slim row we persist. Message and full file
// list deliberately dropped — the model uses only counts, and storing
// the message bloats the table and adds nothing.
type UpsertCommitParams struct {
	SHA          string
	RepoID       int64
	Author       string
	Message      string
	FilesChanged int
	Additions    int
	Deletions    int
	CommittedAt  *time.Time
}

// UpsertCommit is idempotent — a SHA already in the table gets its
// stats refreshed (cheap, GitHub returns the same numbers anyway). On
// fresh insert returns the SHA; we don't bother with a separate id.
func (d *DB) UpsertCommit(ctx context.Context, p UpsertCommitParams) error {
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO commits (sha, repo_id, author, message, files_changed, additions, deletions, committed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (sha) DO UPDATE SET
		  author        = EXCLUDED.author,
		  files_changed = EXCLUDED.files_changed,
		  additions     = EXCLUDED.additions,
		  deletions     = EXCLUDED.deletions
	`,
		p.SHA, p.RepoID, nilIfEmpty(p.Author), nilIfEmpty(p.Message),
		nullIfZero(p.FilesChanged), nullIfZero(p.Additions), nullIfZero(p.Deletions),
		p.CommittedAt,
	)
	return err
}

// CommitExists is a cheap pre-flight to avoid re-fetching a SHA the
// collector has already pulled. Saves a GitHub API token per duplicate
// workflow_run pointing at the same SHA (very common — matrix builds).
func (d *DB) CommitExists(ctx context.Context, sha string) (bool, error) {
	var exists bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commits WHERE sha = $1)`, sha).Scan(&exists)
	return exists, err
}

// nullIfZero is a small helper to keep numeric columns NULL rather than
// 0 when GitHub didn't supply a value. Distinguishes "no data" from
// "empty diff" in downstream queries.
func nullIfZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
