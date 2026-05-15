-- +goose Up
-- +goose StatementBegin

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Repositories tracked by the system.
CREATE TABLE repos (
    id              BIGSERIAL PRIMARY KEY,
    owner           TEXT NOT NULL,
    name            TEXT NOT NULL,
    github_id       BIGINT,
    default_branch  TEXT,
    tracked_branches TEXT[] NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'idle',  -- idle | fetching | synced | error | paused
    last_synced_at  TIMESTAMPTZ,
    oldest_run_at   TIMESTAMPTZ,
    newest_run_at   TIMESTAMPTZ,
    runs_count      BIGINT NOT NULL DEFAULT 0,
    jobs_count      BIGINT NOT NULL DEFAULT 0,
    last_error      TEXT,
    is_seed         BOOLEAN NOT NULL DEFAULT FALSE,
    added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);

CREATE INDEX idx_repos_status ON repos(status);

-- Raw workflow runs from GitHub.
CREATE TABLE workflow_runs (
    id               BIGSERIAL PRIMARY KEY,
    repo_id          BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    run_id           BIGINT NOT NULL,         -- GitHub run id
    workflow_name    TEXT,
    head_branch      TEXT,
    head_sha         TEXT NOT NULL,
    event            TEXT,
    status           TEXT,
    conclusion       TEXT,
    actor            TEXT,
    created_at       TIMESTAMPTZ NOT NULL,
    run_started_at   TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ,
    UNIQUE (repo_id, run_id)
);

CREATE INDEX idx_runs_repo_created ON workflow_runs(repo_id, created_at DESC);
CREATE INDEX idx_runs_head_sha ON workflow_runs(head_sha);

-- Individual job within a run.
CREATE TABLE jobs (
    id              BIGSERIAL PRIMARY KEY,
    run_id          BIGINT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    github_job_id   BIGINT,
    name            TEXT NOT NULL,
    status          TEXT,
    conclusion      TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    duration_sec    INTEGER,                  -- actual duration in seconds (NULL if not finished)
    runner_name     TEXT,
    runner_group    TEXT,
    labels          TEXT[] NOT NULL DEFAULT '{}',
    steps_count     INTEGER,
    UNIQUE (run_id, name, github_job_id)
);

CREATE INDEX idx_jobs_run ON jobs(run_id);
CREATE INDEX idx_jobs_name ON jobs(name);
CREATE INDEX idx_jobs_completed_at ON jobs(completed_at DESC NULLS LAST);

-- Commits referenced by runs (for diff features).
CREATE TABLE commits (
    sha             TEXT PRIMARY KEY,
    repo_id         BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    author          TEXT,
    message         TEXT,
    files_changed   INTEGER,
    additions       INTEGER,
    deletions       INTEGER,
    committed_at    TIMESTAMPTZ
);

CREATE INDEX idx_commits_repo ON commits(repo_id);

-- Materialized features per job.
CREATE TABLE features (
    job_id           BIGINT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    feature_vector   JSONB NOT NULL,
    feature_version  INTEGER NOT NULL DEFAULT 1,
    computed_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Trained ML models.
CREATE TABLE models (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT NOT NULL,
    algo             TEXT NOT NULL,           -- linear | rf | xgboost | lightgbm | mlp | lstm
    params           JSONB NOT NULL DEFAULT '{}',
    metrics          JSONB NOT NULL DEFAULT '{}',
    artifact_path    TEXT,                    -- relative to MODELS_DIR
    training_job_id  BIGINT,                  -- ref to bg_jobs.id
    train_window     JSONB,                   -- {start, end, repos: [...]}
    feature_version  INTEGER NOT NULL DEFAULT 1,
    is_active        BOOLEAN NOT NULL DEFAULT FALSE,
    trained_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_models_one_active ON models(is_active) WHERE is_active = TRUE;

-- Predictions: one per (job, model).
CREATE TABLE predictions (
    job_id           BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    model_id         BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    predicted_sec    DOUBLE PRECISION NOT NULL,
    made_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, model_id)
);

CREATE INDEX idx_predictions_job ON predictions(job_id);

-- Scheduler queue state (one row per (job, strategy) decision).
CREATE TABLE queue_state (
    id              BIGSERIAL PRIMARY KEY,
    job_id          BIGINT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    strategy        TEXT NOT NULL,            -- fifo | sjf | edf | custom
    score           DOUBLE PRECISION,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    dequeued_at     TIMESTAMPTZ,
    UNIQUE (job_id, strategy)
);

-- Strategy simulation runs (replay over a window).
CREATE TABLE sim_runs (
    id                  BIGSERIAL PRIMARY KEY,
    strategy            TEXT NOT NULL,
    window_start        TIMESTAMPTZ NOT NULL,
    window_end          TIMESTAMPTZ NOT NULL,
    repos               BIGINT[] NOT NULL DEFAULT '{}',
    jobs_count          INTEGER NOT NULL,
    makespan_sec        DOUBLE PRECISION,
    wait_p50_sec        DOUBLE PRECISION,
    wait_p95_sec        DOUBLE PRECISION,
    throughput_per_min  DOUBLE PRECISION,
    sla_violations      INTEGER,
    extra               JSONB NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Background job log — the single source of truth for the UI progress feed.
CREATE TABLE bg_jobs (
    id           BIGSERIAL PRIMARY KEY,
    kind         TEXT NOT NULL,    -- collect_history | compute_features | train_model | simulate | refresh | bootstrap
    payload      JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'queued', -- queued | running | done | failed | cancelled
    progress     INTEGER NOT NULL DEFAULT 0,
    total        INTEGER NOT NULL DEFAULT 0,
    message      TEXT,
    logs_tail    TEXT,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);

CREATE INDEX idx_bg_jobs_status_created ON bg_jobs(status, created_at DESC);
CREATE INDEX idx_bg_jobs_kind ON bg_jobs(kind);

-- Per-iteration metrics for live training charts.
CREATE TABLE training_metrics (
    training_job_id  BIGINT NOT NULL REFERENCES bg_jobs(id) ON DELETE CASCADE,
    iteration        INTEGER NOT NULL,
    train_loss       DOUBLE PRECISION,
    val_mae          DOUBLE PRECISION,
    val_rmse         DOUBLE PRECISION,
    val_mape         DOUBLE PRECISION,
    ts               TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (training_job_id, iteration)
);

-- Webhook event log (for /admin → Webhooks debugging).
CREATE TABLE webhook_events (
    id            BIGSERIAL PRIMARY KEY,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivery_id   TEXT,
    event_type    TEXT,
    repo          TEXT,
    hmac_valid    BOOLEAN,
    payload       JSONB,
    error         TEXT
);

CREATE INDEX idx_webhook_received ON webhook_events(received_at DESC);

-- Activity log — user-visible action history.
CREATE TABLE activity_log (
    id           BIGSERIAL PRIMARY KEY,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor        TEXT,
    action       TEXT NOT NULL,
    target       TEXT,
    success      BOOLEAN NOT NULL,
    message      TEXT,
    details      JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_activity_at ON activity_log(at DESC);

-- Single-row settings table keyed by string.
CREATE TABLE system_state (
    key    TEXT PRIMARY KEY,
    value  JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO system_state (key, value) VALUES
    ('bootstrap_done', 'false'::jsonb),
    ('active_strategy', '"sjf"'::jsonb),
    ('custom_weights', '{"short_job": 0.6, "deadline": 0.3, "branch": 0.1}'::jsonb);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS system_state, activity_log, webhook_events, training_metrics, bg_jobs,
                     sim_runs, queue_state, predictions, models, features, commits,
                     jobs, workflow_runs, repos CASCADE;
-- +goose StatementEnd
