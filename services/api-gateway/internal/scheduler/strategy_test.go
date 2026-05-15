package scheduler

import (
	"testing"
	"time"
)

func mk(id int64, branch string, arrivedMin int, predSec float64, deadlineMin int) QueuedJob {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var dl time.Time
	if deadlineMin > 0 {
		dl = base.Add(time.Duration(deadlineMin) * time.Minute)
	}
	return QueuedJob{
		ID: id, Branch: branch,
		ArrivedAt:    base.Add(time.Duration(arrivedMin) * time.Minute),
		PredictedSec: predSec,
		Deadline:     dl,
	}
}

func ids(order []QueuedJob) []int64 {
	out := make([]int64, len(order))
	for i, j := range order {
		out[i] = j.ID
	}
	return out
}

func eq(t *testing.T, name string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len got=%d want=%d (got=%v want=%v)", name, len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: order mismatch got=%v want=%v", name, got, want)
		}
	}
}

func TestFIFO(t *testing.T) {
	q := []QueuedJob{
		mk(1, "main", 5, 200, 0),
		mk(2, "feat", 1, 50, 0),
		mk(3, "main", 3, 100, 0),
	}
	got := ids(DequeueAll(FIFO{}, q, time.Time{}))
	eq(t, "fifo", got, []int64{2, 3, 1})
}

func TestSJF(t *testing.T) {
	q := []QueuedJob{
		mk(1, "main", 0, 200, 0),
		mk(2, "feat", 0, 50, 0),
		mk(3, "main", 0, 100, 0),
	}
	got := ids(DequeueAll(SJF{}, q, time.Time{}))
	eq(t, "sjf", got, []int64{2, 3, 1})
}

func TestEDF(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q := []QueuedJob{
		mk(1, "main", 0, 200, 60),   // deadline t+60
		mk(2, "feat", 0, 50, 0),     // no deadline → tail
		mk(3, "main", 0, 100, 10),   // deadline t+10
	}
	got := ids(DequeueAll(EDF{}, q, now))
	eq(t, "edf", got, []int64{3, 1, 2})
}

func TestEDFAllNoDeadline_BreaksTiesByID(t *testing.T) {
	q := []QueuedJob{
		mk(7, "feat", 0, 50, 0),
		mk(3, "feat", 0, 99, 0),
	}
	got := ids(DequeueAll(EDF{}, q, time.Time{}))
	eq(t, "edf-no-deadline", got, []int64{3, 7})
}

func TestCustomPrefersShortAndMainBranch(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Custom{W1: 0.6, W2: 0.3, W3: 0.1}
	q := []QueuedJob{
		// Equal deadlines, equal lengths — branch decides.
		mk(1, "main", 0, 100, 60),
		mk(2, "feat", 0, 100, 60),
	}
	got := ids(DequeueAll(c, q, now))
	eq(t, "custom-branch", got, []int64{1, 2})
}

func TestNewByName(t *testing.T) {
	for _, n := range []string{"fifo", "sjf", "edf", "custom"} {
		if New(n, DefaultCustomWeights()) == nil {
			t.Fatalf("New(%q) returned nil", n)
		}
	}
	if New("garbage", DefaultCustomWeights()) != nil {
		t.Fatal("unknown name should return nil")
	}
}
