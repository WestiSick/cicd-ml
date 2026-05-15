package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// StubHandlers returns handlers for collect_history, compute_features and
// train_model that simulate the work in progress chunks. These let the UI
// stream realistic progress before the real implementations land — the
// frontend's WebSocket integration is tested against the same shape it
// will use in production.
//
// Each stub respects ctx cancellation so a `docker compose down` exits
// promptly instead of timing out.
func StubHandlers() (collect, features, train, simulate func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error) {
	collect = stubProgress("collecting GitHub Actions history", 20, 200*time.Millisecond)
	features = stubProgress("computing feature vectors", 10, 150*time.Millisecond)
	train = func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
		var p struct{ Algo string `json:"algo"` }
		_ = json.Unmarshal(job.Payload, &p)
		label := fmt.Sprintf("training %s", p.Algo)
		return stubProgress(label, 30, 120*time.Millisecond)(ctx, job, progress)
	}
	simulate = stubProgress("simulating scheduling strategies", 15, 100*time.Millisecond)
	return
}

func stubProgress(label string, steps int, stepDelay time.Duration) func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
	return func(ctx context.Context, job store.BGJob, progress func(int, int, string, string)) error {
		for i := 1; i <= steps; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(stepDelay):
			}
			msg := fmt.Sprintf("%s — step %d/%d", label, i, steps)
			progress(i, steps, msg, "")
		}
		return nil
	}
}

// FinishOnDone watches bg_jobs and flips bootstrap_done=true once there
// are no queued or running jobs of the relevant kinds. This is intentionally
// observational — it doesn't need to be transactional, just eventually
// consistent. Runs in its own goroutine.
func FinishOnDone(ctx context.Context, db *store.DB) {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			state, err := db.GetSystemState(ctx)
			if err != nil {
				continue
			}
			if state.BootstrapDone {
				// Nothing more to do.
				return
			}

			done, err := bootstrapPipelineEmpty(ctx, db)
			if err != nil {
				log.Warn().Err(err).Msg("bootstrap watcher: pipeline check")
				continue
			}
			if done {
				if err := db.SetBootstrapDone(ctx, true); err == nil {
					log.Info().Msg("bootstrap pipeline complete — bootstrap_done=true")
					_ = db.RecordActivity(ctx, "system", "bootstrap_complete", "", "initial setup finished", true, nil)
					return
				}
			}
		}
	}
}

// bootstrapPipelineEmpty returns true once at least one bootstrap chain
// has completed AND no bootstrap-related jobs are still queued/running.
// The "at least one done" guard prevents a fresh DB (no jobs yet) from
// accidentally flipping the gate.
func bootstrapPipelineEmpty(ctx context.Context, db *store.DB) (bool, error) {
	var pending int
	var doneCount int
	err := db.Pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status IN ('queued', 'running')),
		  COUNT(*) FILTER (WHERE status = 'done')
		FROM bg_jobs
		WHERE kind IN ('bootstrap', 'collect_history', 'compute_features', 'train_model', 'simulate')
	`).Scan(&pending, &doneCount)
	if err != nil {
		return false, err
	}
	return pending == 0 && doneCount > 0, nil
}
