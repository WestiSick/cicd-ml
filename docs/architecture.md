# Architecture

System architecture for `cicd-ml` — the practical artifact of the master's
thesis on ML-based CI/CD prediction and scheduling.

## Components

```
                ┌──────────────────────────────────────────────┐
                │             Frontend (React + TS)            │
                │  /dashboard /datasets /experiments /admin    │
                └────────────────┬──────────────┬──────────────┘
                                 │ REST         │ WebSocket
                                 ▼              ▼
                    ┌──────────────────────────────────┐
                    │       api-gateway (Go, chi)      │
                    │  REST · WS · webhooks · auth     │
                    │  scheduler · bootstrap · bg_jobs │
                    └──┬───────────────┬───────────────┘
                       │               │
                       │  POST /predict │
                       ▼               ▼
            ┌──────────────────┐  ┌──────────────────┐
            │  ml-service      │  │     Redis        │
            │  (FastAPI)       │  │  queue · pub/sub │
            │ train · predict  │  └──────────────────┘
            └────────┬─────────┘
                     │
                     ▼
            ┌──────────────────────────────────────┐
            │            PostgreSQL                 │
            │  repos · runs · jobs · features ·     │
            │  models · predictions · bg_jobs ·     │
            │  sim_runs · training_metrics · …      │
            └──────────────────────────────────────┘
                     ▲
                     │
            ┌──────────────────┐
            │   collector      │
            │  GitHub Actions  │
            │  history ingest  │
            └──────────────────┘
```

## Data flow

1. **Ingestion.** `collector` consumes `bg_jobs` of kind `collect_history`
   or `refresh`, pulls workflow runs and jobs from GitHub, persists them in
   `workflow_runs` / `jobs` / `commits`. Checkpoints are written so the
   worker resumes on restart.
2. **Feature engineering.** `ml-service` reads the raw tables, computes a
   feature vector per job, materialises it into the `features` table (JSONB
   + a feature version number).
3. **Training.** A user-triggered training run inserts a `bg_jobs` row of
   kind `train_model`. The worker streams per-iteration metrics into
   `training_metrics`; the api-gateway broadcasts them over
   `/ws/training/:id`. On completion, the model artifact is written to the
   shared `model-artifacts` volume and a row is added to `models`.
4. **Prediction.** On a GitHub webhook the api-gateway computes the feature
   vector for the incoming job, calls `ml-service` `/predict`, writes a row
   to `predictions`, enqueues the job in Redis (sorted set keyed by the
   active strategy's score), and broadcasts `job.enqueued` over `/ws/queue`.
5. **Scheduling.** The scheduler component (inside api-gateway) pops jobs
   from the active strategy's queue. In hybrid mode it does not execute
   anything — it records the decision and lets GitHub Actions actually run
   the job, then back-fills `actual_duration` from the
   `workflow_run.completed` webhook.
6. **Simulation.** `simulator` performs offline replay of a historical job
   stream through all selected strategies, writing rows to `sim_runs`.

## Error envelope (UI feedback contract)

Every endpoint — Go or Python — returns errors in the same shape:

```json
{
  "error": {
    "code": "github_rate_limited",
    "message": "GitHub API rate limit exceeded",
    "details": { "reset_at": "2026-05-14T19:42:00Z" },
    "user_action": "Wait until reset or add a GitHub token in /admin."
  }
}
```

The frontend reads `user_action` and surfaces it verbatim in a toast (via
sonner). Internal codes and stack traces never reach the UI — they live
in the logs.

## Background jobs

The `bg_jobs` table is the single source of truth for any work that takes
longer than a request. Every worker — collector, training worker, simulator —
reads its row, updates `progress / total / message / logs_tail`, and the
api-gateway streams those updates over `/ws/bg-jobs`. The UI subscribes
once and renders progress chips wherever they apply.

## Design system

> See the plan §7.2 for the full specification. Highlights:
>
> - Tokens live in `frontend/src/styles/tokens.css` — the only place colours
>   and sizes are defined.
> - Two fonts: `Inter` (display/UI) + `JetBrains Mono` (every technical
>   value: id, sha, sec, metrics). The mono font is what makes the UI feel
>   like an instrument, not a marketing page.
> - One accent colour: warm amber `#f2c94c`. Status uses
>   green/amber/red/blue, but only for status — never decoration.
> - Squared geometry: radius 6–8px, no shadows, no gradients.
> - Density: row height 36–40 px (Linear-class), not 64 px.
> - Animation budget: 120ms hover, 180ms entry, 240ms modal. No bouncy
>   springs.
> - No shadcn-defaults, no MUI-defaults, no purple/blue gradients, no
>   glassmorphism, no decorative icons. These are all signals of
>   "AI-generated" design.

## WebSocket channels

| Channel | Purpose |
|---|---|
| `/ws/bootstrap`        | First-run setup progress. |
| `/ws/bg-jobs`          | Progress for any background job. |
| `/ws/queue`            | Live queue + push feed (for `/dashboard`). |
| `/ws/training/:id`     | Per-iteration metrics + log tail for a training run. |

All channels broadcast JSON messages; the api-gateway maintains a
fan-out from Redis Pub/Sub to active WebSocket subscribers.

## Why Go + Python (not one language)

- **Go** for everything I/O-shaped: HTTP server, Postgres pool, Redis
  pub/sub, GitHub API ingestion, WebSocket fanout. Cheap goroutines,
  predictable latency.
- **Python** for ML: every relevant library (XGBoost, LightGBM, PyTorch,
  Optuna) is best-in-class here, and ad-hoc analysis in `ml/notebooks/`
  shares the same environment as the service.

The boundary is intentional and narrow: api-gateway calls `ml-service`
over HTTP (JSON in / out). No shared code, no shared types — only the
DB schema and the error envelope as contracts.
