"""XGBoost — the expected best-performer on tabular CI data.

The thesis hypothesis Chapter 3 is that gradient boosting on top of our
feature set out-performs both Linear and RF. Chapter 4 then verifies
this empirically.

Hyperparameter defaults are deliberately conservative — picking the
optimum is what `/experiments → Optuna search` is for.
"""
from __future__ import annotations

from typing import Any

import numpy as np
from xgboost import XGBRegressor

from ..training.metrics_stream import post_metric
from .base import BaseModel


class XGBoostModel(BaseModel):
    algo = "xgboost"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        return XGBRegressor(
            n_estimators=int(params.get("n_estimators", 400)),
            max_depth=int(params.get("max_depth", 6)),
            learning_rate=float(params.get("learning_rate", 0.05)),
            subsample=float(params.get("subsample", 0.8)),
            colsample_bytree=float(params.get("colsample_bytree", 0.8)),
            reg_alpha=float(params.get("reg_alpha", 0.0)),
            reg_lambda=float(params.get("reg_lambda", 1.0)),
            objective="reg:squarederror",
            n_jobs=int(params.get("n_jobs", -1)),
            random_state=int(params.get("random_state", 42)),
            verbosity=0,
            tree_method="hist",
        )

    def _fit_with_eval(self, X_train, y_train, X_test, y_test, params):
        """XGBoost fits with eval_set so we can stream val_rmse per iter.

        We don't pass an `xgboost.callback` object — XGBoost's callback
        API has shifted across versions, and a plain post-fit loop over
        the evals_result_ is simpler and version-stable.
        """
        self.estimator.fit(
            X_train, y_train,
            eval_set=[(X_train, y_train), (X_test, y_test)],
            verbose=False,
        )
        job_id = int(params.get("_streaming_training_job_id", 0))
        if job_id <= 0:
            return
        evals = getattr(self.estimator, "evals_result_", None)
        if not evals:
            return
        # XGBoost names datasets validation_0 / validation_1 etc.
        train_curve = evals.get("validation_0", {})
        val_curve = evals.get("validation_1", {})
        train_rmse = train_curve.get("rmse", [])
        val_rmse   = val_curve.get("rmse", [])
        n = min(len(train_rmse), len(val_rmse))
        for i in range(n):
            post_metric(
                training_job_id=job_id,
                iteration=i + 1,
                train_loss=float(train_rmse[i]),
                val_rmse=float(val_rmse[i]),
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
