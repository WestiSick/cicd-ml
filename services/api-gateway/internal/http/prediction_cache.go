package http

import (
	"fmt"
	"sync"
	"time"
)

// predictionCache is an in-process, TTL-bounded map that remembers
// "we just predicted X for this workflow_run" so the matching `completed`
// event a few minutes later can compute δ-error = predicted - actual.
//
// Why in-memory rather than DB:
//   - The match window is short (typical CI run: 1-15 minutes). A new
//     row in `predictions` for every webhook prediction would explode
//     the table without giving us anything useful — we don't even know
//     the job_id at webhook time (jobs aren't in the DB yet).
//   - Survives a single process; loses everything on restart. Acceptable
//     because the worst case is the dashboard shows the `completed` event
//     without δ (still shows actual_sec). The collector backfills proper
//     prediction rows once the run is persisted.
//
// Capacity: capped at maxEntries. When full, oldest entries are evicted.
// 1000 is generous — even a busy demo never triggers more than a few
// dozen concurrent in-flight runs.
type predictionCache struct {
	mu      sync.Mutex
	entries map[string]predictionEntry
	ttl     time.Duration
	max     int
}

type predictionEntry struct {
	// PredictedSec is the value the user saw on the dashboard at
	// .requested time — model output × calibration factor. Used to
	// compute the dashboard's δ% on .completed.
	PredictedSec float64
	// PredictedRawSec is the un-calibrated model output. Used to
	// feed the calibration EMA on .completed so the factor converges
	// to the true model-vs-reality bias (rather than measuring its
	// own corrected residual, which would dampen convergence).
	PredictedRawSec    float64
	CalibrationFactor  float64
	ModelID            int64
	ModelAlgo          string
	RememberedAt       time.Time
}

func newPredictionCache(ttl time.Duration, max int) *predictionCache {
	return &predictionCache{
		entries: make(map[string]predictionEntry, max),
		ttl:     ttl,
		max:     max,
	}
}

// Remember stores a webhook-time prediction. `predicted` is the
// CALIBRATED value shown to the user (= raw × factor). `predictedRaw`
// and `factor` are kept alongside so the .completed handler can:
//
//   - feed the raw value into the calibration EMA (correct math: the
//     EMA learns the model's true bias, not its compounded residual),
//   - report δ% against the calibrated value (honest math: the user
//     saw `predicted`, so the error must be measured against that).
//
// When the caller has no calibration to apply (e.g. unknown repo or
// missing workflow), pass `predicted == predictedRaw` and `factor=1.0`.
func (c *predictionCache) Remember(repo string, runID int64, predicted, predictedRaw, factor float64, modelID int64, algo string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Quick eviction pass — drop expired entries before adding. Cheap at
	// this scale (a handful of map iterations per webhook).
	now := time.Now()
	if len(c.entries) >= c.max {
		c.evictExpired(now)
	}
	if len(c.entries) >= c.max {
		c.evictOldest()
	}
	c.entries[cacheKey(repo, runID)] = predictionEntry{
		PredictedSec:      predicted,
		PredictedRawSec:   predictedRaw,
		CalibrationFactor: factor,
		ModelID:           modelID,
		ModelAlgo:         algo,
		RememberedAt:      now,
	}
}

func (c *predictionCache) Get(repo string, runID int64) (predictionEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cacheKey(repo, runID)]
	if !ok {
		return predictionEntry{}, false
	}
	if time.Since(e.RememberedAt) > c.ttl {
		delete(c.entries, cacheKey(repo, runID))
		return predictionEntry{}, false
	}
	return e, true
}

func (c *predictionCache) Forget(repo string, runID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, cacheKey(repo, runID))
}

// evictExpired must be called with the mutex held.
func (c *predictionCache) evictExpired(now time.Time) {
	for k, e := range c.entries {
		if now.Sub(e.RememberedAt) > c.ttl {
			delete(c.entries, k)
		}
	}
}

// evictOldest drops one entry — the oldest by RememberedAt — so the
// cache stays within max. Must be called with the mutex held.
func (c *predictionCache) evictOldest() {
	var oldestKey string
	var oldestT time.Time
	first := true
	for k, e := range c.entries {
		if first || e.RememberedAt.Before(oldestT) {
			oldestKey = k
			oldestT = e.RememberedAt
			first = false
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func cacheKey(repo string, runID int64) string {
	return fmt.Sprintf("%s#%d", repo, runID)
}
