-- +goose Up
-- +goose StatementBegin

-- Per-file diff stats for each commit.
--
-- Why a separate table (vs. JSON on `commits`):
--   - We want to classify files (backend/frontend/test/docs/config) at
--     feature-engineering time in Python rather than at collection
--     time in Go — keeps the bucket taxonomy mutable without a re-
--     fetch from GitHub. JSON would work too but loses the index.
--   - SHA-level joins are cheap with a btree on (sha) already provided
--     by the composite PK; per-bucket aggregates are computed in
--     ml-service's pandas pipeline, not SQL.
--
-- Storage envelope: ~5-20 files per commit × ~10k commits per repo over
-- 12 months ≈ 100k-200k rows per active repo. Each row is ~120 bytes,
-- so the table stays under 50 MB even with 5+ repos — small enough that
-- the ON DELETE CASCADE from commits cleans up without a slow scan.
CREATE TABLE IF NOT EXISTS commit_files (
    sha       TEXT    NOT NULL REFERENCES commits(sha) ON DELETE CASCADE,
    filename  TEXT    NOT NULL,
    status    TEXT,                                       -- 'added' | 'modified' | 'removed' | 'renamed'
    additions INTEGER NOT NULL DEFAULT 0,
    deletions INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (sha, filename)
);

-- Lookup by sha alone — used by the ml-service feature pipeline when
-- aggregating per-commit bucket counts. The composite PK gives this
-- for free, but an explicit alias makes EXPLAIN output readable.
CREATE INDEX IF NOT EXISTS idx_commit_files_sha ON commit_files(sha);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS commit_files;
-- +goose StatementEnd
