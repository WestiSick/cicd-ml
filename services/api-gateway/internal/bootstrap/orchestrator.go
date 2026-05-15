// Package bootstrap drives the first-run setup flow.
//
// Triggered by POST /api/setup/start with a payload selecting which seed
// repos to ingest and which models to pre-train. The orchestrator creates
// the `repos` rows and chains the `bg_jobs` queue:
//
//	collect_history (per repo)   ──►   compute_features   ──►   train_model (per algo)   ──►   simulate
//
// Each step is its own bg_jobs row so the UI shows accurate per-phase
// progress without bespoke endpoints. The handler for `bootstrap` writes
// the chained jobs and immediately reports done — the work happens in
// the dependent jobs picked up by the runner afterwards.
package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"

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

	// Enqueue the bootstrap chain. The handler in handlers.go expands this
	// into per-repo collect_history rows + downstream jobs.
	job, err := o.db.EnqueueBGJob(ctx, store.JobKindBootstrap, req)
	if err != nil {
		return 0, fmt.Errorf("enqueue bootstrap: %w", err)
	}
	log.Info().Int64("id", job.ID).Int("repos", len(req.Repos)).Int("models", len(req.Models)).Msg("setup queued")
	return job.ID, nil
}

// Handler returns the bgjobs.Handler bound to this orchestrator.
//
// The bootstrap handler is intentionally fast — it just chains downstream
// jobs. We mark progress in coarse phases so the user sees something
// happen immediately:
//
//	phase 1/4: registering repos      (already done by Start)
//	phase 2/4: queuing data collection
//	phase 3/4: queuing feature build
//	phase 4/4: queuing model training
func (o *Orchestrator) Handler() func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
	return func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
		var req SetupRequest
		if err := json.Unmarshal(job.Payload, &req); err != nil {
			return fmt.Errorf("decode payload: %w", err)
		}

		total := 4
		progress(1, total, "registering seed repositories", "")
		// repos are already added in Start — included here for the user-visible step.

		progress(2, total, "queuing data collection", "")
		for _, slug := range req.Repos {
			if _, err := o.db.EnqueueBGJob(ctx, store.JobKindCollectHistory, map[string]any{
				"repo":  slug,
				"months": req.HistoryMonths,
				"github_token": req.GithubToken,
			}); err != nil {
				return fmt.Errorf("enqueue collect for %s: %w", slug, err)
			}
		}

		progress(3, total, "queuing feature extraction", "")
		if _, err := o.db.EnqueueBGJob(ctx, store.JobKindComputeFeatures, map[string]any{
			"scope": "all",
		}); err != nil {
			return fmt.Errorf("enqueue compute_features: %w", err)
		}

		progress(4, total, "queuing model training", "")
		for _, m := range req.Models {
			if _, err := o.db.EnqueueBGJob(ctx, store.JobKindTrainModel, map[string]any{
				"algo": m,
			}); err != nil {
				return fmt.Errorf("enqueue train %s: %w", m, err)
			}
		}

		progress(total, total, "setup chain queued — workers will continue in the background", "")
		// Note: bootstrap_done is flipped only when ALL chained jobs finish
		// successfully. That logic lives in a separate watcher (see
		// finishOnDone in handlers.go) — keeps the orchestrator focused.
		return nil
	}
}
