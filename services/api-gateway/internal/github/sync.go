package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// Syncer ingests a repo's workflow_runs + jobs into Postgres.
//
// The handler in bootstrap.go calls Run(ctx, ...) for each
// `collect_history` bg_job and forwards progress to the UI.
//
// Design notes:
//   - We walk runs in descending created_at (GitHub's default), so the
//     newest data lands first — useful if the user cancels mid-sync.
//   - We stop once a page's runs are all older than the cutoff window
//     (BootstrapDefaultMonths). This avoids fetching ancient history
//     even on huge repos.
//   - Rate-limit-aware: on 403/429 we wait until reset, then resume.
//     The wait shows up in the UI as a progress message so the user
//     understands the pause.
//   - **Per-run work is parallel.** GetCommit + ListRunJobs are I/O-bound
//     calls to GitHub with ~150–300ms RTT each. Running them serially
//     produces a ~50ms-per-run wall-clock floor that dominates total
//     sync time even though the rate-limit budget (5000/h with a PAT)
//     is barely touched. A bounded worker pool (default 12) ingests a
//     page of 100 runs in ~5s instead of ~30s. The bound also keeps us
//     friendly to api.github.com — 12 concurrent requests is well under
//     GitHub's abuse-detection thresholds.
type Syncer struct {
	DB     *store.DB
	Client *Client

	// Concurrency caps the number of in-flight GetCommit + ListRunJobs
	// goroutines per page. Zero = default (read from env GITHUB_SYNC_CONCURRENCY,
	// falling back to 12). Override per-test via the field directly.
	Concurrency int
}

type RunInput struct {
	RepoOwner string
	RepoName  string
	Months    int // 3, 6 or 12 — drives the cutoff
}

// Progress is the callback the syncer uses to report progress. Matches the
// bgjobs.ProgressFn signature on purpose, so the binding in main.go is a
// straight forward.
//
// We piggyback structured progress data on the `logsTail` channel as a
// single-line JSON blob (see syncStats below). The frontend parses it
// to render the live progress strip on the /datasets card: ETA, rate
// limit countdown, jobs/sec, pages-of-N progress. Plain-text `message`
// stays human-readable for /admin → Activity log.
type Progress func(progress, total int, message, logsTail string)

// syncStats is the wire format for structured sync progress. Lives in
// bg_jobs.logs_tail as a one-line JSON document; the dataset card on
// /datasets reads it through the regular /api/bg-jobs poll and renders
// the bar / ETA / rate counter.
type syncStats struct {
	Phase         string  `json:"phase"`          // "fetching_meta" | "fetching_runs" | "rate_limited" | "done"
	Page          int     `json:"page,omitempty"` // current page of runs being fetched
	RunsSeen      int     `json:"runs_seen,omitempty"`
	RunsTotal     int     `json:"runs_total,omitempty"`
	JobsPerSec    float64 `json:"jobs_per_sec,omitempty"`
	EtaSeconds    float64 `json:"eta_seconds,omitempty"`
	RateRemaining int     `json:"rate_remaining,omitempty"`
	RateLimit     int     `json:"rate_limit,omitempty"`
	RateResetUnix int64   `json:"rate_reset_unix,omitempty"`
}

func (s syncStats) Encode() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// concurrency resolves the worker-pool size. Env-var read once per Run
// to avoid surprise mid-flight changes.
func (s *Syncer) concurrency() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	if v := os.Getenv("GITHUB_SYNC_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 64 {
			return n
		}
	}
	return 12
}

// Run performs a full sync for one repository.
func (s *Syncer) Run(ctx context.Context, in RunInput, progress Progress) error {
	owner, name := in.RepoOwner, in.RepoName

	repo, err := s.DB.LookupRepo(ctx, owner, name)
	if err != nil {
		return fmt.Errorf("repo not registered: %w", err)
	}

	_ = s.DB.SetRepoStatus(ctx, repo.ID, "fetching", "")
	defer func() {
		// On clean exit, mark synced; on error the caller's defer in the
		// bg_jobs runner records the failure separately, so we just need
		// to clear the "fetching" chip.
		if ctx.Err() == nil {
			_ = s.DB.SetRepoStatus(ctx, repo.ID, "synced", "")
		}
	}()

	progress(0, 1, fmt.Sprintf("fetching repo metadata: %s/%s", owner, name),
		syncStats{Phase: "fetching_meta"}.Encode())
	meta, rate, err := s.Client.GetRepo(ctx, owner, name)
	if err := s.handleRateLimit(ctx, err, rate, progress); err != nil {
		return err
	}
	_ = meta // could persist default_branch — left for a later iteration.

	cutoff := time.Now().AddDate(0, -in.Months, 0).UTC()
	createdFilter := ">=" + cutoff.Format("2006-01-02")
	workers := s.concurrency()

	// progressGuard serialises the progress callback so 12 parallel
	// workers don't garble the JSON-encoded stats payload on the wire.
	var progressGuard sync.Mutex
	emit := func(seen, total int, msg, tail string) {
		progressGuard.Lock()
		defer progressGuard.Unlock()
		progress(seen, total, msg, tail)
	}

	// Phase 1: pages of runs (newest first).
	totalCounted := 0
	page := 1
	startedAt := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		runs, rl, err := s.Client.ListWorkflowRuns(ctx, owner, name, page, createdFilter)
		if err := s.handleRateLimit(ctx, err, rl, progress); err != nil {
			return err
		}

		if page == 1 {
			// First page tells us the total — cap progress denominator.
			emit(0, runs.TotalCount, fmt.Sprintf("found %d runs in last %d months", runs.TotalCount, in.Months), "")
		}

		// Find the cutoff index within this page. Anything past it is
		// older than our window — we'll process the prefix in parallel
		// then bail out cleanly.
		cutoffIdx := len(runs.WorkflowRuns)
		for i, r := range runs.WorkflowRuns {
			if r.CreatedAt.Before(cutoff) {
				cutoffIdx = i
				break
			}
		}
		toProcess := runs.WorkflowRuns[:cutoffIdx]
		hitCutoff := cutoffIdx < len(runs.WorkflowRuns)

		// Worker pool — each goroutine handles one run end-to-end:
		// UpsertWorkflowRun, optional GetCommit + UpsertCommit, optional
		// ListRunJobs + UpsertJob loop. errCh collects fatal errors
		// (anything that's not a rate-limit, which we retry inline);
		// the first non-nil cancels the rest via shared ctx.
		runCtx, cancelRuns := context.WithCancel(ctx)
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		errCh := make(chan error, len(toProcess))
		var counterMu sync.Mutex

		for _, r := range toProcess {
			if err := runCtx.Err(); err != nil {
				break
			}
			select {
			case sem <- struct{}{}:
			case <-runCtx.Done():
				break
			}
			wg.Add(1)
			go func(r WorkflowRun) {
				defer wg.Done()
				defer func() { <-sem }()

				if err := s.processRun(runCtx, repo.ID, owner, name, r, emit); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancelRuns()
					return
				}

				counterMu.Lock()
				totalCounted++
				done := totalCounted
				counterMu.Unlock()

				if done%10 == 0 {
					elapsed := time.Since(startedAt).Seconds()
					perSec := 0.0
					if elapsed > 0 {
						perSec = float64(done) / elapsed
					}
					eta := 0.0
					if perSec > 0 && runs.TotalCount > done {
						eta = float64(runs.TotalCount-done) / perSec
					}
					stats := syncStats{
						Phase:         "fetching_runs",
						Page:          page,
						RunsSeen:      done,
						RunsTotal:     runs.TotalCount,
						JobsPerSec:    perSec,
						EtaSeconds:    eta,
						RateRemaining: rl.Remaining,
						RateLimit:     rl.Limit,
						RateResetUnix: rl.ResetAt.Unix(),
					}
					emit(done, runs.TotalCount,
						fmt.Sprintf("%s/%s: %d/%d runs (page %d, %d/%d rate, ~%.1f r/s)",
							owner, name, done, runs.TotalCount, page, rl.Remaining, rl.Limit, perSec),
						stats.Encode())
				}
			}(r)
		}
		wg.Wait()
		cancelRuns()
		close(errCh)

		// Surface the first fatal error from any worker.
		for werr := range errCh {
			if werr != nil {
				return werr
			}
		}

		// Update counters every page so the UI sees the live totals.
		_ = s.DB.UpdateRepoSyncCounters(ctx, repo.ID)

		if hitCutoff {
			emit(totalCounted, totalCounted, "sync complete (older than window)",
				syncStats{Phase: "done", RunsSeen: totalCounted, RunsTotal: totalCounted}.Encode())
			return nil
		}

		// End of pagination.
		if len(runs.WorkflowRuns) < 100 {
			break
		}
		page++
	}

	emit(totalCounted, totalCounted, fmt.Sprintf("%s/%s: sync complete", owner, name),
		syncStats{Phase: "done", RunsSeen: totalCounted, RunsTotal: totalCounted}.Encode())
	return nil
}

// processRun handles one workflow_run end-to-end. Safe to call from a
// goroutine pool because every dependency (DB, Client, progress) is
// already concurrent-safe (pgxpool, &http.Client, mutexed callback).
//
// Returns a fatal error only for things the caller can't recover from
// (DB write failure, context cancellation). GetCommit / ListRunJobs
// errors that aren't rate-limit are logged and skipped — feature
// engineering tolerates missing commit-fields and jobs are independent
// per run, so a single bad fetch shouldn't poison the whole sync.
func (s *Syncer) processRun(ctx context.Context, repoID int64, owner, name string, r WorkflowRun, progress Progress) error {
	runDBID, err := s.DB.UpsertWorkflowRun(ctx, repoID, store.UpsertWorkflowRunParams{
		RunID:        r.ID,
		WorkflowName: r.Name,
		HeadBranch:   r.HeadBranch,
		HeadSHA:      r.HeadSHA,
		Event:        r.Event,
		Status:       r.Status,
		Conclusion:   r.Conclusion,
		Actor:        r.Actor.Login,
		CreatedAt:    r.CreatedAt,
		RunStartedAt: r.RunStartedAt,
		UpdatedAt:    r.UpdatedAt,
	})
	if err != nil {
		return fmt.Errorf("upsert run %d: %w", r.ID, err)
	}

	// Commit details — cheap to skip if we already have this SHA
	// (matrix builds → many workflow_runs share head_sha). Errors are
	// non-fatal: feature engineering tolerates NULLs in the commit fields.
	if r.HeadSHA != "" {
		// CommitFullyCached (not CommitExists) so SHAs whose `commits` row
		// exists but whose `commit_files` were never populated (pre-Task-C
		// data) get re-fetched once and enriched. Otherwise Re-sync from
		// scratch would never backfill commit-content features for
		// historical jobs — the model would keep training on zeros.
		if cached, _ := s.DB.CommitFullyCached(ctx, r.HeadSHA); !cached {
			c, rl, cerr := s.Client.GetCommit(ctx, owner, name, r.HeadSHA)
			if rlErr := s.handleRateLimit(ctx, cerr, rl, progress); rlErr != nil {
				return rlErr
			}
			if cerr == nil {
				committedAt := c.CommitDetail.Author.Date
				if err := s.DB.UpsertCommit(ctx, store.UpsertCommitParams{
					SHA:          c.SHA,
					RepoID:       repoID,
					Author:       c.Author.Login,
					Message:      truncateMessage(c.CommitDetail.Message, 280),
					FilesChanged: len(c.Files),
					Additions:    c.Stats.Additions,
					Deletions:    c.Stats.Deletions,
					CommittedAt:  zeroToNil(committedAt),
				}); err == nil && len(c.Files) > 0 {
					// Persist the per-file diff for commit-content features.
					// GitHub caps the Files array at 300 per commit — anything
					// larger comes back truncated. That's fine: super-large
					// commits are outliers we don't model accurately anyway.
					rows := make([]store.CommitFileParams, 0, len(c.Files))
					for _, f := range c.Files {
						rows = append(rows, store.CommitFileParams{
							Filename:  f.Filename,
							Status:    f.Status,
							Additions: f.Additions,
							Deletions: f.Deletions,
						})
					}
					if fErr := s.DB.BulkInsertCommitFiles(ctx, c.SHA, rows); fErr != nil {
						log.Warn().Err(fErr).Str("sha", c.SHA).
							Int("files", len(rows)).
							Msg("commit_files insert failed; non-fatal")
					}
				}
			}
		}
	}

	// Only fetch jobs for runs that have completed — in-flight runs
	// don't yet have meaningful durations. This also cuts API calls.
	if r.Status == "completed" {
		jobs, rl2, jerr := s.Client.ListRunJobs(ctx, owner, name, r.ID)
		if rlErr := s.handleRateLimit(ctx, jerr, rl2, progress); rlErr != nil {
			return rlErr
		}
		if jerr != nil {
			// Non-rate-limit error — log but don't fail the whole sync.
			log.Warn().Err(jerr).Int64("run_id", r.ID).Msg("list jobs failed; skipping")
			return nil
		}
		for _, j := range jobs {
			var dur *int
			if j.StartedAt != nil && j.CompletedAt != nil {
				d := int(j.CompletedAt.Sub(*j.StartedAt).Seconds())
				if d >= 0 {
					dur = &d
				}
			}
			stepsCount := len(j.Steps)
			if _, err := s.DB.UpsertJob(ctx, runDBID, store.UpsertJobParams{
				GithubJobID: j.ID,
				Name:        j.Name,
				Status:      j.Status,
				Conclusion:  j.Conclusion,
				StartedAt:   j.StartedAt,
				CompletedAt: j.CompletedAt,
				DurationSec: dur,
				RunnerName:  j.RunnerName,
				RunnerGroup: j.RunnerGroup,
				Labels:      j.Labels,
				StepsCount:  &stepsCount,
			}); err != nil {
				return fmt.Errorf("upsert job %d: %w", j.ID, err)
			}
		}
	}
	return nil
}

// truncateMessage caps the commit message so the row stays slim. We
// keep the first line plus a trailing ellipsis, since the predictor
// uses counts not message text. 280 chars matches a Twitter-era
// rule-of-thumb for "summary length" — enough for diagnostics.
func truncateMessage(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// zeroToNil maps a zero time.Time to nil so the DB stores NULL instead
// of the unix epoch. The pgx layer's UpsertCommit already does NULL for
// nil; this saves callers a four-line if/else at every call site.
func zeroToNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// handleRateLimit centralises the 403/429 retry policy. If the response
// was a true rate-limit, we sleep until reset and signal the UI; any
// other error we surface as-is.
//
// Concurrent-safe: 12 workers can all be in handleRateLimit at the same
// time (likely when the API limit just hit). Each will independently
// wait until reset — slightly wasteful (12 sleeping goroutines vs 1
// with a shared barrier) but correct. The progress callback is mutexed
// by the caller's `emit` wrapper so the messages don't garble.
func (s *Syncer) handleRateLimit(ctx context.Context, err error, rl RateLimit, progress Progress) error {
	if err == nil {
		return nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.IsRateLimited() {
		wait := time.Until(apiErr.Rate.ResetAt)
		if wait < 0 {
			wait = time.Minute
		}
		msg := fmt.Sprintf("rate limited — resuming at %s (~%.0f min)", apiErr.Rate.ResetAt.Format("15:04"), wait.Minutes())
		log.Warn().Dur("wait", wait).Msg(msg)
		progress(-1, -1, msg, syncStats{
			Phase:         "rate_limited",
			RateRemaining: apiErr.Rate.Remaining,
			RateLimit:     apiErr.Rate.Limit,
			RateResetUnix: apiErr.Rate.ResetAt.Unix(),
		}.Encode())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait + 5*time.Second):
		}
		return nil
	}
	_ = rl
	return err
}
