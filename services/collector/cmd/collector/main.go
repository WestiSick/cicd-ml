// Package main — collector worker.
//
// Pulls `bg_jobs` rows of kind `collect_history` / `refresh` and walks the
// GitHub Actions API for the target repo, persisting workflow_runs, jobs and
// commits with checkpoints in Postgres. Rate-limited; resumes after limit
// reset without losing progress.
//
// The collector is a separate container so its long-running pulls don't share
// goroutine schedulers or memory pressure with the API gateway. It writes
// the same `bg_jobs` rows the API surfaces over WebSocket — the UI sees
// progress live without any direct coupling.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	zerolog.TimeFieldFormat = time.RFC3339
	level, err := zerolog.ParseLevel(getenv("LOG_LEVEL", "info"))
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Info().Msg("collector starting (scaffold — pulls bg_jobs in upcoming iteration)")

	// Heartbeat loop while the real worker is being implemented.
	// Once internal/sync.Run is in place, this becomes:
	//   sync.New(db, redis, github).Run(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("collector stopping")
			return
		case <-t.C:
			log.Debug().Msg("collector heartbeat")
		}
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
