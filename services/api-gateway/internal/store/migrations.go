// Migrations run automatically when the api-gateway boots.
//
// We embed the .sql files at build time and execute them in a single
// transaction. The schema uses goose-style `-- +goose Up`/`-- +goose Down`
// directives so the same files also work with the goose CLI for ad-hoc
// administration (see Makefile). At runtime we only run "Up" — and we
// only apply files that haven't been applied yet, tracked in
// `schema_migrations`.
//
// Why embedded + auto-apply (no external goose container):
//   - One less moving part in compose.
//   - Migrations are version-locked to the binary.
//   - No race between "is api running" and "is migrator done".
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending migration files to the connected database.
// Idempotent: re-running after a clean boot is a no-op.
func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := listMigrationFiles()
	if err != nil {
		return err
	}

	applied, err := d.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, name := range files {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		// Strip goose directives so the SQL is plain.
		sqlText := stripGooseDirectives(string(body))

		log.Info().Str("migration", name).Msg("applying")
		tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, sqlText); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}

	log.Info().Int("count", len(files)).Msg("migrations up-to-date")
	return nil
}

func listMigrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *DB) appliedVersions(ctx context.Context) (map[string]bool, error) {
	rows, err := d.Pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// stripGooseDirectives keeps only the "Up" SQL.
// We split on the "-- +goose Down" marker, take the part before it,
// and strip the "-- +goose Up" / "-- +goose StatementBegin/End" lines.
func stripGooseDirectives(s string) string {
	if idx := strings.Index(s, "-- +goose Down"); idx >= 0 {
		s = s[:idx]
	}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "-- +goose") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
