package http

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GET /api/datasets/{id}/push-recommendations?days=90&tz=Europe/Moscow
//
// "Когда лучше пушить" heatmap per repository — aggregates historical
// CI jobs of this repo into a 24×7 grid (hour-of-day × day-of-week) and
// reports, for each cell, the average end-to-end time relative to the
// repo's overall mean.
//
// End-to-end = wait (run.created_at → job.started_at) + duration
// (job.started_at → job.completed_at). Wait is what genuinely varies
// with time-of-day on GitHub-hosted runners (queue depth); duration is
// in there too because what the user really wants to know is "when do
// I get my green check fastest", not "when is the queue shortest".
//
// Time-of-day is computed in the caller's timezone (default UTC) so a
// Russian dissertation looks at Moscow office hours, not 03:00 UTC.
//
// Days is the historical window (default 90, cap 365). dow is 0..6 with
// Monday = 0 — the European/ISO convention; the frontend renders labels
// "пн вт ср чт пт сб вс" in that order.
//
// Response shape (mean_*_sec are seconds; *_delta_pct are signed %, with
// negative = faster than mean = good):
//
//	{
//	  "repo_id": 1,
//	  "days": 90,
//	  "tz": "Europe/Moscow",
//	  "window_start": "2026-02-22T00:00:00Z",
//	  "window_end":   "2026-05-23T00:00:00Z",
//	  "overall": {
//	    "sample_count": 1842,
//	    "mean_wait_sec": 12.4,
//	    "mean_duration_sec": 240.3,
//	    "mean_total_sec": 252.7
//	  },
//	  "cells": [
//	    {
//	      "hour": 14, "dow": 2,
//	      "sample_count": 24,
//	      "mean_wait_sec": 18.0, "mean_duration_sec": 250.0, "mean_total_sec": 268.0,
//	      "wait_delta_pct": 45.2, "duration_delta_pct": 4.0, "total_delta_pct": 6.1
//	    }, ...
//	  ],
//	  "best":  { "hour": 4, "dow": 1, "total_delta_pct": -38.2 },
//	  "worst": { "hour": 18, "dow": 4, "total_delta_pct": 52.1 }
//	}
//
// Cells with `sample_count < min_samples` (default 3) are still returned
// but marked with the original count so the frontend can render them
// muted — empty cells (no data) are simply omitted.
func (s *Server) datasetPushRecommendations(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id",
			"Dataset id must be numeric", "Check the URL — should be /datasets/<numeric-id>.")
		return
	}

	days := 90
	if q := r.URL.Query().Get("days"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	// Timezone is passed straight to Postgres `AT TIME ZONE`, so we
	// must validate before binding — otherwise a hostile or malformed
	// value would produce an SQL error and a 500. Regex is the cheap,
	// well-bounded check; Postgres will still reject unknown names but
	// at that point we know the string itself is sane.
	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = "UTC"
	}
	if !validTZ.MatchString(tz) || len(tz) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_tz",
			"Timezone must be an IANA name like 'Europe/Moscow' or 'UTC'",
			"Drop the tz query parameter to use UTC.")
		return
	}

	// Existence check + nicer 404 than letting the empty aggregation
	// silently return zero cells.
	if _, err := s.db.LookupRepoByID(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "repo_not_found",
				"No repository with that id", "Reload /datasets to refresh the list.")
			return
		}
		writeError(w, http.StatusInternalServerError, "repo_lookup_failed",
			"Could not load repository", "Try again in a second.")
		return
	}

	ctx := r.Context()
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.AddDate(0, 0, -days)

	overall, err := s.pushRecsOverall(ctx, id, windowStart)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "push_recs_failed",
			"Could not aggregate push recommendations", "Try refreshing the page.")
		return
	}
	cells, err := s.pushRecsCells(ctx, id, windowStart, tz)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "push_recs_failed",
			"Could not aggregate push recommendations", "Try refreshing the page.")
		return
	}

	// Compute deltas + locate best/worst cells. We only consider cells
	// with at least minSamplesForRanking observations as best/worst
	// candidates — otherwise a single 1s job in the 04:00 Sunday cell
	// looks like the optimal push time, which is misleading.
	const minSamplesForRanking = 5
	type ranked struct {
		Hour       int     `json:"hour"`
		Dow        int     `json:"dow"`
		TotalDelta float64 `json:"total_delta_pct"`
	}
	var best, worst *ranked

	cellsOut := make([]map[string]any, 0, len(cells))
	for _, c := range cells {
		// Deltas are undefined when the overall mean is zero (no completed
		// jobs in the window) — the loop wouldn't run in that case but be
		// defensive in case of edge data.
		waitDelta := pctDelta(c.MeanWait, overall.MeanWait)
		durDelta := pctDelta(c.MeanDuration, overall.MeanDuration)
		totalDelta := pctDelta(c.MeanTotal(), overall.MeanTotal())

		cellsOut = append(cellsOut, map[string]any{
			"hour":               c.Hour,
			"dow":                c.Dow,
			"sample_count":       c.SampleCount,
			"mean_wait_sec":      c.MeanWait,
			"mean_duration_sec": c.MeanDuration,
			"mean_total_sec":     c.MeanTotal(),
			"wait_delta_pct":     waitDelta,
			"duration_delta_pct": durDelta,
			"total_delta_pct":    totalDelta,
		})

		if c.SampleCount >= minSamplesForRanking {
			r := ranked{Hour: c.Hour, Dow: c.Dow, TotalDelta: totalDelta}
			if best == nil || totalDelta < best.TotalDelta {
				bc := r
				best = &bc
			}
			if worst == nil || totalDelta > worst.TotalDelta {
				wc := r
				worst = &wc
			}
		}
	}

	resp := map[string]any{
		"repo_id":      id,
		"days":         days,
		"tz":           tz,
		"window_start": windowStart.Format(time.RFC3339),
		"window_end":   windowEnd.Format(time.RFC3339),
		"overall": map[string]any{
			"sample_count":      overall.SampleCount,
			"mean_wait_sec":     overall.MeanWait,
			"mean_duration_sec": overall.MeanDuration,
			"mean_total_sec":    overall.MeanTotal(),
		},
		"cells": cellsOut,
		"best":  best,
		"worst": worst,
	}
	writeJSON(w, http.StatusOK, resp)
}

// validTZ permits IANA timezone names and the canonical fixed values
// (UTC, GMT). Forbids spaces, semicolons, and anything else that could
// matter for SQL injection — though parameter binding already protects
// us; the regex is just to surface bad input as a clean 400.
var validTZ = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+/\-]*$`)

type pushRecsOverall struct {
	SampleCount  int64
	MeanWait     float64
	MeanDuration float64
}

func (o pushRecsOverall) MeanTotal() float64 { return o.MeanWait + o.MeanDuration }

func (s *Server) pushRecsOverall(ctx context.Context, repoID int64, windowStart time.Time) (pushRecsOverall, error) {
	// GREATEST clamps negative wait_sec — webhook arrival skew or
	// clock-skew between us and GitHub can occasionally produce
	// started_at < created_at by a second or two; we don't want those
	// pulling the mean down.
	row := s.db.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::bigint,
			COALESCE(AVG(GREATEST(EXTRACT(EPOCH FROM (j.started_at - w.created_at)), 0)), 0),
			COALESCE(AVG(j.duration_sec), 0)
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		WHERE w.repo_id = $1
		  AND w.created_at >= $2
		  AND j.started_at IS NOT NULL
		  AND j.duration_sec IS NOT NULL
	`, repoID, windowStart)
	var o pushRecsOverall
	if err := row.Scan(&o.SampleCount, &o.MeanWait, &o.MeanDuration); err != nil {
		return pushRecsOverall{}, err
	}
	return o, nil
}

type pushRecsCell struct {
	Hour         int
	Dow          int
	SampleCount  int64
	MeanWait     float64
	MeanDuration float64
}

func (c pushRecsCell) MeanTotal() float64 { return c.MeanWait + c.MeanDuration }

func (s *Server) pushRecsCells(ctx context.Context, repoID int64, windowStart time.Time, tz string) ([]pushRecsCell, error) {
	// AT TIME ZONE on a timestamptz returns the naive local time in the
	// target zone, so EXTRACT(hour/isodow) then reads it in that zone.
	// isodow returns 1..7 with Monday=1; we shift to 0..6 here so the
	// JSON contract matches the frontend's day-row index.
	rows, err := s.db.Pool.Query(ctx, `
		WITH base AS (
			SELECT
				EXTRACT(HOUR   FROM (w.created_at AT TIME ZONE $3))::int      AS hour,
				(EXTRACT(ISODOW FROM (w.created_at AT TIME ZONE $3))::int - 1) AS dow,
				GREATEST(EXTRACT(EPOCH FROM (j.started_at - w.created_at)), 0) AS wait_sec,
				j.duration_sec::numeric                                       AS duration_sec
			FROM jobs j
			JOIN workflow_runs w ON j.run_id = w.id
			WHERE w.repo_id = $1
			  AND w.created_at >= $2
			  AND j.started_at IS NOT NULL
			  AND j.duration_sec IS NOT NULL
		)
		SELECT hour, dow,
		       COUNT(*)::bigint,
		       AVG(wait_sec)::float8,
		       AVG(duration_sec)::float8
		FROM base
		GROUP BY hour, dow
		ORDER BY dow, hour
	`, repoID, windowStart, tz)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []pushRecsCell{}
	for rows.Next() {
		var c pushRecsCell
		if err := rows.Scan(&c.Hour, &c.Dow, &c.SampleCount, &c.MeanWait, &c.MeanDuration); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// pctDelta returns (value - baseline) / baseline * 100. Returns 0 when
// the baseline is zero or non-finite — the heatmap renders that as a
// neutral cell, which is the right thing to do when we have no signal.
func pctDelta(value, baseline float64) float64 {
	if baseline <= 0 || math.IsNaN(baseline) || math.IsInf(baseline, 0) {
		return 0
	}
	return (value - baseline) / baseline * 100.0
}
