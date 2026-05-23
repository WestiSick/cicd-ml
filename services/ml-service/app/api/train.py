"""POST /train — runs a training pipeline end-to-end.

Synchronous: even XGBoost on 200 rows takes < 2s. When dataset sizes grow
we'll move this into a worker (the api-gateway already has the bg_jobs
infrastructure to chain it). For the current scale, a direct HTTP call
is cleaner than indirecting through Redis.
"""
from __future__ import annotations

from typing import Any

from fastapi import APIRouter
from pydantic import BaseModel, Field

from ..config import load
from ..training.optuna_search import run_search
from ..training.pipeline import InsufficientDataError, TrainRequest, cross_validate, train_one
from . import errors

router = APIRouter()


class TrainBody(BaseModel):
    algo: str = Field(..., description="linear | rf | xgboost | lightgbm")
    params: dict[str, Any] = Field(default_factory=dict)
    repo_ids: list[int] | None = None
    since: str | None = None
    name: str | None = None
    training_job_id: int | None = None
    activate: bool = False
    # error_weighted: tier-2 continual learning. Up-weights training rows
    # where the previous model produced a high relative error so the new
    # fit pays disproportionate attention to slices it currently gets
    # wrong. Pairs with the webhook-time per-(repo, workflow) calibration
    # (tier 1) for a two-layer "learn from mistakes" system.
    error_weighted: bool = False
    error_weight_alpha: float = Field(1.0, ge=0.0, le=5.0)


@router.post("/")
async def start_training(body: TrainBody) -> dict[str, Any]:
    s = load()
    name = body.name or f"{body.algo}-default"
    req = TrainRequest(
        algo=body.algo,
        params=body.params,
        repo_ids=body.repo_ids,
        since=body.since,
        name=name,
        training_job_id=body.training_job_id,
        activate=body.activate,
        error_weighted=body.error_weighted,
        error_weight_alpha=body.error_weight_alpha,
    )
    try:
        out = train_one(s.postgres_dsn, s.models_dir, req)
    except InsufficientDataError as e:
        # 422 — semantically richer than a generic 400. The frontend's
        # error envelope decoder treats both identically.
        raise errors.APIError(
            status=422,
            code="insufficient_data",
            message=str(e),
            user_action="Sync more data on /datasets, or widen the time window.",
        )
    except ValueError as e:
        raise errors.APIError(
            status=400,
            code="invalid_algo",
            message=str(e),
            user_action="Pick one of linear, rf, xgboost, lightgbm.",
        )

    return {
        "model_id": out.model_id,
        "algo": body.algo,
        "name": name,
        "metrics": out.metrics,
        "train_size": out.train_size,
        "test_size": out.test_size,
        "feature_importance": _top_features(out.feature_importance, k=15),
        "artifact_path": out.artifact_path,
    }


def _top_features(d: dict[str, float], k: int) -> list[dict[str, Any]]:
    """Compact representation for the UI — sorted by absolute importance,
    truncated to k. The full importance dict lives in the artifact for
    later inspection."""
    items = sorted(d.items(), key=lambda kv: kv[1], reverse=True)
    return [{"name": n, "value": float(v)} for n, v in items[:k]]


class CVBody(BaseModel):
    """POST /train/cv — walk-forward cross-validation, no persistence.

    The frontend's /experiments page calls this when the user clicks
    'Cross-validate' to estimate generalisation before committing to a
    full training run. Response shape mirrors POST /train but with
    per-fold + mean + std metric blocks instead of one set.
    """
    algo: str = Field(..., description="linear | rf | xgboost | lightgbm | mlp")
    params: dict[str, Any] = Field(default_factory=dict)
    repo_ids: list[int] | None = None
    since: str | None = None
    n_splits: int = Field(5, ge=2, le=10)


@router.post("/cv")
async def cross_validate_endpoint(body: CVBody) -> dict[str, Any]:
    s = load()
    req = TrainRequest(
        algo=body.algo,
        params=body.params,
        repo_ids=body.repo_ids,
        since=body.since,
        name=f"{body.algo}-cv",
        training_job_id=None,
        activate=False,
    )
    try:
        out = cross_validate(s.postgres_dsn, req, n_splits=body.n_splits)
    except InsufficientDataError as e:
        raise errors.APIError(
            status=422,
            code="insufficient_data",
            message=str(e),
            user_action="Sync more data on /datasets, lower n_splits, or widen the time window.",
        )
    except ValueError as e:
        raise errors.APIError(
            status=400,
            code="invalid_algo",
            message=str(e),
            user_action="Pick one of linear, rf, xgboost, lightgbm, mlp.",
        )

    return {
        "algo":             out.algo,
        "n_splits":         out.n_splits,
        "fold_metrics":     out.fold_metrics,
        "mean_metrics":     out.mean_metrics,
        "std_metrics":      out.std_metrics,
        "total_train_size": out.total_train_size,
        "total_test_size":  out.total_test_size,
    }


class OptunaBody(BaseModel):
    algo: str = Field(..., description="linear | rf | xgboost | lightgbm | mlp")
    n_trials: int = Field(30, ge=2, le=200)
    repo_ids: list[int] | None = None
    since: str | None = None
    name: str | None = None
    training_job_id: int | None = None
    activate: bool = False
    error_weighted: bool = False
    error_weight_alpha: float = Field(1.0, ge=0.0, le=5.0)


@router.post("/optuna")
async def start_optuna_search(body: OptunaBody) -> dict[str, Any]:
    """Run Optuna over the requested algo, then refit with best params
    through the regular pipeline so the resulting model lands in
    `models` like any other training.
    """
    s = load()
    try:
        search = run_search(
            dsn=s.postgres_dsn,
            algo=body.algo,
            repo_ids=body.repo_ids,
            since=body.since,
            n_trials=body.n_trials,
        )
    except ValueError as e:
        # Insufficient data, unknown algo, etc.
        raise errors.APIError(
            status=422,
            code="optuna_unavailable",
            message=str(e),
            user_action="Lower n_trials or sync more data on /datasets.",
        )

    name = body.name or f"{body.algo}-optuna-{body.n_trials}"
    req = TrainRequest(
        algo=body.algo,
        params=search.best_params,
        repo_ids=body.repo_ids,
        since=body.since,
        name=name,
        training_job_id=body.training_job_id,
        activate=body.activate,
        error_weighted=body.error_weighted,
        error_weight_alpha=body.error_weight_alpha,
    )
    try:
        out = train_one(s.postgres_dsn, s.models_dir, req)
    except InsufficientDataError as e:
        raise errors.APIError(
            status=422, code="insufficient_data", message=str(e),
            user_action="Sync more data on /datasets, or widen the time window.",
        )

    return {
        "model_id":     out.model_id,
        "algo":         body.algo,
        "name":         name,
        "n_trials":     search.n_trials,
        "best_params":  search.best_params,
        "best_metrics": search.best_metrics,
        "metrics":      out.metrics,
        "train_size":   out.train_size,
        "test_size":    out.test_size,
        "feature_importance": _top_features(out.feature_importance, k=15),
        "artifact_path": out.artifact_path,
        # Trim history — the full list of all trials is heavy for the UI.
        # Top 5 is enough for a "what did Optuna find" summary.
        "trial_history": sorted(search.trial_history, key=lambda t: t["mae_test_sec"])[:5],
    }
