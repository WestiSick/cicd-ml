// Package main — standalone collector worker.
//
// Polls `bg_jobs` for `collect_history` / `refresh` rows and walks the
// GitHub Actions API (workflow_runs, jobs, commits) writing into Postgres
// with the same syncer the api-gateway used in single-binary mode.
//
// Runs in its own container so:
//   - long-running GitHub API pulls don't share goroutine schedulers with
//     the user-facing API gateway;
//   - rate-limit waits don't tie up gateway HTTP workers;
//   - the operator can scale collectors horizontally (multiple replicas;
//     SKIP LOCKED on bg_jobs handles the load balance).
//
// Progress + cancellation updates are pushed to the gateway over HTTP
// (POST /api/internal/broadcast) — see internal/bgjobs.HTTPBroadcaster.
// The gateway re-publishes on its WebSocket hub so /datasets sees live
// progress without polling.
//
// When this binary is NOT running, the api-gateway claims the same kinds
// itself (single-binary mode is still supported). To switch modes, set
// ENABLED_BG_KINDS on the gateway to exclude collect_history/refresh.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/bgjobs"
	"github.com/buzdin/cicd-ml/api-gateway/internal/config"
	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
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

	// Migrations are owned by the api-gateway. The collector connects
	// to the same DB and assumes the schema is already up to date.

	broadcaster := bgjobs.NewHTTPBroadcaster(cfg.GatewayInternalBase)
	runner := bgjobs.NewRunnerWithBroadcaster(db, broadcaster)

	// Only the io-bound pool. compute pool gets a zero-length kind list
	// via RestrictKinds, leaving the gateway to handle bootstrap /
	// compute_features / train_model.
	runner.RestrictKinds(map[string]bool{
		store.JobKindCollectHistory: true,
		store.JobKindRefresh:        true,
	})

	syncer := &gh.Syncer{DB: db}
	runner.Register(store.JobKindCollectHistory, collectHandler(syncer, db))
	runner.Register(store.JobKindRefresh, collectHandler(syncer, db))

	log.Info().Str("gateway", cfg.GatewayInternalBase).Msg("collector starting")
	runner.Run(ctx)
	log.Info().Msg("collector stopped")
}

// collectHandler — same logic as the gateway's collectHandler, copied
// here so the standalone binary doesn't need access to private gateway
// internals. Both versions stay in sync via code review; if either
// drifts, the symptom is "live progress strip looks slightly different
// between single-binary and split-binary mode".
func collectHandler(syncer *gh.Syncer, db *store.DB) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		var payload struct {
			Repo        string `json:"repo"`
			Months      int    `json:"months"`
			GithubToken string `json:"github_token"`
		}
		if err := decodeJSON(job.Payload, &payload); err != nil {
			return err
		}
		owner, name, err := store.ParseGithubURL(payload.Repo)
		if err != nil {
			return err
		}
		if payload.Months == 0 {
			payload.Months = 6
		}

		if existing, err := db.LookupRepo(ctx, owner, name); err == nil && existing.Status == "paused" {
			progress(1, 1, "skipped — repository is paused", "")
			return nil
		}

		token := payload.GithubToken
		if token == "" {
			if persisted, err := db.GetGithubPAT(ctx); err == nil {
				token = persisted
			}
		}

		syncer.Client = gh.NewClient(token)
		return syncer.Run(ctx, gh.RunInput{
			RepoOwner: owner,
			RepoName:  name,
			Months:    payload.Months,
		}, gh.Progress(progress))
	}
}
