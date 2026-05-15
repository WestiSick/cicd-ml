package http

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/buzdin/cicd-ml/api-gateway/internal/ml"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
)

// thesisOutputDir is the path inside the api container; the host
// counterpart is the `thesis-output` docker volume. We use a function
// rather than a constant so tests can override.
//
// All exported CSV/JSON ends up here. The user finds them at the same
// path on the host via `docker compose cp` or, in dev, the mounted
// volume.
func thesisOutputDir() string {
	if p := os.Getenv("THESIS_OUTPUT_DIR"); p != "" {
		return p
	}
	return "/var/lib/cicdml/thesis"
}

// POST /api/experiments/export-thesis-pack
//
// Writes a snapshot of every dissertation-relevant table into CSV under
// /var/lib/cicdml/thesis/<timestamp>/. Returns the directory path and
// list of files written. Synchronous — the operation is sub-second at
// our scale.
//
// Files (matched 1:1 with what plan §"Где взять материалы для
// диссертации" promises):
//
//   models.csv           — id, name, algo, MAE, RMSE, MAPE, R2, Spearman
//   strategy_comparison.csv — strategy, makespan, wait_p50, wait_p95, sla_viol
//   dataset_stats.csv    — repo, runs_count, jobs_count, oldest, newest
//   predicted_actual.csv — actual, predicted (for the active model)
//   feature_importance.csv — feature, value (for the active model)
//
// LaTeX-friendly: comma-separated, period decimal, no header quoting.
func (s *Server) exportThesisPack(w http.ResponseWriter, r *http.Request) {
	root := thesisOutputDir()
	stamp := time.Now().UTC().Format("20060102-150405")
	outDir := filepath.Join(root, stamp)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "mkdir_failed",
			"Could not create export directory: "+err.Error(),
			"Check that the thesis-output volume is mounted and writable.")
		return
	}

	// Bound the whole export to 30s — DB queries are tiny but the file
	// I/O could in theory stall on a misbehaving volume.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	written := []string{}
	type fileWriter struct {
		name string
		fn   func(context.Context, io.Writer) (int, error)
	}
	writers := []fileWriter{
		{name: "models.csv", fn: s.writeModelsCSV},
		{name: "strategy_comparison.csv", fn: s.writeStrategyCSV},
		{name: "dataset_stats.csv", fn: s.writeDatasetCSV},
		{name: "predicted_actual.csv", fn: s.writePredictedActualCSV},
		{name: "feature_importance.csv", fn: s.writeFeatureImportanceCSV},
	}
	type fileSummary struct {
		Name string `json:"name"`
		Rows int    `json:"rows"`
	}
	files := []fileSummary{}

	for _, fw := range writers {
		path := filepath.Join(outDir, fw.name)
		f, err := os.Create(path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "create_failed",
				"Could not create "+fw.name+": "+err.Error(), "")
			return
		}
		n, err := fw.fn(ctx, f)
		_ = f.Close()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "write_failed",
				"Could not write "+fw.name+": "+err.Error(), "")
			return
		}
		written = append(written, path)
		files = append(files, fileSummary{Name: fw.name, Rows: n})
	}

	// Trigger PNG/PDF figure generation in ml-service. We do this after
	// the CSVs land so a reviewer can spot the directory in the file
	// browser while figures are still rendering. Failures are reported
	// but don't fail the whole export — CSV is the canonical artifact.
	figFiles := []string{}
	if s.mlClient != nil {
		if resp, err := s.mlClient.ExportFigures(ctx, ml.ExportFiguresRequest{Timestamp: stamp}); err != nil {
			// Surface as a partial success — the user gets CSVs and a
			// clear note about what's missing.
			files = append(files, fileSummary{Name: "figures.error: " + err.Error(), Rows: 0})
		} else {
			figFiles = resp.Files
		}
	}

	_ = s.db.RecordActivity(r.Context(), "user", "export_thesis_pack", stamp,
		"thesis pack exported", true, map[string]any{
			"dir":     outDir,
			"files":   len(written),
			"figures": len(figFiles),
		})

	writeJSON(w, http.StatusOK, map[string]any{
		"directory": outDir,
		"timestamp": stamp,
		"files":     files,
		"figures":   figFiles,
	})
}

// ---- per-file writers --------------------------------------------------

func (s *Server) writeModelsCSV(ctx context.Context, w io.Writer) (int, error) {
	models, err := s.db.ListModels(ctx)
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"id", "name", "algo", "is_active",
		"mae_test_sec", "rmse_test_sec", "mape_test", "r2_test", "spearman_test",
		"trained_at",
	}); err != nil {
		return 0, err
	}
	for _, m := range models {
		var metrics map[string]float64
		_ = json.Unmarshal(m.Metrics, &metrics)
		row := []string{
			strconv.FormatInt(m.ID, 10),
			m.Name, m.Algo, strconv.FormatBool(m.IsActive),
			f(metrics["mae_test_sec"]),
			f(metrics["rmse_test_sec"]),
			f(metrics["mape_test"]),
			f(metrics["r2_test"]),
			f(metrics["spearman_test"]),
			m.TrainedAt.Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return 0, err
		}
	}
	return len(models), nil
}

func (s *Server) writeStrategyCSV(ctx context.Context, w io.Writer) (int, error) {
	runs, err := s.db.ListSimRuns(ctx, 200)
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"id", "strategy", "jobs_count",
		"makespan_sec", "wait_p50_sec", "wait_p95_sec", "throughput_per_min",
		"sla_violations", "window_start", "window_end", "created_at",
	}); err != nil {
		return 0, err
	}
	for _, r := range runs {
		row := []string{
			strconv.FormatInt(r.ID, 10), r.Strategy, strconv.Itoa(r.JobsCount),
			pf(r.MakespanSec), pf(r.WaitP50Sec), pf(r.WaitP95Sec),
			pf(r.ThroughputPerMin),
			pi(r.SLAViolations),
			r.WindowStart.Format(time.RFC3339),
			r.WindowEnd.Format(time.RFC3339),
			r.CreatedAt.Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return 0, err
		}
	}
	return len(runs), nil
}

func (s *Server) writeDatasetCSV(ctx context.Context, w io.Writer) (int, error) {
	repos, err := s.db.ListRepos(ctx)
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"id", "owner", "name", "status", "runs_count", "jobs_count",
		"oldest_run_at", "newest_run_at",
	}); err != nil {
		return 0, err
	}
	for _, r := range repos {
		row := []string{
			strconv.FormatInt(r.ID, 10), r.Owner, r.Name, r.Status,
			strconv.FormatInt(r.RunsCount, 10),
			strconv.FormatInt(r.JobsCount, 10),
			tsOrEmpty(r.OldestRunAt), tsOrEmpty(r.NewestRunAt),
		}
		if err := cw.Write(row); err != nil {
			return 0, err
		}
	}
	return len(repos), nil
}

func (s *Server) writePredictedActualCSV(ctx context.Context, w io.Writer) (int, error) {
	active, err := s.activeModel(ctx)
	if err != nil || active == nil {
		// No active model — write header only so the file exists for
		// downstream scripts that may glob() the directory.
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"job_id", "repo", "job_name", "actual_sec", "predicted_sec"})
		cw.Flush()
		return 0, nil
	}
	pts, err := s.db.ListPredictedActual(ctx, active.ID, 5000)
	if err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"job_id", "repo", "job_name", "actual_sec", "predicted_sec"}); err != nil {
		return 0, err
	}
	for _, p := range pts {
		row := []string{
			strconv.FormatInt(p.JobID, 10), p.Repo, p.JobName,
			strconv.Itoa(p.ActualSec), f(p.PredictedSec),
		}
		if err := cw.Write(row); err != nil {
			return 0, err
		}
	}
	return len(pts), nil
}

func (s *Server) writeFeatureImportanceCSV(ctx context.Context, w io.Writer) (int, error) {
	active, err := s.activeModel(ctx)
	if err != nil || active == nil {
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"feature", "importance"})
		cw.Flush()
		return 0, nil
	}
	importance := map[string]float64{}
	_ = json.Unmarshal(active.FeatureImportance, &importance)
	type item struct {
		name  string
		value float64
	}
	items := make([]item, 0, len(importance))
	for k, v := range importance {
		items = append(items, item{k, v})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].value > items[j].value })

	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"feature", "importance"}); err != nil {
		return 0, err
	}
	for _, it := range items {
		if err := cw.Write([]string{it.name, f(it.value)}); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// activeModel returns the currently-active model row, or nil if none.
// Wraps ListModels + filter to avoid duplicating SQL.
func (s *Server) activeModel(ctx context.Context) (*store.Model, error) {
	models, err := s.db.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	for i := range models {
		if models[i].IsActive {
			return &models[i], nil
		}
	}
	return nil, nil
}

// ---- formatting helpers ------------------------------------------------

func f(v float64) string {
	// Use 6 significant digits; enough for thesis precision without
	// trailing noise like 28.880306243896484.
	return strconv.FormatFloat(v, 'g', 6, 64)
}

func pf(v *float64) string {
	if v == nil {
		return ""
	}
	return f(*v)
}

func pi(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

func tsOrEmpty(v *time.Time) string {
	if v == nil {
		return ""
	}
	return v.Format(time.RFC3339)
}

