"""POST /predict — score jobs against the active model.

Two call modes:
  1. `job_ids: [..]` — load those rows, transform, predict, write back
     into `predictions`. This is what the api-gateway webhook handler
     calls when a new GitHub workflow_run is announced.
  2. `dry_run: true` (without job_ids) — predict over the full dataset
     in chunks, useful for re-scoring after a fresh model is activated
     so the simulator sees up-to-date predictions everywhere.

Returns the predictions inline as well so the api-gateway can broadcast
them on /ws/queue without a follow-up DB read.
"""
from __future__ import annotations

from pathlib import Path
from typing import Any

import numpy as np
import pandas as pd
from fastapi import APIRouter
from pydantic import BaseModel
from sqlalchemy import text

from ..config import load
from ..features.build import transform, FeatureSchema
from ..models.base import BaseModel as MLBaseModel
from ..storage import db
from . import errors

router = APIRouter()


class PredictBody(BaseModel):
    job_ids: list[int] | None = None
    dry_run: bool = False


class PredictFromPayloadBody(BaseModel):
    """Webhook-time prediction. Used by api-gateway's GitHub webhook
    handler when the run doesn't yet exist in the DB.

    Fields mirror what the workflow_run payload carries; missing values
    are routed to the schema's __missing__ bucket so prediction still
    works without a full feature row.

    head_sha unlocks the commit-content features (backend/frontend/test
    file counts, is_docs_only). When supplied AND the matching commit
    is already in `commits` + `commit_files` (api-gateway populates this
    synchronously in `ensureCommitForWebhook` before calling us), the
    bucket columns flow into the model. Without it, those features fall
    back to zeros and the prediction degrades to repo/branch averages —
    that's the same behaviour as ml-service v2 had before this fix.
    """
    repo_owner: str
    repo_name: str
    workflow_name: str | None = None
    head_branch: str | None = None
    event: str | None = None
    job_name: str | None = None
    runner_name: str | None = None
    steps_count: int | None = None
    head_sha: str | None = None


@router.post("/")
async def predict(body: PredictBody) -> dict[str, Any]:
    s = load()
    active = db.fetch_active_model_row(s.postgres_dsn)
    if active is None:
        raise errors.APIError(
            status=409,
            code="no_active_model",
            message="No active model — nothing can be predicted.",
            user_action="Train a model on /experiments and click Activate.",
        )

    model = _load_model_for_active(s.models_dir, active)

    if body.job_ids:
        df = _load_jobs_by_ids(s.postgres_dsn, body.job_ids)
    elif body.dry_run:
        df = db.load_jobs_df(s.postgres_dsn)
    else:
        raise errors.APIError(
            status=400,
            code="invalid_request",
            message="Either job_ids or dry_run=true is required.",
            user_action="Pass at least one job id in the request body.",
        )

    if df.empty:
        return {"model_id": int(active["id"]), "predictions": []}

    schema = FeatureSchema.from_dict(active["params"].get("__schema__")) if "__schema__" in (active.get("params") or {}) else model.schema
    # model.schema is the canonical source; the __schema__ fallback is a
    # forward-compat hook for when we start versioning schemas in `params`.
    assert schema is not None
    X, _, _ = transform(df, schema)
    preds_log = model.estimator.predict(X)
    preds = np.expm1(preds_log)
    preds = np.clip(preds, 0.0, None).astype(float)

    rows = list(zip(df["job_id"].astype(int).tolist(), preds.tolist()))
    db.insert_predictions(s.postgres_dsn, int(active["id"]), rows)

    return {
        "model_id": int(active["id"]),
        "model_algo": active["algo"],
        "predictions": [{"job_id": int(j), "predicted_sec": float(p)} for j, p in rows],
    }


@router.post("/from-payload")
async def predict_from_payload(body: PredictFromPayloadBody) -> dict[str, Any]:
    """Webhook-time predict. The api-gateway calls this when GitHub sends a
    workflow_run.requested before any row exists in `jobs` — we still want
    a `predicted_sec` to show on the dashboard immediately, then refine it
    once the run completes and lands in the DB.

    We build a one-row DataFrame, transform via the *active* model's pinned
    schema (unseen categoricals route to __other__, missing to __missing__),
    and return the predicted seconds. No DB write here — the proper
    `predictions` row gets created later by the job_ids path when the jobs
    are persisted by the collector.
    """
    s = load()
    active = db.fetch_active_model_row(s.postgres_dsn)
    if active is None:
        raise errors.APIError(
            status=409,
            code="no_active_model",
            message="No active model — webhook predict cannot run.",
            user_action="Train a model on /experiments and click Activate.",
        )
    model = _load_model_for_active(s.models_dir, active)
    schema = model.schema
    assert schema is not None

    # Use current UTC time for time-based features. The webhook fires at
    # workflow_run.requested, which is when GitHub would normally start
    # scheduling — close enough for our hour_of_day / day_of_week features.
    now = pd.Timestamp.utcnow()

    # hours_since_last_run — cache-temperature signal. At webhook time
    # the new workflow_run isn't yet in the DB, so we look up the most
    # recent run for this repo and measure the delta to now(). Once the
    # collector ingests this run, load_jobs_df will compute it the same
    # way via the LAG() CTE — identical semantics, just one path is
    # batch-SQL and the other is a single-row helper.
    hours_since = db.lookup_hours_since_last_run(s.postgres_dsn, body.repo_owner, body.repo_name)

    df = pd.DataFrame([{
        "repo_owner":            body.repo_owner,
        "repo_name":             body.repo_name,
        "workflow_name":         body.workflow_name,
        "head_branch":           body.head_branch,
        "head_sha":              body.head_sha,
        "event":                 body.event,
        "job_name":              body.job_name,
        "runner_name":           body.runner_name,
        "steps_count":           body.steps_count if body.steps_count is not None else 0,
        "run_created_at":        now,
        "hours_since_last_run":  hours_since,
    }])

    # Attach commit-content features when we have a SHA AND it's already
    # been persisted. api-gateway's webhook handler does a synchronous
    # GetCommit + UpsertCommit + BulkInsertCommitFiles before calling us
    # so the typical happy path finds rows; if it didn't (rate-limited,
    # no PAT), attach_commit_file_features fills zeros and the prediction
    # silently falls back to repo/branch averages.
    if body.head_sha:
        df = db.attach_commit_file_features(df, s.postgres_dsn)

    X, _, _ = transform(df, schema)
    pred_log = model.estimator.predict(X)
    predicted_sec = float(np.clip(np.expm1(pred_log[0]), 0.0, None))

    return {
        "model_id":      int(active["id"]),
        "model_algo":    active["algo"],
        "predicted_sec": predicted_sec,
    }


def _load_model_for_active(models_dir: Path, active: dict[str, Any]) -> MLBaseModel:
    path = models_dir / str(active["artifact_path"])
    if not path.exists():
        raise errors.APIError(
            status=500,
            code="artifact_missing",
            message=f"Active model artifact not found at {path.name}.",
            user_action="Retrain the active model. The model row is intact but the file is gone.",
        )
    return MLBaseModel.load(path)


def _load_jobs_by_ids(dsn: str, ids: list[int]) -> pd.DataFrame:
    """Mirrors load_jobs_df but filters by job_id — needed for the webhook
    path where we have specific jobs and don't want to scan the full
    dataset.

    Schema parity with load_jobs_df is important: the active model was
    trained against load_jobs_df's columns; if we drop a column here
    the transform() at predict time fills it with zeros silently, which
    quietly degrades predictions. Hence the same SELECT list (incl.
    head_sha + commit aggregate cols) and the same attach_commit_file_
    features post-process.
    """
    sql = """
        WITH wr_lag AS (
            SELECT id,
                   EXTRACT(EPOCH FROM (
                     created_at - LAG(created_at) OVER (
                       PARTITION BY repo_id ORDER BY created_at, id
                     )
                   )) / 3600.0 AS hours_since_last_run
            FROM workflow_runs
        )
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
            w.head_sha        AS head_sha,
            w.event           AS event,
            w.actor           AS actor,
            w.created_at      AS run_created_at,
            r.id              AS repo_id,
            r.owner           AS repo_owner,
            r.name            AS repo_name,
            c.files_changed   AS commit_files_changed,
            c.additions       AS commit_additions,
            c.deletions       AS commit_deletions,
            COALESCE(wl.hours_since_last_run, 999.0) AS hours_since_last_run
        FROM jobs j
        JOIN workflow_runs w ON j.run_id = w.id
        JOIN wr_lag wl       ON wl.id = w.id
        JOIN repos r         ON w.repo_id = r.id
        LEFT JOIN commits c  ON w.head_sha = c.sha
        WHERE j.id = ANY(:ids)
    """
    with db.connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn, params={"ids": ids})
    return db.attach_commit_file_features(df, dsn)
