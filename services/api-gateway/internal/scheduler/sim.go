package scheduler

import (
	"math"
	"sort"
	"time"
)

// SimulatorJob is the input row to the replay engine. It carries both the
// estimate the strategy will see (PredictedSec) and the ground-truth
// duration that actually elapses (ActualSec) — these can differ when ML
// predictions miss. For oracle simulations PredictedSec == ActualSec.
type SimulatorJob struct {
	ID           int64
	Repo         string
	Branch       string
	ArrivedAt    time.Time
	PredictedSec float64
	ActualSec    float64
	Deadline     time.Time // zero if no SLA
}

// SimulatorConfig — tweakable knobs the user picks on /simulator.
type SimulatorConfig struct {
	// Number of parallel runners. 1 reproduces single-lane serial CI;
	// 5–10 is the typical mid-size GitHub Actions cap.
	Runners int
}

// Metrics are everything the thesis needs from one simulation run. The
// fields map 1:1 to columns in `sim_runs` so persistence is trivial.
type Metrics struct {
	Strategy          string  `json:"strategy"`
	JobsCount         int     `json:"jobs_count"`
	MakespanSec       float64 `json:"makespan_sec"`
	WaitP50Sec        float64 `json:"wait_p50_sec"`
	WaitP95Sec        float64 `json:"wait_p95_sec"`
	WaitMeanSec       float64 `json:"wait_mean_sec"`
	ThroughputPerMin  float64 `json:"throughput_per_min"`
	SLAViolations     int     `json:"sla_violations"`
	WindowStart       time.Time `json:"window_start"`
	WindowEnd         time.Time `json:"window_end"`
}

// Run executes one strategy over a snapshot of jobs and returns metrics.
//
// Replay model:
//   - A virtual clock starts at the earliest ArrivedAt.
//   - At each step the simulator advances time to the next "interesting"
//     instant: either a new arrival or a runner becoming free.
//   - Whenever a runner is free and there are ready jobs, the strategy
//     picks one and it starts executing for ActualSec.
//
// This is event-driven, not tick-based — runtime is O(N log N) on the
// number of jobs, not on the total wall-time of the window.
func Run(s Strategy, jobs []SimulatorJob, cfg SimulatorConfig) Metrics {
	if cfg.Runners <= 0 {
		cfg.Runners = 1
	}
	if len(jobs) == 0 {
		return Metrics{Strategy: s.Name()}
	}

	// Sort by arrival so we can stream-feed the queue. Stable sort keeps
	// equal arrivals in their original (ID) order — important for
	// reproducible test fixtures.
	sorted := make([]SimulatorJob, len(jobs))
	copy(sorted, jobs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].ArrivedAt.Before(sorted[j].ArrivedAt)
	})

	start := sorted[0].ArrivedAt
	now := start

	// runnerFreeAt[i] is the timestamp when runner i becomes free.
	runnerFreeAt := make([]time.Time, cfg.Runners)
	for i := range runnerFreeAt {
		runnerFreeAt[i] = start
	}

	// ready holds jobs that have arrived but not yet started.
	ready := []QueuedJob{}
	nextArrival := 0

	waits := make([]float64, 0, len(sorted))
	slaViolations := 0
	lastCompletion := start
	completed := 0

	for completed < len(sorted) {
		// 1. Admit arrivals up to `now`.
		for nextArrival < len(sorted) && !sorted[nextArrival].ArrivedAt.After(now) {
			j := sorted[nextArrival]
			ready = append(ready, QueuedJob{
				ID: j.ID, Repo: j.Repo, Branch: j.Branch,
				ArrivedAt: j.ArrivedAt, PredictedSec: j.PredictedSec, Deadline: j.Deadline,
			})
			nextArrival++
		}

		// 2. Dispatch as many jobs as we have free runners.
		dispatched := false
		for len(ready) > 0 {
			freeRunner := -1
			for i, t := range runnerFreeAt {
				if !t.After(now) {
					freeRunner = i
					break
				}
			}
			if freeRunner == -1 {
				break
			}

			idx := s.NextJob(ready, now)
			picked := ready[idx]
			ready = append(ready[:idx], ready[idx+1:]...)

			// We need the actual duration to advance the runner — look it
			// up in the original sorted slice (cheap: small N, linear scan
			// inside the index range we know the job came from).
			var actual float64
			for k := range sorted {
				if sorted[k].ID == picked.ID {
					actual = sorted[k].ActualSec
					break
				}
			}
			startedAt := now
			waitSec := startedAt.Sub(picked.ArrivedAt).Seconds()
			if waitSec < 0 {
				waitSec = 0
			}
			waits = append(waits, waitSec)

			completedAt := startedAt.Add(time.Duration(actual * float64(time.Second)))
			runnerFreeAt[freeRunner] = completedAt
			if completedAt.After(lastCompletion) {
				lastCompletion = completedAt
			}
			if !picked.Deadline.IsZero() && completedAt.After(picked.Deadline) {
				slaViolations++
			}
			completed++
			dispatched = true
		}

		if completed >= len(sorted) {
			break
		}

		// 3. Advance the clock to the next interesting moment: either the
		// next arrival, or the next runner freeing up.
		next := time.Time{}
		if nextArrival < len(sorted) {
			next = sorted[nextArrival].ArrivedAt
		}
		earliestFree := time.Time{}
		for _, t := range runnerFreeAt {
			if earliestFree.IsZero() || t.Before(earliestFree) {
				earliestFree = t
			}
		}
		// If a runner will be free before the next arrival (or there are
		// no more arrivals), jump there. Otherwise jump to the arrival.
		switch {
		case next.IsZero() && !earliestFree.IsZero():
			now = earliestFree
		case !next.IsZero() && earliestFree.IsZero():
			now = next
		case earliestFree.After(now) && earliestFree.Before(next):
			now = earliestFree
		case !next.IsZero():
			now = next
		}

		// Guard against pathological inputs (e.g. all jobs with 0 duration
		// and no progress) — if we didn't dispatch anything and the clock
		// didn't move, force-advance by a second to avoid an infinite loop.
		if !dispatched && now.Equal(start) && nextArrival == 0 {
			now = now.Add(time.Second)
		}
	}

	windowStart := sorted[0].ArrivedAt
	windowEnd := sorted[len(sorted)-1].ArrivedAt
	makespan := lastCompletion.Sub(windowStart).Seconds()

	throughputPerMin := 0.0
	if makespan > 0 {
		throughputPerMin = float64(len(sorted)) / (makespan / 60.0)
	}

	return Metrics{
		Strategy:         s.Name(),
		JobsCount:        len(sorted),
		MakespanSec:      makespan,
		WaitP50Sec:       percentile(waits, 0.50),
		WaitP95Sec:       percentile(waits, 0.95),
		WaitMeanSec:      mean(waits),
		ThroughputPerMin: throughputPerMin,
		SLAViolations:    slaViolations,
		WindowStart:      windowStart,
		WindowEnd:        windowEnd,
	}
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// percentile uses linear interpolation between adjacent ranks — same
// convention as NumPy's default (method='linear'). Important for matching
// dissertation tables to whatever offline analysis script the reviewer
// runs later.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
