// Package main — standalone simulator worker.
//
// Polls `bg_jobs` for `simulate` rows and runs the time-stepped
// scheduler simulation (see internal/scheduler) against historical
// jobs in the chosen window, persisting one row per strategy into
// `sim_runs`. Same code path as POST /api/simulator/run — both
// callers funnel through internal/simrun.
//
// Why a separate container: simulation is CPU-burst (sub-second per
// strategy on our dataset scale) but bg_jobs concurrency means the
// gateway's compute pool could be blocked by a long-running
// compute_features / train_model when a Setup orchestrator enqueues a
// simulate at the end. Running simulator in its own container ensures
// the demo finishes promptly.
//
// Progress broadcasting is the same HTTPBroadcaster the collector uses
// — see internal/bgjobs.HTTPBroadcaster + /api/internal/broadcast.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/bgjobs"
	"github.com/buzdin/cicd-ml/api-gateway/internal/config"
	"github.com/buzdin/cicd-ml/api-gateway/internal/scheduler"
	"github.com/buzdin/cicd-ml/api-gateway/internal/simrun"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

func main() {
	zerolog.TimeFieldFormat = time.RFC3339
	cfg := config.Load()
	zerolog.SetGlobalLevel(cfg.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db, err := store.Connect(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres connect")
	}
	defer db.Close()

	broadcaster := bgjobs.NewHTTPBroadcaster(cfg.GatewayInternalBase)
	runner := bgjobs.NewRunnerWithBroadcaster(db, broadcaster)
	runner.RestrictKinds(map[string]bool{store.JobKindSimulate: true})
	runner.Register(store.JobKindSimulate, simulateHandler(db))

	log.Info().Str("gateway", cfg.GatewayInternalBase).Msg("simulator starting")
	runner.Run(ctx)
	log.Info().Msg("simulator stopped")
}

// simulateHandler — bg_jobs payload shape:
//
//	{
//	  "window_start":   "ISO-8601" (optional, default "now - 7d"),
//	  "window_end":     "ISO-8601" (optional, default "now"),
//	  "repo_ids":       [int...],   (optional, default all)
//	  "strategies":     ["fifo",...] (optional, default all)
//	  "runners":        int,         (optional, default 1)
//	  "sla_main_sec":    int,
//	  "sla_feature_sec": int
//	}
//
// Emits one progress update per strategy completed so the UI can show
// "1/4 strategies done, 2/4, ...".
func simulateHandler(db *store.DB) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		var payload struct {
			WindowStart   *time.Time `json:"window_start"`
			WindowEnd     *time.Time `json:"window_end"`
			RepoIDs       []int64    `json:"repo_ids"`
			Strategies    []string   `json:"strategies"`
			Runners       int        `json:"runners"`
			SLAMainSec    int        `json:"sla_main_sec"`
			SLAFeatureSec int        `json:"sla_feature_sec"`
		}
		if len(job.Payload) > 0 {
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return fmt.Errorf("decode simulate payload: %w", err)
			}
		}

		// CustomWeights read directly from system_state — keeps the
		// worker independent of the HTTP layer's helper.
		weights := readCustomWeights(ctx, db)

		params := simrun.NewParams(simrun.Params{
			WindowStart:   ptrToTime(payload.WindowStart),
			WindowEnd:     ptrToTime(payload.WindowEnd),
			RepoIDs:       payload.RepoIDs,
			Strategies:    payload.Strategies,
			Runners:       payload.Runners,
			SLAMainSec:    payload.SLAMainSec,
			SLAFeatureSec: payload.SLAFeatureSec,
			CustomWeights: weights,
		})

		progress(0, len(params.Strategies),
			fmt.Sprintf("simulator: %d strategies, window %s → %s",
				len(params.Strategies),
				params.WindowStart.Format("2006-01-02"),
				params.WindowEnd.Format("2006-01-02"),
			), "")

		out, err := simrun.Run(ctx, db, params)
		if err != nil {
			return err
		}
		progress(len(params.Strategies), len(params.Strategies),
			fmt.Sprintf("simulator done: %d strategies, %d jobs replayed",
				len(out.Metrics), out.JobsRun), "")
		return nil
	}
}

func ptrToTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// readCustomWeights pulls scheduler weights straight from system_state
// without the HTTP-layer helper. Returns defaults on any error so
// simulation can proceed even when the user never tuned weights.
func readCustomWeights(ctx context.Context, db *store.DB) scheduler.CustomWeights {
	var raw []byte
	err := db.Pool.QueryRow(ctx, `SELECT value FROM system_state WHERE key = 'custom_weights'`).Scan(&raw)
	if err != nil {
		return scheduler.DefaultCustomWeights()
	}
	var w scheduler.CustomWeights
	if err := json.Unmarshal(raw, &w); err != nil {
		return scheduler.DefaultCustomWeights()
	}
	if w.ShortJob == 0 && w.Deadline == 0 && w.Branch == 0 {
		return scheduler.DefaultCustomWeights()
	}
	return w
}
