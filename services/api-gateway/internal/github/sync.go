package github

import (
	"context"
	"errors"
	"fmt"
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
type Syncer struct {
	DB     *store.DB
	Client *Client
}

type RunInput struct {
	RepoOwner string
	RepoName  string
	Months    int // 3, 6 or 12 — drives the cutoff
}

// Progress is the callback the syncer uses to report progress. Matches the
// bgjobs.ProgressFn signature on purpose, so the binding in main.go is a
// straight forward.
type Progress func(progress, total int, message, logsTail string)

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

	progress(0, 1, fmt.Sprintf("fetching repo metadata: %s/%s", owner, name), "")
	meta, rate, err := s.Client.GetRepo(ctx, owner, name)
	if err := s.handleRateLimit(ctx, err, rate, progress); err != nil {
		return err
	}
	_ = meta // could persist default_branch — left for a later iteration.

	cutoff := time.Now().AddDate(0, -in.Months, 0).UTC()
	createdFilter := ">=" + cutoff.Format("2006-01-02")

	// Phase 1: pages of runs (newest first).
	totalCounted := 0
	page := 1
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
			progress(0, runs.TotalCount, fmt.Sprintf("found %d runs in last %d months", runs.TotalCount, in.Months), "")
		}

		for _, r := range runs.WorkflowRuns {
			if r.CreatedAt.Before(cutoff) {
				// Older than window — done with this repo.
				_ = s.DB.UpdateRepoSyncCounters(ctx, repo.ID)
				progress(totalCounted, totalCounted, "sync complete (older than window)", "")
				return nil
			}

			runDBID, err := s.DB.UpsertWorkflowRun(ctx, repo.ID, store.UpsertWorkflowRunParams{
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
			// (matrix builds → many workflow_runs share head_sha). Errors
			// are non-fatal: feature engineering tolerates NULLs in the
			// commit fields.
			if r.HeadSHA != "" {
				if exists, _ := s.DB.CommitExists(ctx, r.HeadSHA); !exists {
					c, rl3, cerr := s.Client.GetCommit(ctx, owner, name, r.HeadSHA)
					if rlErr := s.handleRateLimit(ctx, cerr, rl3, progress); rlErr != nil {
						return rlErr
					}
					if cerr == nil {
						committedAt := c.CommitDetail.Author.Date
						_ = s.DB.UpsertCommit(ctx, store.UpsertCommitParams{
							SHA:          c.SHA,
							RepoID:       repo.ID,
							Author:       c.Author.Login,
							Message:      truncateMessage(c.CommitDetail.Message, 280),
							FilesChanged: len(c.Files),
							Additions:    c.Stats.Additions,
							Deletions:    c.Stats.Deletions,
							CommittedAt:  zeroToNil(committedAt),
						})
					}
				}
			}

			// Only fetch jobs for runs that have completed — in-flight runs
			// don't yet have meaningful durations. This also cuts API calls.
			if r.Status == "completed" {
				jobs, rl2, jerr := s.Client.ListRunJobs(ctx, owner, name, r.ID)
				if err := s.handleRateLimit(ctx, jerr, rl2, progress); err != nil {
					return err
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

			totalCounted++
			if totalCounted%10 == 0 {
				progress(totalCounted, runs.TotalCount, fmt.Sprintf("%s/%s: %d runs synced (rate %d/%d remaining)", owner, name, totalCounted, rl.Remaining, rl.Limit), "")
			}
		}

		// Update counters every page so the UI sees the live totals.
		_ = s.DB.UpdateRepoSyncCounters(ctx, repo.ID)

		// End of pagination.
		if len(runs.WorkflowRuns) < 100 {
			break
		}
		page++
	}

	progress(totalCounted, totalCounted, fmt.Sprintf("%s/%s: sync complete", owner, name), "")
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
		progress(-1, -1, msg, "")
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
