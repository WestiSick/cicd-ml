// Package store wraps the Postgres connection pool.
//
// Queries live in feature-specific files (repos.go, bgjobs.go, ...). This file
// only handles connection lifecycle. We use pgx native, not database/sql, to
// get proper JSONB and array support without scanning hacks.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type DB struct {
	Pool *pgxpool.Pool
}

// Connect waits for Postgres to be reachable, then opens a pool.
// The retry loop accommodates Docker startup ordering — depends_on:healthy
// covers most cases but binary cold-start is still racy on slower hosts.
func Connect(ctx context.Context, dsn string) (*DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("POSTGRES_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute

	var pool *pgxpool.Pool
	deadline := time.Now().Add(60 * time.Second)
	for {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				break
			} else {
				err = pingErr
				pool.Close()
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("postgres unreachable after 60s: %w", err)
		}
		log.Warn().Err(err).Msg("postgres not ready, retrying")
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
}
