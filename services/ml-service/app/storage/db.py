"""SQLAlchemy engine + thin loaders.

The ml-service reads the same Postgres the api-gateway writes to. We use
SQLAlchemy + pandas.read_sql for ergonomics — a Linear regression baseline
on 200 jobs doesn't need a hand-tuned cursor. For million-row windows we'd
switch to chunked reads; until then keep it simple.
"""
from __future__ import annotations

from contextlib import contextmanager
from typing import Any, Iterator

import pandas as pd
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

_ENGINE: Engine | None = None


def get_engine(dsn: str) -> Engine:
    """Lazy-singleton engine. ml-service is single-process so one engine
    instance is fine; FastAPI's threadpool uses the connection pool."""
    global _ENGINE
    if _ENGINE is None:
        _ENGINE = create_engine(dsn, pool_size=4, max_overflow=4, future=True)
    return _ENGINE


@contextmanager
def connection(dsn: str) -> Iterator[Any]:
    eng = get_engine(dsn)
    with eng.connect() as conn:
        yield conn


def load_jobs_df(dsn: str, repo_ids: list[int] | None = None, since: str | None = None) -> pd.DataFrame:
    """Return one row per completed job, joined with run + repo info.

    Filtered to jobs with non-null duration_sec — training is meaningless
    without a target. The optional repo_ids/since let callers slice for
    train/test splits or per-experiment subsets.
    """
    sql = """
        SELECT
            j.id              AS job_id,
            j.name            AS job_name,
            j.duration_sec    AS duration_sec,
            j.runner_name     AS runner_name,
            j.runner_group    AS runner_group,
            j.steps_count     AS steps_count,
            j.labels          AS labels,
            j.started_at      AS started_at,
            w.id              AS run_id,
            w.workflow_name   AS workflow_name,
            w.head_branch     AS head_branch,
            w.event           AS event,
            w.actor           AS actor,
            w.created_at      AS run_created_at,
            r.id              AS repo_id,
            r.owner           AS repo_owner,
            r.name            AS repo_name
        FROM jobs j
        JOIN workflow_runs w ON j.run_id = w.id
        JOIN repos r         ON w.repo_id = r.id
        WHERE j.duration_sec IS NOT NULL
          AND j.duration_sec > 0
    """
    params: dict[str, Any] = {}
    if repo_ids:
        sql += " AND r.id = ANY(:repo_ids)"
        params["repo_ids"] = repo_ids
    if since:
        sql += " AND w.created_at >= :since"
        params["since"] = since
    sql += " ORDER BY w.created_at ASC"

    with connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn, params=params)
    return df


def fetch_active_model_row(dsn: str) -> dict[str, Any] | None:
    """Returns the currently-active model row or None if no model is active."""
    sql = """
        SELECT id, name, algo, params, metrics, artifact_path, feature_version
        FROM models WHERE is_active = TRUE LIMIT 1
    """
    with connection(dsn) as conn:
        row = conn.execute(text(sql)).mappings().first()
    return dict(row) if row else None


def list_models_rows(dsn: str) -> list[dict[str, Any]]:
    sql = """
        SELECT id, name, algo, params, metrics, artifact_path,
               training_job_id, feature_version, is_active, trained_at
        FROM models ORDER BY trained_at DESC
    """
    with connection(dsn) as conn:
        rows = conn.execute(text(sql)).mappings().all()
    return [dict(r) for r in rows]


def insert_model_row(
    dsn: str,
    *,
    name: str,
    algo: str,
    params: dict[str, Any],
    metrics: dict[str, Any],
    feature_importance: dict[str, float],
    artifact_path: str,
    training_job_id: int | None,
    feature_version: int,
) -> int:
    sql = """
        INSERT INTO models (name, algo, params, metrics, feature_importance,
                            artifact_path, training_job_id, feature_version, is_active)
        VALUES (:name, :algo, CAST(:params AS jsonb), CAST(:metrics AS jsonb),
                CAST(:feature_importance AS jsonb),
                :artifact_path, :training_job_id, :feature_version, FALSE)
        RETURNING id
    """
    import json as _json

    with connection(dsn) as conn:
        with conn.begin():
            mid = conn.execute(
                text(sql),
                {
                    "name": name,
                    "algo": algo,
                    "params": _json.dumps(params),
                    "metrics": _json.dumps(metrics),
                    "feature_importance": _json.dumps(feature_importance),
                    "artifact_path": artifact_path,
                    "training_job_id": training_job_id,
                    "feature_version": feature_version,
                },
            ).scalar_one()
    return int(mid)


def set_active_model(dsn: str, model_id: int) -> None:
    """Atomically flip the `is_active` flag — the partial unique index in
    the schema enforces "at most one active model" so we deactivate first
    then activate inside a transaction."""
    with connection(dsn) as conn:
        with conn.begin():
            conn.execute(text("UPDATE models SET is_active = FALSE WHERE is_active = TRUE"))
            conn.execute(text("UPDATE models SET is_active = TRUE WHERE id = :id"), {"id": model_id})


def insert_predictions(dsn: str, model_id: int, rows: list[tuple[int, float]]) -> int:
    """Bulk-upsert predictions. Returns the number of rows written.

    Conflict resolution: (job_id, model_id) is the PK, so re-running
    /predict for the same set with the same model just refreshes
    predicted_sec — useful when the feature pipeline changes.
    """
    if not rows:
        return 0
    sql = """
        INSERT INTO predictions (job_id, model_id, predicted_sec)
        VALUES (:job_id, :model_id, :predicted_sec)
        ON CONFLICT (job_id, model_id) DO UPDATE
          SET predicted_sec = EXCLUDED.predicted_sec,
              made_at = now()
    """
    with connection(dsn) as conn:
        with conn.begin():
            conn.execute(
                text(sql),
                [{"job_id": j, "model_id": model_id, "predicted_sec": p} for j, p in rows],
            )
    return len(rows)
