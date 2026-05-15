"""LightGBM — the second gradient-boosting baseline.

Symmetric in role to XGBoost (Chapter 4 reports both). LightGBM is
typically faster on dense numeric data and handles high-cardinality
categoricals natively, but our pipeline pre-one-hots so the practical
difference is mostly speed.
"""
from __future__ import annotations

from typing import Any

import numpy as np
from lightgbm import LGBMRegressor

from ..training.metrics_stream import post_metric
from .base import BaseModel


class LightGBMModel(BaseModel):
    algo = "lightgbm"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        return LGBMRegressor(
            n_estimators=int(params.get("n_estimators", 400)),
            max_depth=int(params.get("max_depth", -1)),
            learning_rate=float(params.get("learning_rate", 0.05)),
            num_leaves=int(params.get("num_leaves", 31)),
            subsample=float(params.get("subsample", 0.8)),
            colsample_bytree=float(params.get("colsample_bytree", 0.8)),
            reg_alpha=float(params.get("reg_alpha", 0.0)),
            reg_lambda=float(params.get("reg_lambda", 0.0)),
            objective="regression",
            # Without an explicit `metric` the sklearn LGBM API silently
            # skips per-iteration evals on eval_set, leaving the live
            # chart flat. We request rmse (curve) and l1 (= MAE, also
            # logged for the UI's MAE pane).
            metric=["rmse", "l1"],
            n_jobs=int(params.get("n_jobs", -1)),
            random_state=int(params.get("random_state", 42)),
            verbosity=-1,
        )

    def _fit_with_eval(self, X_train, y_train, X_test, y_test, params):
        """LightGBM streams `l2` (squared loss) per iteration when given
        an eval_set. We post the post-fit curve in one batch — keeps the
        wire chat simple and a 400-iteration model is only 400 HTTP
        calls anyway.
        """
        import logging
        _log = logging.getLogger(__name__)
        self.estimator.fit(
            X_train, y_train,
            eval_set=[(X_train, y_train), (X_test, y_test)],
            eval_names=["training", "validation"],
        )
        evals_dbg = getattr(self.estimator, "evals_result_", None)
        if evals_dbg:
            _log.warning("LGBM evals_result_ structure: keys=%s sample=%s",
                         list(evals_dbg.keys()),
                         {k: {mk: (mv[:3] if isinstance(mv, list) else mv) for mk, mv in v.items()} for k, v in evals_dbg.items()})
        job_id = int(params.get("_streaming_training_job_id", 0))
        if job_id <= 0:
            return
        evals = getattr(self.estimator, "evals_result_", None)
        if not evals:
            return
        # LightGBM names the train eval_set entry `training` and the
        # second one `valid_1` when both are supplied via fit's
        # `eval_set`. We read RMSE for the loss curve and L1 (= MAE)
        # for the MAE chart.
        train_curve = evals.get("training", {}) or evals.get("valid_0", {})
        val_curve = evals.get("validation", {}) or evals.get("valid_1", {}) or evals.get("valid_0", {})

        def _pick(curve: dict, *names: str) -> list:
            for n in names:
                if n in curve:
                    return curve[n]
            return []

        train_rmse = _pick(train_curve, "rmse", "l2", "regression")
        val_rmse   = _pick(val_curve,   "rmse", "l2", "regression")
        val_mae    = _pick(val_curve,   "l1",   "mae")
        n = min(len(train_rmse), len(val_rmse))
        for i in range(n):
            tr = float(train_rmse[i])
            va = float(val_rmse[i])
            mae = float(val_mae[i]) if i < len(val_mae) else 0.0
            post_metric(
                training_job_id=job_id,
                iteration=i + 1,
                train_loss=tr,
                val_rmse=va,
                val_mae=mae,
            )

    def feature_importance(self) -> dict[str, float]:
        if self.estimator is None:
            return {}
        try:
            importances = np.asarray(self.estimator.feature_importances_).ravel()
        except Exception:
            return {}
        if len(self.feature_names) != len(importances):
            return {}
        return {n: float(v) for n, v in zip(self.feature_names, importances)}


def factory_by_algo(algo: str):
    """Helper used by the training pipeline to instantiate the right
    class from a string. Kept here so all algos register through one map.
    """
    from .linear import LinearModel
    from .mlp import MLPModel
    from .rf import RandomForestModel
    from .xgb import XGBoostModel
    table = {
        "linear":   LinearModel,
        "rf":       RandomForestModel,
        "xgboost":  XGBoostModel,
        "lightgbm": LightGBMModel,
        "mlp":      MLPModel,
    }
    cls = table.get(algo)
    if cls is None:
        raise ValueError(f"unknown algo: {algo}")
    return cls()
