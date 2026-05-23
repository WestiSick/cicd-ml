// Storage helpers for the `commits` table — diff stats per commit SHA.
//
// Populated by the collector after every workflow_run upsert: we look up
// the head_sha and (if not already cached) fetch the commit detail from
// GitHub. Joined into the feature dataframe in ml-service so the
// regression has access to `files_changed`, `additions`, `deletions`.
package store

import (
	"context"
	"fmt"
	"strings"
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
//
// NOTE: prefer CommitFullyCached for new code — by itself this returns
// true for SHAs whose `commits` row exists but whose `commit_files` rows
// were never populated (everything ingested before the commit_files
// table existed in migration 00004). Re-sync paths that want to backfill
// per-file diffs MUST use CommitFullyCached, otherwise they keep
// short-circuiting on the historical commits we need to enrich.
func (d *DB) CommitExists(ctx context.Context, sha string) (bool, error) {
	var exists bool
	err := d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commits WHERE sha = $1)`, sha).Scan(&exists)
	return exists, err
}

// CommitFullyCached returns true only when BOTH the `commits` row and
// at least one `commit_files` row exist for this SHA. Use this as the
// skip-guard in collector/webhook paths so that commits which predate
// the per-file-diff schema get re-fetched once and then cached forever.
//
// The "at least one commit_files row" check is correct because GitHub
// always returns a non-empty Files[] array for any non-merge commit
// (and merge commits are rare in CI-triggering pushes); a SHA with zero
// commit_files rows is almost certainly a pre-migration artefact, not
// a legitimate empty diff.
func (d *DB) CommitFullyCached(ctx context.Context, sha string) (bool, error) {
	var ok bool
	err := d.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM commits WHERE sha = $1)
		   AND EXISTS(SELECT 1 FROM commit_files WHERE sha = $1)
	`, sha).Scan(&ok)
	return ok, err
}

// CommitFileParams is one row for the per-file diff table. The ml-
// service classifies filenames into backend/frontend/test/docs/config
// buckets at feature-engineering time; we just store the raw paths.
type CommitFileParams struct {
	Filename  string
	Status    string // 'added' | 'modified' | 'removed' | 'renamed' — verbatim from GitHub
	Additions int
	Deletions int
}

// BulkInsertCommitFiles is idempotent — a (sha, filename) already in the
// table gets its counts refreshed. Returns the number of attempted rows
// (some may have been duplicates and updated rather than inserted).
//
// We accept a slice rather than streaming so the call site can decide
// whether to commit for an empty diff (no-op) without us doing an
// otherwise-pointless transaction.
func (d *DB) BulkInsertCommitFiles(ctx context.Context, sha string, files []CommitFileParams) error {
	if len(files) == 0 {
		return nil
	}
	// Build a multi-row VALUES clause: cheaper than N round-trips, and
	// pgx handles the parameter packing. Cap at 1000 per batch — Postgres
	// errors at 65535 parameters total (5 per row × 13000+); 1000 leaves
	// plenty of room and a typical commit has well under that anyway.
	const batchSize = 1000
	for start := 0; start < len(files); start += batchSize {
		end := start + batchSize
		if end > len(files) {
			end = len(files)
		}
		chunk := files[start:end]

		args := make([]any, 0, len(chunk)*5)
		var sb strings.Builder
		sb.WriteString(`INSERT INTO commit_files (sha, filename, status, additions, deletions) VALUES `)
		for i, f := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			base := i*5 + 1
			fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d)", base, base+1, base+2, base+3, base+4)
			args = append(args, sha, f.Filename, nilIfEmpty(f.Status), f.Additions, f.Deletions)
		}
		sb.WriteString(` ON CONFLICT (sha, filename) DO UPDATE SET
			status    = EXCLUDED.status,
			additions = EXCLUDED.additions,
			deletions = EXCLUDED.deletions`)
		if _, err := d.Pool.Exec(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("bulk insert commit_files: %w", err)
		}
	}
	return nil
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
