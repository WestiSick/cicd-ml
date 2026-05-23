// Package http wires up the chi router with all REST + WebSocket routes.
//
// Single source of truth for the route map. Handlers themselves live in
// dedicated files (system.go, repos.go, setup.go, bgjobs.go, ws.go).
//
// Route map:
//
//	GET    /healthz                  — liveness, no auth
//	GET    /api/system/state         — bootstrap_done, active model/strategy
//	GET    /api/repos                — list tracked repositories
//	POST   /api/repos                — add by URL (body: {url, branches?})
//	GET    /api/bg-jobs              — recent background jobs
//	GET    /api/bg-jobs/:id          — single job
//	GET    /api/activity             — recent user-visible actions
//	POST   /api/setup/start          — onboarding entry point
//	POST   /webhooks/github          — GitHub webhook receiver (stub)
//	GET    /ws/bootstrap             — alias of /ws/bg-jobs filtered to bootstrap kinds
//	GET    /ws/bg-jobs               — all bg jobs progress stream
//	GET    /ws/queue                 — live queue + push feed (stub)
//	GET    /ws/training/:id          — training metrics stream (stub)
package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/buzdin/cicd-ml/api-gateway/internal/bootstrap"
	"github.com/buzdin/cicd-ml/api-gateway/internal/config"
	gh "github.com/buzdin/cicd-ml/api-gateway/internal/github"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ml"
	"github.com/buzdin/cicd-ml/api-gateway/internal/store"
	"github.com/buzdin/cicd-ml/api-gateway/internal/ws"
)

type Server struct {
	cfg               config.Config
	db                *store.DB
	hub               *ws.Hub
	mlClient          *ml.Client
	orches            *bootstrap.Orchestrator
	installer         *gh.Installer // GitHub webhook installer — shared across handlers
	recentPredictions *predictionCache
	r                 chi.Router
}

func NewServer(cfg config.Config, db *store.DB, hub *ws.Hub, mlClient *ml.Client) *Server {
	// Compute the callback URL once at startup so all handlers use the
	// same string. If PUBLIC_API_BASE is misconfigured, IsReachable
	// downstream will skip the call rather than asking GitHub to POST
	// to localhost.
	callbackURL := strings.TrimRight(cfg.PublicAPIBase, "/") + "/webhooks/github"

	installer := gh.NewInstaller(callbackURL, cfg.GithubWebhookSecret)
	s := &Server{
		cfg:       cfg,
		db:        db,
		hub:       hub,
		mlClient:  mlClient,
		orches:    bootstrap.NewOrchestrator(db).WithInstaller(installerAdapter{inner: installer}),
		installer: installer,
		// 30-minute TTL covers the longest typical CI workflow_run with
		// margin. 1k entries is generous for single-user scale.
		recentPredictions: newPredictionCache(30*time.Minute, 1000),
	}
	s.r = s.buildRouter()
	return s
}

func (s *Server) Router() chi.Router                    { return s.r }
func (s *Server) Orchestrator() *bootstrap.Orchestrator { return s.orches }

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// Timeout middleware excluded from WebSocket routes — chi.Mux applies it
	// to subtrees we mount it on, not globally.
	apiTimeout := middleware.Timeout(60 * time.Second)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:5173", s.cfg.PublicAPIBase},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Get("/healthz", s.healthz)

	r.Route("/api", func(r chi.Router) {
		r.Use(apiTimeout)

		// System state — the bootstrap gate.
		r.Get("/system/state", s.getSystemState)

		// Repositories.
		r.Get("/repos", s.listRepos)
		r.Post("/repos", s.addRepo)
		r.Post("/repos/{id}/sync", s.syncRepo)
		r.Post("/repos/{id}/pause", s.pauseRepo)
		r.Post("/repos/{id}/resume", s.resumeRepo)
		r.Post("/repos/{id}/resync", s.resyncRepo)
		r.Post("/repos/{id}/webhook", s.installRepoWebhook)
		r.Delete("/repos/{id}/webhook", s.removeRepoWebhook)
		r.Delete("/repos/{id}", s.deleteRepo)

		// Setup / bootstrap.
		r.Post("/setup/start", s.startSetup)

		// Background jobs (read-only — workers write).
		r.Get("/bg-jobs", s.listBGJobs)
		r.Get("/bg-jobs/{id}", s.getBGJob)
		r.Post("/bg-jobs/{id}/cancel", s.cancelBGJob)

		// Activity log.
		r.Get("/activity", s.listActivity)

		// Admin / diagnostics.
		r.Get("/admin/webhooks", s.listAdminWebhooks)
		r.Get("/admin/calibrations", s.listAdminCalibrations)
		r.Get("/admin/health", s.systemHealth)
		r.Post("/admin/settings", s.updateAdminSettings)
		r.Post("/admin/bg-jobs/pause", s.pauseBGRunner)
		r.Post("/admin/bg-jobs/resume", s.resumeBGRunner)

		// Models registry.
		r.Get("/models", s.listModels)
		r.Get("/models/{id}", s.getModel)
		r.Get("/models/{id}/feature-importance", s.getModelFeatureImportance)
		r.Get("/models/{id}/predicted-vs-actual", s.getModelPredictedVsActual)
		r.Get("/models/{id}/download", s.downloadModelArtifact)
		r.Post("/models/{id}/activate", s.activateModel)
		r.Delete("/models/{id}", s.deleteModel)

		// Thesis pack export — bundles every dissertation-relevant CSV
		// into the mounted thesis-output volume.
		r.Post("/experiments/export-thesis-pack", s.exportThesisPack)

		// Training — POST enqueues a train_model bg_job; the worker
		// proxies to ml-service. GET reads back from bg_jobs.
		r.Post("/training", s.startTraining)
		r.Post("/training/cv", s.crossValidate)
		r.Get("/training/{id}", s.getBGJob) // reuse bg_jobs handler
		r.Get("/training/{id}/metrics", s.listTrainingMetrics)

		// Internal — ml-service posts per-iteration metrics here.
		r.Post("/internal/training/{id}/metric", s.postTrainingMetric)
		r.Post("/internal/broadcast", s.internalBroadcast)

		r.Get("/simulator/runs", s.listSimRuns)
		r.Post("/simulator/run", s.runSimulator)
		r.Get("/simulator/strategies", s.listStrategies)
		r.Get("/simulator/runs/{id}/export.csv", s.exportSimRunCSV)
		r.Get("/queue", s.queueSnapshot)
		r.Get("/dashboard/load-24h", s.dashboardLoad24h)
		r.Get("/datasets", s.datasetsSummary)
		r.Get("/datasets/coverage", s.datasetsCoverage)
		r.Get("/datasets/timeline", s.datasetsTimeline)
		r.Get("/datasets/{id}", s.datasetDetail)
		r.Get("/datasets/{id}/features", s.datasetFeaturePreview)
		r.Get("/datasets/{id}/export.csv", s.exportDatasetCSV)
		r.Get("/datasets/{id}/push-recommendations", s.datasetPushRecommendations)
	})

	r.Post("/webhooks/github", s.handleGithubWebhook)

	// WebSocket routes — no timeout middleware (long-lived connections).
	r.Get("/ws/queue", func(w http.ResponseWriter, req *http.Request) { s.hub.Serve(w, req, "queue") })
	r.Get("/ws/bootstrap", func(w http.ResponseWriter, req *http.Request) { s.hub.Serve(w, req, "bg-jobs") })
	r.Get("/ws/bg-jobs", func(w http.ResponseWriter, req *http.Request) { s.hub.Serve(w, req, "bg-jobs") })
	r.Get("/ws/training/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		s.hub.Serve(w, req, "training/"+id)
	})

	return r
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// notImplemented used to back stub routes like /api/datasets and
// /api/queue before they got real handlers. Now that every route has
// an implementation we no longer reference it, but we keep the helper
// commented out here as a template for the next time a placeholder
// route is needed.
//
//   func (s *Server) notImplemented(label string) http.HandlerFunc {
//       return func(w http.ResponseWriter, _ *http.Request) {
//           writeJSON(w, http.StatusNotImplemented, errorEnvelope{
//               Error: errorBody{
//                   Code: "not_implemented", ...
//               },
//           })
//       }
//   }

// errorEnvelope matches the canonical contract — see docs/architecture.md.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	UserAction string `json:"user_action,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message, userAction string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Code: code, Message: message, UserAction: userAction}})
}
