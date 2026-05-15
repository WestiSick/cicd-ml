// Package scheduler implements the queue-ordering strategies the thesis
// compares: FIFO, SJF, EDF and a configurable Custom weighted scheme.
//
// The Strategy interface is intentionally narrow:
//
//	NextJob(ready []QueuedJob, now time.Time) int
//
// It returns the index of the job that should run next from the given
// ready list. The simulator (sim.go) feeds it the current ready-queue at
// each "tick" — strategies are pure functions of state and clock; they
// hold no internal mutable state, which makes them trivially testable.
//
// Naming choices follow the dissertation: SJF = Shortest Job First, EDF =
// Earliest Deadline First. Custom uses a weighted score with three
// dimensions (short-job, deadline-proximity, branch-importance) that the
// user can tune from /admin.
package scheduler

import (
	"strings"
	"time"
)

// QueuedJob is the per-job input to a Strategy. The simulator builds these
// from rows of `jobs ⨝ workflow_runs ⨝ predictions`. Fields not relevant
// to a given strategy are simply ignored (e.g. FIFO uses only ArrivedAt).
type QueuedJob struct {
	ID            int64     // jobs.id
	Repo          string    // owner/name (for diagnostics)
	Branch        string    // head_branch (for branch-importance scoring)
	ArrivedAt     time.Time // workflow_runs.created_at
	PredictedSec  float64   // best available duration estimate (oracle = actual, ML = predicted)
	Deadline      time.Time // ArrivedAt + SLA(Branch); zero if no deadline
}

// Strategy chooses the next job to run from a non-empty ready slice.
// Index returned must be in [0, len(ready)). Pure function — no mutation.
type Strategy interface {
	Name() string
	NextJob(ready []QueuedJob, now time.Time) int
}

// ---- FIFO -------------------------------------------------------------

type FIFO struct{}

func (FIFO) Name() string { return "fifo" }

// FIFO picks the job that arrived first — i.e. minimum ArrivedAt. Ties are
// broken by ID for determinism (otherwise re-running a sim could shuffle
// the order, which would make thesis charts subtly non-reproducible).
func (FIFO) NextJob(ready []QueuedJob, _ time.Time) int {
	best := 0
	for i := 1; i < len(ready); i++ {
		if ready[i].ArrivedAt.Before(ready[best].ArrivedAt) ||
			(ready[i].ArrivedAt.Equal(ready[best].ArrivedAt) && ready[i].ID < ready[best].ID) {
			best = i
		}
	}
	return best
}

// ---- SJF --------------------------------------------------------------

type SJF struct{}

func (SJF) Name() string { return "sjf" }

// SJF picks the smallest PredictedSec. Behaves identically to oracle-SJF
// when PredictedSec == actual duration; the gap between the two is the
// ML-induced cost of a real-world deployment — exactly what the thesis
// quantifies.
func (SJF) NextJob(ready []QueuedJob, _ time.Time) int {
	best := 0
	for i := 1; i < len(ready); i++ {
		if ready[i].PredictedSec < ready[best].PredictedSec ||
			(ready[i].PredictedSec == ready[best].PredictedSec && ready[i].ID < ready[best].ID) {
			best = i
		}
	}
	return best
}

// ---- EDF --------------------------------------------------------------

type EDF struct{}

func (EDF) Name() string { return "edf" }

// EDF picks the job with the earliest Deadline. Jobs without a deadline
// (Deadline.IsZero()) get pushed to the tail — a safety so unmetered
// background work doesn't starve out time-sensitive PR pipelines.
func (EDF) NextJob(ready []QueuedJob, _ time.Time) int {
	best := 0
	for i := 1; i < len(ready); i++ {
		a, b := ready[best], ready[i]
		switch {
		case a.Deadline.IsZero() && !b.Deadline.IsZero():
			best = i
		case !a.Deadline.IsZero() && b.Deadline.IsZero():
			// keep best
		case b.Deadline.Before(a.Deadline) ||
			(b.Deadline.Equal(a.Deadline) && b.ID < a.ID):
			best = i
		}
	}
	return best
}

// ---- Custom -----------------------------------------------------------

// Custom combines three signals into a single score: lower is better.
//
//	score = w1·(predicted/maxPredicted) + w2·(timeLeftToDeadline) + w3·(branchPenalty)
//
// All three terms are normalised into [0,1] so weights are comparable. The
// thesis Chapter 4 experiments vary these weights and shows the trade-off
// between makespan and SLA-violations.
type Custom struct {
	W1, W2, W3 float64
}

func (Custom) Name() string { return "custom" }

// branchImportance returns a [0, 1] penalty — 0 for main/master/release
// (more important, scheduled first), 1 for unknown/feature branches.
func branchImportance(branch string) float64 {
	b := strings.ToLower(branch)
	switch {
	case b == "main", b == "master", strings.HasPrefix(b, "release"):
		return 0
	case strings.HasPrefix(b, "hotfix"):
		return 0.1
	case strings.HasPrefix(b, "dev"), strings.HasPrefix(b, "feat"):
		return 0.7
	default:
		return 1
	}
}

func (c Custom) NextJob(ready []QueuedJob, now time.Time) int {
	// Normalise predicted_sec by max in this snapshot to keep score in [0,1].
	var maxP float64
	for _, j := range ready {
		if j.PredictedSec > maxP {
			maxP = j.PredictedSec
		}
	}
	if maxP == 0 {
		maxP = 1
	}

	best := 0
	bestScore := c.score(ready[0], now, maxP)
	for i := 1; i < len(ready); i++ {
		s := c.score(ready[i], now, maxP)
		if s < bestScore ||
			(s == bestScore && ready[i].ID < ready[best].ID) {
			best = i
			bestScore = s
		}
	}
	return best
}

func (c Custom) score(j QueuedJob, now time.Time, maxPredicted float64) float64 {
	short := j.PredictedSec / maxPredicted
	branch := branchImportance(j.Branch)

	// Deadline urgency: 0 if past deadline (most urgent), 1 if far away.
	// Normalisation horizon: 24h; jobs further out are effectively "no rush".
	deadlineTerm := 1.0
	if !j.Deadline.IsZero() {
		remaining := j.Deadline.Sub(now).Seconds()
		const horizon = 24 * 3600.0
		if remaining < 0 {
			deadlineTerm = 0
		} else if remaining < horizon {
			deadlineTerm = remaining / horizon
		}
	}

	return c.W1*short + c.W2*deadlineTerm + c.W3*branch
}

// ---- Construction ------------------------------------------------------

// New builds a strategy by canonical name. Returns nil for unknown names —
// callers check this before scheduling.
func New(name string, customWeights CustomWeights) Strategy {
	switch strings.ToLower(name) {
	case "fifo":
		return FIFO{}
	case "sjf":
		return SJF{}
	case "edf":
		return EDF{}
	case "custom":
		return Custom{W1: customWeights.ShortJob, W2: customWeights.Deadline, W3: customWeights.Branch}
	default:
		return nil
	}
}

// CustomWeights bundles the configurable weights for the Custom strategy.
// Persisted in system_state.custom_weights, editable from /admin.
type CustomWeights struct {
	ShortJob float64 `json:"short_job"`
	Deadline float64 `json:"deadline"`
	Branch   float64 `json:"branch"`
}

// DefaultCustomWeights mirrors the seed value in the migration.
func DefaultCustomWeights() CustomWeights {
	return CustomWeights{ShortJob: 0.6, Deadline: 0.3, Branch: 0.1}
}

// AvailableStrategies lists everything New() understands. Frontend uses
// this to render the simulator's strategy picker. Keeping it inline keeps
// the names in lockstep with the New() switch.
func AvailableStrategies() []string {
	return []string{"fifo", "sjf", "edf", "custom"}
}

// DequeueAll repeatedly calls NextJob until the queue is empty, returning
// the order in which a strategy would have issued jobs from a fixed input.
// Useful for inspecting strategy behaviour without running the full
// time-aware simulator (sim.go).
func DequeueAll(s Strategy, ready []QueuedJob, now time.Time) []QueuedJob {
	work := append([]QueuedJob(nil), ready...)
	out := make([]QueuedJob, 0, len(work))
	for len(work) > 0 {
		idx := s.NextJob(work, now)
		out = append(out, work[idx])
		work = append(work[:idx], work[idx+1:]...)
	}
	return out
}
