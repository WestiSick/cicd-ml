// Package bgjobs is the in-process background-job runner.
//
// Design (revised after we hit head-of-line blocking in production-ish
// usage):
//
//   - SKIP LOCKED in store.ClaimNextBGJob makes multiple worker goroutines
//     safe to run in parallel.
//
//   - We split the worker pool into two pools by kind class:
//
//     io-bound   : collect_history, refresh        (1 worker)
//     compute    : bootstrap, compute_features,
//     train_model, simulate           (3 workers)
//
//     The split eliminates head-of-line blocking — a rate-limited
//     collect_history holding its goroutine for 60 min no longer blocks
//     the training request the user just submitted. Compute jobs are
//     fast (sub-second to a few seconds), so 3 workers is plenty.
//
// Each pool periodically polls bg_jobs filtered by its kind class. The
// poll interval is short (200ms) so the UI sees a freshly-queued job
// pick up almost immediately.
package bgjobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ws"
)

// Handler is the contract a kind-specific worker function fulfils.
//
// `progress` is a closure the handler uses to report status. The runner
// persists each call to `bg_jobs` and broadcasts it on /ws/bg-jobs.
type Handler func(ctx context.Context, job store.BGJob, progress ProgressFn) error

// ProgressFn — handlers call this to record progress. Empty strings on
// message/logsTail are interpreted as "no change" (matches the SQL update
// in store.UpdateBGJobProgress).
type ProgressFn func(progress, total int, message, logsTail string)

// Pool defines one logical worker group: it claims only kinds in its set
// and spawns N goroutines. Two pools share the same Runner, db and hub.
type Pool struct {
	Name        string
	Kinds       []string // empty = any kind (used by tests)
	Concurrency int
	PollEvery   time.Duration
}

// Default pool layout — tuned for the workload mix we actually see.
// Any new long-blocking kind (e.g. a future "refresh-all-repos" mass-sync)
// should join the io-bound pool. CPU-bound kinds go to "compute".
var DefaultPools = []Pool{
	{
		Name:        "io",
		Kinds:       []string{store.JobKindCollectHistory, store.JobKindRefresh},
		Concurrency: 1, // GitHub API rate-limit makes parallel ingest counter-productive
		PollEvery:   500 * time.Millisecond,
	},
	{
		Name: "compute",
		Kinds: []string{
			store.JobKindBootstrap,
			store.JobKindComputeFeatures,
			store.JobKindTrainModel,
			store.JobKindSimulate,
		},
		Concurrency: 3,
		PollEvery:   200 * time.Millisecond,
	},
}

// Broadcaster pushes bg_job updates onto a notification channel. The
// api-gateway binary supplies an adapter around its in-process *ws.Hub
// (`HubBroadcaster`). The standalone collector/simulator binaries supply
// an HTTP broadcaster (`HTTPBroadcaster`) that POSTs to api-gateway's
// /api/internal/broadcast endpoint, which re-publishes on the gateway's
// hub. The Runner doesn't care which is in use — the interface lets us
// swap delivery without touching the worker loop.
type Broadcaster interface {
	Publish(ctx context.Context, channel, eventType string, payload any)
}

// HubBroadcaster wraps *ws.Hub for in-process use. Same package so the
// import graph stays flat — the gateway already depends on ws.
type HubBroadcaster struct {
	Hub *ws.Hub
}

func (h HubBroadcaster) Publish(_ context.Context, channel, eventType string, payload any) {
	if h.Hub == nil {
		return
	}
	h.Hub.PublishJSON(channel, eventType, payload)
}

// NoopBroadcaster is for tests and the very early cold boot when the
// hub isn't wired yet.
type NoopBroadcaster struct{}

func (NoopBroadcaster) Publish(context.Context, string, string, any) {}

type Runner struct {
	db          *store.DB
	broadcaster Broadcaster
	handlers    map[string]Handler

	pools []Pool
}

// NewRunner — preserve the old (db, hub) signature for the gateway, which
// still passes its in-process hub. Standalone workers use
// NewRunnerWithBroadcaster instead.
func NewRunner(db *store.DB, hub *ws.Hub) *Runner {
	return NewRunnerWithBroadcaster(db, HubBroadcaster{Hub: hub})
}

func NewRunnerWithBroadcaster(db *store.DB, b Broadcaster) *Runner {
	if b == nil {
		b = NoopBroadcaster{}
	}
	return &Runner{
		db:          db,
		broadcaster: b,
		handlers:    map[string]Handler{},
		pools:       DefaultPools,
	}
}

// WithPools overrides the default pool layout (useful for tests).
func (r *Runner) WithPools(pools []Pool) *Runner {
	r.pools = pools
	return r
}

// RestrictKinds narrows every pool's claim set to the intersection of
// its original Kinds and `allowed`. Used by the api-gateway when the
// collector/simulator are deployed as separate processes — the gateway
// then refuses to claim collect_history/refresh/simulate so the
// dedicated workers always win the SKIP LOCKED race.
//
// If allowed is empty, every pool is left as-is (single-binary mode).
//
// Pools whose entire Kinds list is filtered out are DROPPED — not kept
// with an empty Kinds slice. The reason: `ClaimNextBGJobByKinds(ctx, [])`
// treats nil/empty as "no filter" and claims any kind. Keeping an
// empty-Kinds pool around would result in workers stealing jobs they
// don't have handlers for (collector grabbing train_model, etc.).
// This was the bug fixed alongside the prod deployment.
func (r *Runner) RestrictKinds(allowed map[string]bool) *Runner {
	if len(allowed) == 0 {
		return r
	}
	newPools := make([]Pool, 0, len(r.pools))
	for _, p := range r.pools {
		filtered := make([]string, 0, len(p.Kinds))
		for _, k := range p.Kinds {
			if allowed[k] {
				filtered = append(filtered, k)
			}
		}
		if len(filtered) == 0 {
			// Pool is completely filtered out — don't start workers for it.
			// This is what prevents "no handler registered" warnings in the
			// standalone collector / simulator binaries.
			continue
		}
		p.Kinds = filtered
		newPools = append(newPools, p)
	}
	r.pools = newPools
	return r
}

func (r *Runner) Register(kind string, h Handler) {
	r.handlers[kind] = h
}

// Run blocks until ctx is cancelled. Spawns every pool's worker goroutines
// in the background and returns when all workers have exited.
func (r *Runner) Run(ctx context.Context) {
	log.Info().Int("pools", len(r.pools)).Msg("bg-jobs runner starting")
	var wg sync.WaitGroup
	for _, p := range r.pools {
		for i := 0; i < p.Concurrency; i++ {
			wg.Add(1)
			workerID := fmt.Sprintf("%s-%d", p.Name, i)
			go func(p Pool, id string) {
				defer wg.Done()
				r.workerLoop(ctx, p, id)
			}(p, workerID)
		}
	}
	wg.Wait()
	log.Info().Msg("bg-jobs runner stopped")
}

func (r *Runner) workerLoop(ctx context.Context, p Pool, id string) {
	t := time.NewTicker(p.PollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.dispatchOnce(ctx, p, id)
		}
	}
}

func (r *Runner) dispatchOnce(ctx context.Context, p Pool, workerID string) {
	// Defense-in-depth — a pool with empty Kinds means "this pool was
	// fully filtered out by RestrictKinds and should not claim anything".
	// We re-check here because ClaimNextBGJobByKinds([]) would otherwise
	// fall back to claim-any behaviour and steal jobs we have no handler
	// for. RestrictKinds already drops such pools entirely, so this is
	// belt-and-braces.
	if len(p.Kinds) == 0 {
		return
	}
	// Operator pause-flag honoured cooperatively. We check once per
	// poll tick — when paused, the worker just idles without claiming.
	// In-flight jobs continue running; only new claims are suppressed.
	if paused, _ := r.db.GetBGJobsPaused(ctx); paused {
		return
	}
	job, err := r.db.ClaimNextBGJobByKinds(ctx, p.Kinds)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		log.Warn().Err(err).Str("worker", workerID).Msg("claim bg job")
		return
	}

	handler, ok := r.handlers[job.Kind]
	if !ok {
		msg := fmt.Sprintf("no handler registered for kind=%s", job.Kind)
		log.Warn().Str("kind", job.Kind).Int64("id", job.ID).Msg(msg)
		_ = r.db.MarkBGJobFailed(ctx, job.ID, msg)
		r.broadcast(ctx, job.ID)
		return
	}

	log.Info().Str("kind", job.Kind).Int64("id", job.ID).Str("worker", workerID).Msg("bg job started")
	r.broadcast(ctx, job.ID)

	// Cancel-watcher: polls bg_jobs.status for an external Cancel request
	// (set via POST /api/bg-jobs/{id}/cancel). When detected, cancels the
	// handler's context; the handler bails out at the next ctx-aware op.
	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	cancelled := make(chan struct{})
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				fresh, err := r.db.GetBGJob(jobCtx, job.ID)
				if err != nil {
					continue
				}
				if fresh.Status == store.JobStatusCancelled {
					close(cancelled)
					cancelJob()
					return
				}
			}
		}
	}()

	progress := func(prog, total int, message, logsTail string) {
		if err := r.db.UpdateBGJobProgress(jobCtx, job.ID, prog, total, message, logsTail); err != nil {
			log.Warn().Err(err).Msg("update bg job progress")
		}
		r.broadcast(jobCtx, job.ID)
	}

	if err := handler(jobCtx, job, progress); err != nil {
		// External cancellation (cancel-watcher fired): leave the
		// "cancelled" status the watcher set, just broadcast.
		select {
		case <-cancelled:
			log.Info().Int64("id", job.ID).Msg("bg job cancelled (user request)")
			r.broadcast(ctx, job.ID)
			return
		default:
		}
		// ctx cancellation from outer shutdown.
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			log.Info().Int64("id", job.ID).Msg("bg job cancelled (ctx done)")
			_ = r.db.MarkBGJobFailed(ctx, job.ID, "cancelled — shutting down")
			r.broadcast(ctx, job.ID)
			return
		}
		log.Warn().Err(err).Int64("id", job.ID).Msg("bg job failed")
		_ = r.db.MarkBGJobFailed(ctx, job.ID, err.Error())
		r.broadcast(ctx, job.ID)
		return
	}

	_ = r.db.MarkBGJobDone(ctx, job.ID, "")
	r.broadcast(ctx, job.ID)
	log.Info().Int64("id", job.ID).Str("worker", workerID).Msg("bg job done")
}

func (r *Runner) broadcast(ctx context.Context, id int64) {
	updated, err := r.db.GetBGJob(ctx, id)
	if err != nil {
		return
	}
	r.broadcaster.Publish(ctx, "bg-jobs", "bg_job.updated", updated)
}
