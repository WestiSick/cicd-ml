// Snapshot auto-restore — speeds up the "fresh checkout → working demo"
// path from hours (real bootstrap fetching from GitHub) to a minute or
// two (load a pre-baked SQL dump).
//
// How it fits together:
//
//  1. The thesis author runs `make snapshot` once to produce
//     `db/seed/snapshot.sql.gz` containing INSERT statements for the
//     current dataset (repos, workflow_runs, jobs, commits, features,
//     models — everything).
//  2. The compose file bind-mounts `./db/seed/` into the api-gateway
//     container at `/var/lib/cicdml/seed/`.
//  3. On every startup, this function checks: is bootstrap_done false?
//     AND does the snapshot file exist? If both yes, it gunzips and
//     executes the SQL via pgx, then sets bootstrap_done=true.
//
// The check at runtime (rather than relying on Postgres'
// /docker-entrypoint-initdb.d/) is deliberate: that mechanism ONLY runs
// when the volume is empty. With ours, a reviewer can recover a stuck
// dev environment just by deleting the bootstrap_done row in
// system_state and restarting — the orchestrator notices the missing
// flag and reapplies.
package store

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// SnapshotResult records what happened so the caller can log it.
type SnapshotResult struct {
	Skipped       bool   // true if no restore was performed (no file, or already bootstrapped)
	Reason        string // why we skipped — useful in logs
	StatementsRun int    // number of top-level SQL statements applied
	BytesRead     int64  // size of decompressed SQL
	Elapsed       time.Duration
}

// RestoreSnapshotIfPresent restores `db/seed/snapshot.sql.gz` if:
//   - the file exists at `snapshotPath`, AND
//   - bootstrap_done is currently false in system_state.
//
// On success it sets bootstrap_done=true so the orchestrator on /setup
// recognises a ready system. Safe to call on every startup — it's a
// cheap stat() when there's nothing to do.
//
// Errors mid-restore are returned to the caller. They DON'T mark
// bootstrap_done — the caller can decide whether to fall back to the
// regular /setup flow or surface the error.
func (d *DB) RestoreSnapshotIfPresent(ctx context.Context, snapshotPath string) (SnapshotResult, error) {
	res := SnapshotResult{}

	// Cheap pre-flight: missing file is the common case in prod, where
	// the operator never seeded a snapshot.
	st, err := os.Stat(snapshotPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			res.Skipped = true
			res.Reason = "no snapshot file at " + snapshotPath
			return res, nil
		}
		return res, fmt.Errorf("stat snapshot: %w", err)
	}
	if st.IsDir() {
		res.Skipped = true
		res.Reason = "snapshot path is a directory"
		return res, nil
	}

	done, err := d.isBootstrapDone(ctx)
	if err != nil {
		return res, fmt.Errorf("check bootstrap_done: %w", err)
	}
	if done {
		res.Skipped = true
		res.Reason = "bootstrap_done already true"
		return res, nil
	}

	log.Info().Str("path", snapshotPath).Int64("size", st.Size()).Msg("restoring snapshot")
	start := time.Now()

	f, err := os.Open(snapshotPath)
	if err != nil {
		return res, fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return res, fmt.Errorf("gunzip snapshot: %w", err)
	}
	defer gz.Close()

	// Read fully into memory — for the thesis scale (≤ a few MB
	// compressed → ~20MB uncompressed), this is fine and lets us use
	// pgx's bulk Exec without managing streaming chunks.
	sqlBytes, err := io.ReadAll(gz)
	if err != nil {
		return res, fmt.Errorf("read decompressed snapshot: %w", err)
	}
	res.BytesRead = int64(len(sqlBytes))

	// pgx.Conn.Exec handles multi-statement scripts — the same way
	// migrations are applied. We wrap in a transaction so a partial
	// restore can be rolled back rather than leaving a half-loaded DB.
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return res, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
		return res, fmt.Errorf("apply snapshot SQL: %w", err)
	}
	// pgx doesn't report statement count for multi-statement Exec; we
	// just count semicolons as a rough indicator for the log line.
	res.StatementsRun = approximateStatementCount(sqlBytes)

	// Mark bootstrap as done so the orchestrator on /setup doesn't try
	// to re-run. Use the same helper the orchestrator does.
	if err := d.setBootstrapDoneInTx(ctx, tx, true); err != nil {
		return res, fmt.Errorf("set bootstrap_done: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit restore: %w", err)
	}

	res.Elapsed = time.Since(start)
	log.Info().
		Int("statements", res.StatementsRun).
		Int64("bytes", res.BytesRead).
		Dur("elapsed", res.Elapsed).
		Msg("snapshot restored")
	return res, nil
}

func (d *DB) isBootstrapDone(ctx context.Context) (bool, error) {
	var raw []byte
	err := d.Pool.QueryRow(ctx, `SELECT value FROM system_state WHERE key = $1`, keyBootstrapDone).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, err
	}
	return v, nil
}

func (d *DB) setBootstrapDoneInTx(ctx context.Context, tx pgx.Tx, done bool) error {
	val, _ := json.Marshal(done)
	_, err := tx.Exec(ctx, `
		INSERT INTO system_state (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, keyBootstrapDone, val)
	return err
}

// approximateStatementCount eyeballs the top-level statement count by
// counting non-quoted semicolons. Used only for the log line — exact
// figure isn't important, only the order of magnitude (helps spot
// "snapshot was truncated" vs "snapshot looked complete").
func approximateStatementCount(sql []byte) int {
	n := 0
	inSingle := false
	for _, b := range sql {
		switch b {
		case '\'':
			inSingle = !inSingle
		case ';':
			if !inSingle {
				n++
			}
		}
	}
	return n
}
