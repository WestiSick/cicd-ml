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

from ..features.build import FEATURE_VERSION, fit_schema, time_based_cv, time_based_split, transform
from ..models.base import _compute_metrics
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


@dataclass
class CVOutcome:
    """Per-fold metrics + summary across all folds. The summary is what
    the dissertation Chapter 3 reports — point estimates from a single
    train/test split are misleading without CV bounds."""
    algo: str
    n_splits: int
    fold_metrics: list[dict[str, float]]      # one dict per fold
    mean_metrics: dict[str, float]
    std_metrics:  dict[str, float]
    total_train_size: int
    total_test_size:  int


def cross_validate(dsn: str, req: TrainRequest, n_splits: int = 5) -> CVOutcome:
    """Walk-forward cross-validation for one algorithm.

    Each fold fits a fresh model on the prefix of the time-sorted dataset
    and evaluates on the next slice — see `time_based_cv` for the exact
    boundaries. Per-fold metrics are computed with the same canonical
    `_compute_metrics` used by single-split training, so CV and one-shot
    numbers are directly comparable.

    Does NOT persist a model row or predictions: this is an *evaluation*
    call, not a training one. The caller (`POST /train/cv`) returns the
    summary table; if the user likes the numbers they then click "Train"
    to fit the production model with `train_one`.
    """
    df = db.load_jobs_df(dsn, repo_ids=req.repo_ids, since=req.since)
    if len(df) < 25:
        raise InsufficientDataError(
            f"only {len(df)} usable jobs — at least 25 needed for {n_splits}-fold CV."
        )

    folds = time_based_cv(df, n_splits=n_splits)
    per_fold: list[dict[str, float]] = []
    total_train = total_test = 0

    for fi, (train_idx, test_idx) in enumerate(folds):
        train_df = df.loc[train_idx].copy()
        test_df  = df.loc[test_idx].copy()
        # Schema fitted on this fold's train slice only — CV-correct.
        schema = fit_schema(train_df)
        X_tr, names, y_tr = transform(train_df, schema)
        X_te, _,     y_te = transform(test_df, schema)
        if y_tr is None or y_te is None:
            log.warning("fold %d: missing target, skipping", fi)
            continue
        model = factory_by_algo(req.algo)
        # CV-time: no per-iteration streaming — folds are not interesting
        # individually for the live UI; we report only the summary.
        params = dict(req.params or {})
        params.pop("_streaming_training_job_id", None)
        model.fit(X_tr, y_tr, X_te, y_te, params, names, schema)

        pred_tr_log = model.estimator.predict(X_tr)
        pred_te_log = model.estimator.predict(X_te)
        m = _compute_metrics(y_tr, pred_tr_log, y_te, pred_te_log)
        per_fold.append(m)
        total_train += len(train_idx)
        total_test  += len(test_idx)

    if not per_fold:
        raise InsufficientDataError("every CV fold failed — dataset too sparse")

    # mean and std across folds for each metric. NaNs are skipped per
    # metric so a single failing fold doesn't poison the summary.
    keys = sorted({k for fold in per_fold for k in fold.keys()})
    mean: dict[str, float] = {}
    std:  dict[str, float] = {}
    for k in keys:
        vals = [float(f[k]) for f in per_fold if k in f and np.isfinite(f[k])]
        if not vals:
            mean[k] = float("nan")
            std[k]  = float("nan")
        else:
            arr = np.array(vals, dtype=float)
            mean[k] = float(arr.mean())
            std[k]  = float(arr.std(ddof=0))
    return CVOutcome(
        algo=req.algo,
        n_splits=len(per_fold),
        fold_metrics=per_fold,
        mean_metrics=mean,
        std_metrics=std,
        total_train_size=total_train,
        total_test_size=total_test,
    )
