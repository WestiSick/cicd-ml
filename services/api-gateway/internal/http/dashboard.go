package http

import (
	"net/http"
	"time"
)

// GET /api/dashboard/load-24h
//
// Hourly histogram of completed jobs in the last 24h — drives the
// sparkline on /dashboard ("how busy were we today"). Returns one
// bucket per hour aligned to UTC; the frontend rotates the array so
// the rightmost cell is the current hour.
//
// Cheap query (count + group_by on a < 100K-row index); ok to poll at
// /dashboard's 5s tick.
func (s *Server) dashboardLoad24h(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Pool.Query(r.Context(), `
		SELECT date_trunc('hour', w.created_at) AS bucket,
		       COUNT(*)                          AS jobs,
		       COALESCE(AVG(j.duration_sec)::FLOAT, 0) AS mean_sec
		FROM jobs j
		JOIN workflow_runs w ON j.run_id = w.id
		WHERE w.created_at >= now() - interval '24 hours'
		  AND j.duration_sec IS NOT NULL
		GROUP BY bucket
		ORDER BY bucket ASC
	`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load24h_failed",
			"Could not load 24h load", "")
		return
	}
	defer rows.Close()

	type bucket struct {
		Hour    string  `json:"hour"` // ISO timestamp of the hour-start
		Jobs    int     `json:"jobs"`
		MeanSec float64 `json:"mean_sec"`
	}
	out := make([]bucket, 0, 24)
	for rows.Next() {
		var b bucket
		var h time.Time
		if err := rows.Scan(&h, &b.Jobs, &b.MeanSec); err == nil {
			b.Hour = h.UTC().Format(time.RFC3339)
			out = append(out, b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"buckets": out})
}
