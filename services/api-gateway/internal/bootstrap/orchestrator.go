// Package bootstrap drives the first-run setup flow.
//
// Triggered by POST /api/setup/start with a payload selecting which seed
// repos to ingest and which models to pre-train. The orchestrator creates
// the `repos` rows and runs the chain:
//
//	collect_history (per repo)   ──►   compute_features   ──►   train_model (per algo)
//
// The bootstrap job is *long-running*: it waits for each phase to finish
// before enqueuing the next. This dependency is critical — the parallel
// bg-jobs runner (3 compute workers) would otherwise pick up train_model
// while collect_history is still gathering data, and training would fail
// with InsufficientDataError before any row landed in `jobs`.
//
// Why orchestrate in code rather than declare dependencies in `bg_jobs`:
//   - Keeps the schema and runner simple — a single "is queued?" gate.
//   - The orchestrator can give the UI rich progress text per phase
//     ("data collection: 5/7 repos done") which a generic dependency
//     graph couldn't.
//   - The user-facing state machine on /setup mirrors these phases 1:1.
package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// SetupRequest is the payload sent from the /setup page.
type SetupRequest struct {
	GithubToken string   `json:"github_token,omitempty"`
	Repos       []string `json:"repos"`       // owner/name slugs
	HistoryMonths int    `json:"history_months"`
	Models      []string `json:"models"`      // algo ids: linear, rf, xgboost, lightgbm, mlp, lstm
}

// Validate enforces the minimal preconditions. The frontend duplicates
// these in the form so the user sees errors before submission; here we
// also defend the API.
func (r SetupRequest) Validate() error {
	if len(r.Repos) == 0 {
		return fmt.Errorf("at least one repository required")
	}
	if len(r.Models) == 0 {
		return fmt.Errorf("at least one model required")
	}
	if r.HistoryMonths != 3 && r.HistoryMonths != 6 && r.HistoryMonths != 12 {
		return fmt.Errorf("history_months must be 3, 6, or 12")
	}
	return nil
}

type Orchestrator struct {
	db *store.DB
}

func NewOrchestrator(db *store.DB) *Orchestrator { return &Orchestrator{db: db} }

// Start registers the repos and enqueues the bootstrap bg_job that will
// chain the rest of the pipeline. Returns the bootstrap job id so the
// frontend can subscribe to /ws/bg-jobs and filter for it.
func (o *Orchestrator) Start(ctx context.Context, req SetupRequest) (int64, error) {
	if err := req.Validate(); err != nil {
		return 0, err
	}

	// Register the repos. Existing ones are a no-op (UNIQUE conflict).
	for _, slug := range req.Repos {
		owner, name, err := store.ParseGithubURL(slug)
		if err != nil {
			return 0, fmt.Errorf("repo %q: %w", slug, err)
		}
		if _, err := o.db.AddRepo(ctx, store.AddRepoParams{
			Owner:  owner,
			Name:   name,
			IsSeed: true,
		}); err != nil {
			return 0, fmt.Errorf("add repo %s/%s: %w", owner, name, err)
		}
		_ = o.db.RecordActivity(ctx, "setup", "add_repo", owner+"/"+name, "added during setup", true, nil)
	}

	// Enqueue the bootstrap chain — the Handler below picks it up and
	// drives the three phases.
	job, err := o.db.EnqueueBGJob(ctx, store.JobKindBootstrap, req)
	if err != nil {
		return 0, fmt.Errorf("enqueue bootstrap: %w", err)
	}
	log.Info().Int64("id", job.ID).Int("repos", len(req.Repos)).Int("models", len(req.Models)).Msg("setup queued")
	return job.ID, nil
}

// Handler returns the bgjobs.Handler bound to this orchestrator.
//
// Three phases (matches /setup UI):
//   1. data collection   — enqueue collect_history per repo, wait for terminal state
//   2. feature extraction — enqueue compute_features, wait
//   3. model training    — enqueue train_model per algo, wait
//
// We wait on phase 1 because compute_features and train_model both
// depend on rows in `jobs`. Phase 3's wait is so the bootstrap row
// itself shows "done" only when the whole chain finishes — the UI's
// "Bootstrapping…" screen looks reasonable.
//
// Rate-limited collect_history naturally sleeps inside its handler.
// The bootstrap waits along with it; once the GitHub rate-limit window
// resets the handler resumes and bootstrap proceeds. This may take
// hours without a token, minutes with one (5000 req/h).
func (o *Orchestrator) Handler() func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
	return func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
		var req SetupRequest
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return fmt.Errorf("decode payload: %w", err)
		}

		const totalPhases = 3

		// ---- Phase 1: data collection ----
		progress(1, totalPhases, fmt.Sprintf("phase 1/%d: queuing data collection (%d repos)", totalPhases, len(req.Repos)), "")
		collectIDs := make([]int64, 0, len(req.Repos))
		for _, slug := range req.Repos {
			j, err := o.db.EnqueueBGJob(ctx, store.JobKindCollectHistory, map[string]any{
				"repo":         slug,
				"months":       req.HistoryMonths,
				"github_token": req.GithubToken,
			})
			if err != nil {
				return fmt.Errorf("enqueue collect for %s: %w", slug, err)
			}
			collectIDs = append(collectIDs, j.ID)
		}
		if err := o.waitForJobs(ctx, collectIDs, func(done, total int) {
			progress(1, totalPhases, fmt.Sprintf("phase 1/%d: data collection %d/%d repos done", totalPhases, done, total), "")
		}); err != nil {
			return err
		}

		// ---- Phase 2: feature extraction ----
		progress(2, totalPhases, fmt.Sprintf("phase 2/%d: queuing feature extraction", totalPhases), "")
		featuresJob, err := o.db.EnqueueBGJob(ctx, store.JobKindComputeFeatures, map[string]any{"scope": "all"})
		if err != nil {
			return fmt.Errorf("enqueue compute_features: %w", err)
		}
		if err := o.waitForJobs(ctx, []int64{featuresJob.ID}, func(done, total int) {
			progress(2, totalPhases, fmt.Sprintf("phase 2/%d: feature extraction %d/%d", totalPhases, done, total), "")
		}); err != nil {
			return err
		}

		// ---- Phase 3: model training ----
		progress(3, totalPhases, fmt.Sprintf("phase 3/%d: queuing %d model training run(s)", totalPhases, len(req.Models)), "")
		trainIDs := make([]int64, 0, len(req.Models))
		for _, m := range req.Models {
			j, err := o.db.EnqueueBGJob(ctx, store.JobKindTrainModel, map[string]any{
				"algo":     m,
				"activate": false,
				"name":     fmt.Sprintf("bootstrap-%s", m),
			})
			if err != nil {
				return fmt.Errorf("enqueue train %s: %w", m, err)
			}
			trainIDs = append(trainIDs, j.ID)
		}
		// We *don't* short-circuit on individual training failures: some
		// algos (notably MLP) may not converge on tiny datasets, and the
		// bootstrap is a "best effort" — surviving models still land in
		// the registry. Any individual failure is visible on /experiments.
		_ = o.waitForJobs(ctx, trainIDs, func(done, total int) {
			progress(3, totalPhases, fmt.Sprintf("phase 3/%d: training %d/%d models done", totalPhases, done, total), "")
		})

		progress(totalPhases, totalPhases, "bootstrap complete", "")
		return nil
	}
}

// waitForJobs polls the bg_jobs table until every id in `ids` reaches a
// terminal state (done | failed | cancelled).
//
// Polling cadence: 1s — fast enough that the user sees fresh counts on
// the /setup UI without hammering Postgres.
//
// The progress callback runs on every poll cycle (even when counts
// haven't changed) so the bootstrap row's `message` keeps a fresh
// timestamp via the UpdateBGJobProgress row write — useful for spotting
// a stuck orchestrator.
func (o *Orchestrator) waitForJobs(ctx context.Context, ids []int64, onProgress func(done, total int)) error {
	if len(ids) == 0 {
		return nil
	}
	const poll = time.Second
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		done, total, err := o.countTerminal(ctx, ids)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			log.Warn().Err(err).Msg("bootstrap waitForJobs: count failed")
		}
		onProgress(done, total)
		if done >= total {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (o *Orchestrator) countTerminal(ctx context.Context, ids []int64) (done, total int, err error) {
	row := o.db.Pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status IN ('done', 'failed', 'cancelled')) AS done,
		  COUNT(*) AS total
		FROM bg_jobs
		WHERE id = ANY($1)
	`, ids)
	err = row.Scan(&done, &total)
	return
}
