// Package main — api-gateway entrypoint.
//
// Responsibilities:
//   - HTTP REST API (/api/*) for the frontend.
//   - WebSocket channels (/ws/*) for real-time updates.
//   - GitHub webhook receiver (/webhooks/github).
//   - In-process background job runner (bg_jobs dispatcher).
//   - Bootstrap orchestrator for first-run setup.
//
// The service is intentionally a single binary: simpler ops, single
// source of truth for the WebSocket event bus, and aligned with "one
// container per concern" only where it actually pays off (collector,
// ml-service).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/bgjobs"
	"github.com/buzdin/cicd-ml/api-gateway/internal/bootstrap"
	"github.com/buzdin/cicd-ml/api-gateway/internal/config"
	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
	httpapi "github.com/buzdin/cicd-ml/api-gateway/internal/http"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ml"
	"github.com/buzdin/cicd-ml/api-gateway/internal/scheduler"
	"github.com/buzdin/cicd-ml/api-gateway/internal/simrun"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ws"
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

	if err := db.Migrate(ctx); err != nil {
		log.Fatal().Err(err).Msg("migrate")
	}

	// Best-effort snapshot auto-restore — see store/snapshot.go for the
	// full rationale. In short: if `/var/lib/cicdml/seed/snapshot.sql.gz`
	// exists AND bootstrap_done is false, we apply the pre-baked dump and
	// flip bootstrap_done. This turns the reviewer's first-run experience
	// from "wait an hour for GitHub fetch" into "1–2 minutes total".
	//
	// Failure here is NOT fatal: we log and continue. Worst case the user
	// goes through the normal /setup flow.
	snapshotPath := getenv("SNAPSHOT_PATH", "/var/lib/cicdml/seed/snapshot.sql.gz")
	if res, err := db.RestoreSnapshotIfPresent(ctx, snapshotPath); err != nil {
		log.Warn().Err(err).Str("path", snapshotPath).Msg("snapshot auto-restore failed; falling back to /setup")
	} else if !res.Skipped {
		log.Info().
			Int("statements", res.StatementsRun).
			Dur("elapsed", res.Elapsed).
			Msg("auto-restored snapshot — system is ready without /setup")
	}

	hub := ws.NewHub()

	// Background job runner. Handlers register their kind → function map.
	// Handlers for every kind are registered unconditionally — that way
	// even in multi-binary mode the gateway can still claim a kind if
	// the dedicated worker is offline. The pool kind-filter (below)
	// decides which kinds we actually claim at runtime.
	runner := bgjobs.NewRunner(db, hub)

	orchestrator := bootstrap.NewOrchestrator(db)
	runner.Register(store.JobKindBootstrap, wrap(orchestrator.Handler()))

	// Real GitHub Actions collector for collect_history bg_jobs.
	syncer := &gh.Syncer{DB: db}
	runner.Register(store.JobKindCollectHistory, collectHandler(syncer))

	// Real ML training handler — proxies into the Python ml-service.
	mlClient := ml.NewClient(cfg.MLBaseURL)
	runner.Register(store.JobKindTrainModel, trainHandler(mlClient))

	// Feature materialisation calls ml-service; the row count it reports
	// goes through as bg_job progress so the UI shows real numbers.
	runner.Register(store.JobKindComputeFeatures, computeFeaturesHandler(mlClient))

	// Simulate handler — real implementation in single-binary mode. When
	// the standalone simulator container is deployed and the gateway's
	// ENABLED_BG_KINDS excludes "simulate", this registration is dead
	// code (the kind-filter prevents the pool from claiming simulate).
	runner.Register(store.JobKindSimulate, simulateHandler(db))

	// Optional kind-restriction. When the collector + simulator are
	// deployed as separate containers, set ENABLED_BG_KINDS on the
	// api-gateway to `bootstrap,compute_features,train_model` so the
	// gateway doesn't race the dedicated workers for collect_history /
	// refresh / simulate. Empty (default) = claim everything.
	if allowed := parseKindList(cfg.EnabledBGKinds); len(allowed) > 0 {
		runner.RestrictKinds(allowed)
		log.Info().Strs("allowed", keysOf(allowed)).Msg("bg-jobs runner restricted to a subset of kinds")
	}

	go runner.Run(ctx)
	go bootstrap.FinishOnDone(ctx, db)

	srv := httpapi.NewServer(cfg, db, hub, mlClient)

	httpSrv := &http.Server{
		Addr:              cfg.Bind,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("addr", cfg.Bind).Msg("api-gateway listening")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("listen")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// getenv is the inline equivalent of config.getenv — kept here so the
// snapshot path override doesn't need a round-trip through the config
// struct (only one caller, and this main file already pulls os anyway).
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// parseKindList splits a comma-separated env var into a set of allowed
// kinds. Whitespace around tokens is trimmed; empty tokens are skipped.
func parseKindList(s string) map[string]bool {
	out := map[string]bool{}
	if s == "" {
		return out
	}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := trimSpace(s[start:i])
			if tok != "" {
				out[tok] = true
			}
			start = i + 1
		}
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func trimSpace(s string) string {
	lo, hi := 0, len(s)
	for lo < hi && (s[lo] == ' ' || s[lo] == '\t') {
		lo++
	}
	for hi > lo && (s[hi-1] == ' ' || s[hi-1] == '\t') {
		hi--
	}
	return s[lo:hi]
}

// wrap adapts a func with raw `func(int, int, string, string)` progress
// (used in package-internal contracts to avoid an import cycle with
// bgjobs) to the bgjobs.Handler type.
func wrap(fn func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		return fn(ctx, job, func(p, total int, msg, logs string) {
			progress(p, total, msg, logs)
		})
	}
}

// trainHandler binds the ml.Client to the bg_jobs train_model contract.
//
// bg_jobs payload shape (set by bootstrap orchestrator):
//
//	{"algo": "xgboost"}                  // minimal — train on full dataset
//	{"algo": "xgboost", "params": {...}} // with hyperparameters
//	{"algo": "xgboost", "repo_ids": [..]} // scoped to repos
//
// The handler emits coarse progress (0/3, 1/3, 2/3, 3/3) — fine-grained
// per-iteration progress lives inside ml-service and will eventually be
// streamed via /ws/training/:id (next iteration).
func trainHandler(mlClient *ml.Client) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		var payload struct {
			Algo         string         `json:"algo"`
			Params       map[string]any `json:"params"`
			RepoIDs      []int64        `json:"repo_ids"`
			Activate     bool           `json:"activate"`
			Name         string         `json:"name"`
			OptunaTrials int            `json:"optuna_trials"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("train_model payload: %w", err)
		}
		if payload.Algo == "" {
			return fmt.Errorf("train_model: algo is required")
		}

		// Optuna path: search → refit → persist. Same bg_jobs shape as
		// a plain training so the UI doesn't have to special-case it —
		// listening to /ws/bg-jobs is enough for live progress.
		if payload.OptunaTrials >= 2 {
			progress(1, 3, fmt.Sprintf("Optuna search: %s × %d trials", payload.Algo, payload.OptunaTrials), "")
			resp, err := mlClient.TrainOptuna(ctx, ml.OptunaRequest{
				Algo:          payload.Algo,
				NTrials:       payload.OptunaTrials,
				RepoIDs:       payload.RepoIDs,
				Name:          payload.Name,
				TrainingJobID: job.ID,
				Activate:      payload.Activate,
			})
			if err != nil {
				return err
			}
			progress(2, 3, fmt.Sprintf("best_mae=%.1fs after %d trials", resp.BestMetrics["mae_test_sec"], resp.NTrials), "")
			progress(3, 3, fmt.Sprintf("done — %s (model #%d)", resp.Name, resp.ModelID), "")
			return nil
		}

		progress(1, 3, "calling ml-service /train: "+payload.Algo, "")

		req := ml.TrainRequest{
			Algo:          payload.Algo,
			Params:        payload.Params,
			RepoIDs:       payload.RepoIDs,
			Name:          payload.Name,
			TrainingJobID: job.ID,
			Activate:      payload.Activate,
		}
		resp, err := mlClient.Train(ctx, req)
		if err != nil {
			return err
		}

		progress(2, 3, fmt.Sprintf("trained %s (model #%d) — test_mae=%.1fs", resp.Name, resp.ModelID, resp.Metrics["mae_test_sec"]), "")
		progress(3, 3, fmt.Sprintf("done — %s (model #%d)", resp.Name, resp.ModelID), "")
		return nil
	}
}

// computeFeaturesHandler proxies the bg_job into ml-service's
// /features/build endpoint. Payload schema:
//
//	{"scope": "all"}              // recompute everything
//	{"repo_ids": [1, 3]}          // limit to specific repos
//
// The handler emits a 3-step coarse progress (start / call / done) —
// the actual materialisation inside ml-service is one bulk SQL upsert
// so per-row progress wouldn't tell the user anything useful.
func computeFeaturesHandler(mlClient *ml.Client) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		var payload struct {
			RepoIDs []int64 `json:"repo_ids"`
		}
		_ = json.Unmarshal(job.Payload, &payload) // empty payload is fine

		progress(1, 3, "calling ml-service /features/build", "")
		resp, err := mlClient.BuildFeatures(ctx, ml.BuildFeaturesRequest{RepoIDs: payload.RepoIDs})
		if err != nil {
			return err
		}
		progress(2, 3, fmt.Sprintf("%d feature vectors materialised", resp.Written), "")
		progress(3, 3, fmt.Sprintf("done — %d features × %d jobs", resp.FeatureCount, resp.Jobs), "")
		return nil
	}
}

// simulateHandler — promotes the (previously stub) simulate bg_job to
// real execution. Same logic as POST /api/simulator/run: load window →
// project → run every requested strategy → persist one sim_runs row.
//
// Lives in api-gateway main so single-binary mode also runs real
// simulations on bootstrap finish. The standalone simulator binary
// uses an identical handler from cmd/simulator/main.go.
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

		// Read scheduler weights from system_state directly — avoids
		// pulling the http package into main just for one helper.
		var weightsRaw []byte
		w := scheduler.DefaultCustomWeights()
		_ = db.Pool.QueryRow(ctx,
			`SELECT value FROM system_state WHERE key = 'custom_weights'`).Scan(&weightsRaw)
		if len(weightsRaw) > 0 {
			_ = json.Unmarshal(weightsRaw, &w)
		}

		params := simrun.NewParams(simrun.Params{
			WindowStart:   timeOrZero(payload.WindowStart),
			WindowEnd:     timeOrZero(payload.WindowEnd),
			RepoIDs:       payload.RepoIDs,
			Strategies:    payload.Strategies,
			Runners:       payload.Runners,
			SLAMainSec:    payload.SLAMainSec,
			SLAFeatureSec: payload.SLAFeatureSec,
			CustomWeights: w,
		})

		progress(0, len(params.Strategies),
			fmt.Sprintf("simulator: %d strategies", len(params.Strategies)), "")

		out, err := simrun.Run(ctx, db, params)
		if err != nil {
			return err
		}
		progress(len(params.Strategies), len(params.Strategies),
			fmt.Sprintf("simulator done: %d jobs replayed", out.JobsRun), "")
		return nil
	}
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// collectHandler binds a github.Syncer to the bgjobs.Handler contract.
//
// The bg_jobs.payload carries {repo, months, github_token} — see
// bootstrap.Orchestrator.Handler where these rows are enqueued.
//
// Token resolution priority: payload.github_token → persisted PAT from
// system_state (set via /admin/settings) → empty (unauthenticated, 60/h).
// Pause is honoured here as a safety net — the UI's Pause button just
// flips the repo status; this handler skips with a friendly bg_job message
// instead of running a sync the user explicitly didn't want.
func collectHandler(syncer *gh.Syncer) bgjobs.Handler {
	return func(ctx context.Context, job store.BGJob, progress bgjobs.ProgressFn) error {
		var payload struct {
			Repo        string `json:"repo"`
			Months      int    `json:"months"`
			GithubToken string `json:"github_token"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("collect_history payload: %w", err)
		}
		owner, name, err := store.ParseGithubURL(payload.Repo)
		if err != nil {
			return fmt.Errorf("collect_history repo: %w", err)
		}
		if payload.Months == 0 {
			payload.Months = 6
		}

		// Pause check: skip with a no-op if the repo was paused after the
		// bg_job was enqueued (e.g. user clicked Sync then Pause back-to-back).
		if existing, err := syncer.DB.LookupRepo(ctx, owner, name); err == nil && existing.Status == "paused" {
			progress(1, 1, "skipped — repository is paused", "")
			return nil
		}

		token := payload.GithubToken
		if token == "" {
			if persisted, err := syncer.DB.GetGithubPAT(ctx); err == nil {
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
