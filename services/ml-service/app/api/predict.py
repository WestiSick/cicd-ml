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
    dataset."""
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
        WHERE j.id = ANY(:ids)
    """
    with db.connection(dsn) as conn:
        return pd.read_sql(text(sql), conn, params={"ids": ids})
