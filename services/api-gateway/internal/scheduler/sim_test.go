package scheduler

import (
	"math"
	"testing"
	"time"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// Three jobs arriving together, single runner, oracle SJF should pick the
// shortest first → minimum total wait.
func TestRun_SJFBeatsFIFOOnWaitTime(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := []SimulatorJob{
		{ID: 1, ArrivedAt: t0, PredictedSec: 300, ActualSec: 300}, // long
		{ID: 2, ArrivedAt: t0, PredictedSec: 60, ActualSec: 60},   // short
		{ID: 3, ArrivedAt: t0, PredictedSec: 120, ActualSec: 120}, // medium
	}
	cfg := SimulatorConfig{Runners: 1}

	fifo := Run(FIFO{}, jobs, cfg)
	sjf := Run(SJF{}, jobs, cfg)

	// Both must finish all jobs.
	if fifo.JobsCount != 3 || sjf.JobsCount != 3 {
		t.Fatalf("incomplete: fifo=%d sjf=%d", fifo.JobsCount, sjf.JobsCount)
	}
	// Makespan is identical when arrivals coincide and there's one runner.
	if !approx(fifo.MakespanSec, sjf.MakespanSec) || !approx(fifo.MakespanSec, 480) {
		t.Fatalf("makespan mismatch: fifo=%.0f sjf=%.0f (want 480)", fifo.MakespanSec, sjf.MakespanSec)
	}
	// But mean wait should be strictly smaller for SJF — that's the whole point.
	if !(sjf.WaitMeanSec < fifo.WaitMeanSec) {
		t.Fatalf("expected SJF wait_mean(%.1f) < FIFO wait_mean(%.1f)", sjf.WaitMeanSec, fifo.WaitMeanSec)
	}
}

// With two runners and three jobs (300, 60, 120) arriving together, total
// makespan should equal 300s — runner A takes the long one, runner B
// takes the short, then 60 + 120 = 180s ≤ 300s. The hard constraint is
// "we never finish before the longest individual job".
func TestRun_TwoRunnersParallelism(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := []SimulatorJob{
		{ID: 1, ArrivedAt: t0, PredictedSec: 300, ActualSec: 300},
		{ID: 2, ArrivedAt: t0, PredictedSec: 60, ActualSec: 60},
		{ID: 3, ArrivedAt: t0, PredictedSec: 120, ActualSec: 120},
	}
	m := Run(SJF{}, jobs, SimulatorConfig{Runners: 2})
	if m.MakespanSec < 300 {
		t.Fatalf("makespan %.0f < 300 — impossible", m.MakespanSec)
	}
	if m.MakespanSec > 480 {
		t.Fatalf("makespan %.0f > serial bound — parallelism broken", m.MakespanSec)
	}
}

// SLA violations: with two runners, the long job (deadline 60s, runs 600s)
// always misses; the short one (deadline 120s, runs 30s) makes it.
// This is the typical "ceiling" scenario for thesis Chapter 4.
func TestRun_SLAViolations(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jobs := []SimulatorJob{
		{ID: 1, ArrivedAt: t0, PredictedSec: 600, ActualSec: 600,
			Deadline: t0.Add(60 * time.Second)},
		{ID: 2, ArrivedAt: t0, PredictedSec: 30, ActualSec: 30,
			Deadline: t0.Add(120 * time.Second)},
	}
	m := Run(FIFO{}, jobs, SimulatorConfig{Runners: 2})
	if m.SLAViolations != 1 {
		t.Fatalf("expected 1 SLA violation (only the long job), got %d", m.SLAViolations)
	}
}

func TestRun_EmptyInput(t *testing.T) {
	m := Run(FIFO{}, nil, SimulatorConfig{Runners: 1})
	if m.JobsCount != 0 || m.MakespanSec != 0 {
		t.Fatalf("expected zero-valued metrics, got %+v", m)
	}
}

func TestPercentile(t *testing.T) {
	xs := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile(xs, 0.5); !approx(got, 55) {
		t.Fatalf("p50 got %.4f, want 55", got)
	}
	if got := percentile(xs, 0.95); !approx(got, 95.5) {
		t.Fatalf("p95 got %.4f, want 95.5", got)
	}
	if got := percentile([]float64{}, 0.5); got != 0 {
		t.Fatalf("empty got %v", got)
	}
}
