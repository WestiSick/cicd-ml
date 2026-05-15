package bgjobs

import (
	"testing"

	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// Sanity check on the default pool layout. If someone tightens the
// io-bound pool to zero, training starvation comes back; this test
// guards against that.
func TestDefaultPoolsCoverAllKinds(t *testing.T) {
	covered := map[string]bool{}
	for _, p := range DefaultPools {
		for _, k := range p.Kinds {
			covered[k] = true
		}
	}
	want := []string{
		store.JobKindCollectHistory,
		store.JobKindRefresh,
		store.JobKindBootstrap,
		store.JobKindComputeFeatures,
		store.JobKindTrainModel,
		store.JobKindSimulate,
	}
	for _, k := range want {
		if !covered[k] {
			t.Fatalf("kind %q is not handled by any default pool", k)
		}
	}
}

// The io pool must NOT include train_model — that's the whole point of
// the split. Catching this regression is cheap; the alternative is
// rediscovering head-of-line blocking in production-ish use.
func TestIOPoolExcludesComputeKinds(t *testing.T) {
	var ioKinds []string
	for _, p := range DefaultPools {
		if p.Name == "io" {
			ioKinds = p.Kinds
		}
	}
	for _, kind := range ioKinds {
		if kind == store.JobKindTrainModel || kind == store.JobKindBootstrap {
			t.Fatalf("io pool must not handle %q — it would re-introduce head-of-line blocking", kind)
		}
	}
}

// Concurrency invariants: io pool stays single-worker (deliberate, see
// docstring), compute pool has at least 2 so a stuck handler doesn't
// block the others.
func TestPoolConcurrencyInvariants(t *testing.T) {
	for _, p := range DefaultPools {
		if p.Concurrency < 1 {
			t.Fatalf("pool %q has zero concurrency — workers wouldn't run", p.Name)
		}
		if p.Name == "compute" && p.Concurrency < 2 {
			t.Fatalf("compute pool needs >=2 workers (got %d) so one slow handler can't block training", p.Concurrency)
		}
	}
}
