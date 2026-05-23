// Package simrun is the shared simulator-execution code used by both the
// synchronous POST /api/simulator/run HTTP handler AND the standalone
// simulator worker binary (services/api-gateway/cmd/simulator).
//
// Why a separate package: the simulator handler used to live in
// internal/http/simulator.go with a private `runSimulation` function.
// When the simulator was extracted into its own container, that function
// needed to be reachable from cmd/simulator without importing the entire
// HTTP server. Moving the core into `simrun` is a thin refactor that
// keeps the layering: scheduler does math, store does I/O, simrun glues
// them, http and cmd/simulator are thin entry points.
package simrun

import (
	"context"
	"strings"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/scheduler"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// Params is the union of inputs the simulator needs — same shape as
// the HTTP body and the bg_job payload. NewParams (below) fills in
// defaults so callers can pass a partly-populated struct.
type Params struct {
	WindowStart   time.Time
	WindowEnd     time.Time
	RepoIDs       []int64
	Strategies    []string
	Runners       int
	SLAMainSec    int
	SLAFeatureSec int
	CustomWeights scheduler.CustomWeights
}

// NewParams seeds defaults for any zero-valued field. Mirrors what the
// HTTP handler used to do inline.
func NewParams(p Params) Params {
	if len(p.Strategies) == 0 {
		p.Strategies = scheduler.AvailableStrategies()
	}
	if p.Runners <= 0 {
		p.Runners = 1
	}
	if p.WindowEnd.IsZero() {
		p.WindowEnd = time.Now().UTC()
	}
	if p.WindowStart.IsZero() {
		p.WindowStart = p.WindowEnd.Add(-7 * 24 * time.Hour)
	}
	return p
}

// Result wraps the metrics returned by each strategy. ErrNoJobs is set
// to true (and Metrics is nil) when no completed jobs were found in the
// window — the caller can render a friendly empty-state.
type Result struct {
	Metrics []scheduler.Metrics
	JobsRun int
}

// ErrNoJobs is returned when the window contained no completed jobs.
// The HTTP handler maps this to 400 invalid_window; the worker logs it
// and finishes the bg_job with a clear message.
type ErrNoJobs struct{}

func (ErrNoJobs) Error() string { return "no completed jobs in window" }

// Run executes the simulation for every requested strategy, persisting
// each result into `sim_runs`. Returns the assembled Metrics slice for
// the caller to render or log.
func Run(ctx context.Context, db *store.DB, in Params) (Result, error) {
	p := NewParams(in)
	if !p.WindowEnd.After(p.WindowStart) {
		return Result{}, &InvalidWindowError{WindowStart: p.WindowStart, WindowEnd: p.WindowEnd}
	}

	rawJobs, err := db.LoadSimWindow(ctx, p.WindowStart, p.WindowEnd, p.RepoIDs)
	if err != nil {
		return Result{}, err
	}
	if len(rawJobs) == 0 {
		return Result{}, ErrNoJobs{}
	}

	jobs := projectToSim(rawJobs,
		time.Duration(p.SLAMainSec)*time.Second,
		time.Duration(p.SLAFeatureSec)*time.Second,
	)
	cfg := scheduler.SimulatorConfig{Runners: p.Runners}

	results := make([]scheduler.Metrics, 0, len(p.Strategies))
	for _, name := range p.Strategies {
		strat := scheduler.New(name, p.CustomWeights)
		if strat == nil {
			continue
		}
		m := scheduler.Run(strat, jobs, cfg)
		if _, err := db.InsertSimRun(ctx, store.InsertSimRunParams{
			Strategy:         m.Strategy,
			WindowStart:      m.WindowStart,
			WindowEnd:        m.WindowEnd,
			Repos:            p.RepoIDs,
			JobsCount:        m.JobsCount,
			MakespanSec:      m.MakespanSec,
			WaitP50Sec:       m.WaitP50Sec,
			WaitP95Sec:       m.WaitP95Sec,
			ThroughputPerMin: m.ThroughputPerMin,
			SLAViolations:    m.SLAViolations,
			Extra: map[string]any{
				"runners":         p.Runners,
				"sla_main_sec":    p.SLAMainSec,
				"sla_feature_sec": p.SLAFeatureSec,
				"wait_mean_sec":   m.WaitMeanSec,
			},
		}); err != nil {
			return Result{}, err
		}
		results = append(results, m)
	}
	return Result{Metrics: results, JobsRun: len(jobs)}, nil
}

// InvalidWindowError surfaces when the caller passed window_end <= window_start.
type InvalidWindowError struct {
	WindowStart, WindowEnd time.Time
}

func (e *InvalidWindowError) Error() string {
	return "window_end must be after window_start"
}

// projectToSim converts DB rows to scheduler input — pure compute, no
// I/O. Same logic as the original in internal/http/simulator.go; moved
// here so both binaries call it.
func projectToSim(rows []store.SimInputJob, slaMain, slaFeature time.Duration) []scheduler.SimulatorJob {
	out := make([]scheduler.SimulatorJob, 0, len(rows))
	for _, r := range rows {
		if r.ActualSec == nil {
			continue
		}
		actual := float64(*r.ActualSec)
		predicted := actual
		if r.PredictedSec != nil {
			predicted = *r.PredictedSec
		}
		var deadline time.Time
		if slaMain > 0 || slaFeature > 0 {
			budget := slaFeature
			if isMainLike(r.Branch) {
				budget = slaMain
			}
			if budget > 0 {
				deadline = r.ArrivedAt.Add(budget)
			}
		}
		out = append(out, scheduler.SimulatorJob{
			ID:           r.ID,
			Repo:         r.Repo,
			Branch:       r.Branch,
			ArrivedAt:    r.ArrivedAt,
			PredictedSec: predicted,
			ActualSec:    actual,
			Deadline:     deadline,
		})
	}
	return out
}

// isMainLike classifies the branch class for SLA-budget selection. Same
// rule the feature engineering uses so simulator and trained model see
// the same notion of "main" vs "feature".
func isMainLike(branch string) bool {
	b := strings.ToLower(branch)
	return b == "main" || b == "master" || strings.HasPrefix(b, "release")
}
