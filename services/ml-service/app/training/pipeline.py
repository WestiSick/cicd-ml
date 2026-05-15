"""End-to-end training pipeline.

The single entry point — `train_one` — pulls jobs from Postgres, builds
features with the schema fitted on the train split, fits the chosen
model, and persists everything: model artifact in MODELS_DIR + a row in
`models` + a batch of predictions for the test slice.

Why include test-slice predictions: it lets the simulator (and the
frontend's predicted-vs-actual scatter) work immediately after a fresh
training, without a separate /predict call.
"""
from __future__ import annotations

import logging
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import numpy as np

from ..features.build import FEATURE_VERSION, fit_schema, time_based_split, transform
from ..models.lgbm import factory_by_algo
from ..storage import db

log = logging.getLogger(__name__)


@dataclass
class TrainRequest:
    algo: str
    params: dict[str, Any]
    repo_ids: list[int] | None
    since: str | None
    name: str
    training_job_id: int | None
    activate: bool


@dataclass
class TrainOutcome:
    model_id: int
    metrics: dict[str, float]
    train_size: int
    test_size: int
    feature_importance: dict[str, float]
    artifact_path: str


def train_one(dsn: str, models_dir: Path, req: TrainRequest) -> TrainOutcome:
    """Synchronous training — small datasets, no need for async/job
    streaming yet. Called from the FastAPI handler directly or via the
    api-gateway's bg_jobs worker (which provides a training_job_id).
    """
    df = db.load_jobs_df(dsn, repo_ids=req.repo_ids, since=req.since)
    if len(df) < 10:
        raise InsufficientDataError(
            f"only {len(df)} usable jobs in the chosen window — at least 10 are required."
        )

    train_idx, test_idx = time_based_split(df, train_frac=0.8)
    if len(test_idx) == 0:
        raise InsufficientDataError("time-based split produced empty test set; widen the window.")

    train_df = df.loc[train_idx].copy()
    test_df  = df.loc[test_idx].copy()

    # Fit schema on train only — otherwise the test set's unseen categories
    # would silently sneak into the vocabulary.
    schema = fit_schema(train_df)

    X_train, names, y_train = transform(train_df, schema)
    X_test,  _,     y_test  = transform(test_df, schema)

    if y_train is None or y_test is None:
        raise InsufficientDataError("could not derive target — duration_sec missing on training rows.")

    model = factory_by_algo(req.algo)

    # Per-iteration metric streaming. Only applies to boosted models —
    # Linear/RF fit in one shot, no per-iter signal available. The
    # callback is attached via params so we don't have to change the
    # BaseModel.fit signature.
    if req.training_job_id and req.training_job_id > 0:
        req.params = dict(req.params or {})
        req.params["_streaming_training_job_id"] = req.training_job_id

    result = model.fit(X_train, y_train, X_test, y_test, req.params, names, schema)

    # For non-boosted algorithms (Linear, RF) the boosted callbacks above
    # never fire — push one terminal point so the live chart shows
    # something instead of staying blank. Iteration=1 to keep the X axis
    # in a known range; rmse/mae taken from final metrics.
    if req.training_job_id and req.training_job_id > 0 and req.algo in ("linear", "rf"):
        from .metrics_stream import post_metric
        post_metric(
            training_job_id=req.training_job_id,
            iteration=1,
            train_loss=float(result.metrics.get("mae_train_sec", 0)),
            val_mae=float(result.metrics.get("mae_test_sec", 0)),
            val_rmse=float(result.metrics.get("rmse_test_sec", 0)),
            val_mape=float(result.metrics.get("mape_test", 0)),
        )

    # Persist artifact.
    ts = datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    rel_path = f"{req.algo}-{ts}.joblib"
    artifact = models_dir / rel_path
    model.save(artifact)

    # Strip internal flags from params before persisting — they shouldn't
    # surface on /api/models.
    persisted_params = {
        k: v for k, v in (req.params or {}).items() if not k.startswith("_")
    }

    # Cap stored importance at top 100 features — keeps the JSONB blob
    # readable in the UI and small in storage. The full vector is
    # recoverable from the artifact if ever needed (e.g. for SHAP analysis).
    top_importance = dict(
        sorted(
            (result.feature_importance or {}).items(),
            key=lambda kv: kv[1],
            reverse=True,
        )[:100]
    )

    # Persist model row.
    model_id = db.insert_model_row(
        dsn,
        name=req.name,
        algo=req.algo,
        params=persisted_params,
        metrics=result.metrics,
        feature_importance=top_importance,
        artifact_path=rel_path,
        training_job_id=req.training_job_id,
        feature_version=FEATURE_VERSION,
    )

    # Persist predictions for the test slice — feeds the simulator and the
    # /experiments scatter plot without a follow-up /predict call.
    pred_test_log = model.estimator.predict(X_test)
    pred_test = np.expm1(pred_test_log)
    pred_test = np.clip(pred_test, 0.0, None)
    rows = list(zip(test_df["job_id"].astype(int).tolist(), pred_test.astype(float).tolist()))
    db.insert_predictions(dsn, model_id, rows)

    if req.activate:
        db.set_active_model(dsn, model_id)

    return TrainOutcome(
        model_id=model_id,
        metrics=result.metrics,
        train_size=result.train_size,
        test_size=result.test_size,
        feature_importance=result.feature_importance,
        artifact_path=rel_path,
    )


class InsufficientDataError(Exception):
    """Raised by `train_one` when the dataset is too small. Mapped to a
    400 by the HTTP handler so the UI surfaces an actionable message
    ('collect more data') rather than a 500."""
    pass
