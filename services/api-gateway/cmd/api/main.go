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

	hub := ws.NewHub()

	// Background job runner. Handlers register their kind → function map.
	// For now bootstrap orchestrator owns the bootstrap kind; the rest are
	// stubs that simulate progress until the real workers land. Each stub
	// publishes the same WebSocket events the real worker will — so the
	// frontend doesn't change when we swap them in.
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

	// Simulate stays as a stub for now — the real simulator endpoint is
	// invoked directly from POST /api/simulator/run (synchronous, < 1s).
	_, _, _, simulateH := bootstrap.StubHandlers()
	runner.Register(store.JobKindSimulate, wrap(simulateH))

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

// collectHandler binds a github.Syncer to the bgjobs.Handler contract.
//
// The bg_jobs.payload carries {repo, months, github_token} — see
// bootstrap.Orchestrator.Handler where these rows are enqueued.
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
		syncer.Client = gh.NewClient(payload.GithubToken)
		return syncer.Run(ctx, gh.RunInput{
			RepoOwner: owner,
			RepoName:  name,
			Months:    payload.Months,
		}, gh.Progress(progress))
	}
}
