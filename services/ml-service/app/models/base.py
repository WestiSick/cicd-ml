"""BaseModel — the regression contract every algorithm in this project
implements.

Why a hand-rolled interface (not sklearn's BaseEstimator):
  - We need deterministic save/load with a bundled FeatureSchema; sklearn's
    pickle doesn't carry our schema by itself.
  - We compute metrics in our own canonical shape (MAE/RMSE/MAPE/R²/
    Spearman) for the dissertation tables — wrapping sklearn's scorers
    would just add a layer.
  - `fit` returns a result object instead of `self` so the worker can
    persist metrics + feature importance in one call.

Subclasses live in this package and MUST:
  1. Set `algo` to the canonical string used in DB (`linear`, `rf`, ...).
  2. Implement `_fit_sklearn` returning a fitted sklearn-like estimator.
  3. Implement `feature_importance` returning a dict[feature → score]
     or {} if the algorithm doesn't expose one.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

import joblib
import numpy as np
from scipy.stats import spearmanr
from sklearn.metrics import mean_absolute_error, mean_squared_error, r2_score

from ..features.build import FeatureSchema, inverse_target


@dataclass
class TrainResult:
    metrics: dict[str, float]                       # canonical metrics — see _compute_metrics
    feature_importance: dict[str, float] = field(default_factory=dict)
    train_size: int = 0
    test_size: int = 0


class BaseModel:
    algo: str = ""   # subclasses override

    def __init__(self) -> None:
        self.estimator: Any = None
        self.schema: FeatureSchema | None = None
        self.feature_names: list[str] = []

    # ---- abstract ----------------------------------------------------

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        raise NotImplementedError

    def feature_importance(self) -> dict[str, float]:
        """Default — no importance available. Tree/boosted subclasses
        override this."""
        return {}

    def _fit_with_eval(
        self,
        X_train: np.ndarray,
        y_train: np.ndarray,
        X_test: np.ndarray,
        y_test: np.ndarray,
        params: dict[str, Any],
    ) -> None:
        """Default fit — single-shot, no per-iteration callbacks.
        Boosted subclasses override this to pass eval_set + callbacks
        so the training page can stream loss curves live.

        X_test/y_test go unused in the default — they're available so
        subclasses don't need to plumb them separately.
        """
        del X_test, y_test, params  # explicitly unused here
        self.estimator.fit(X_train, y_train)

    # ---- shared lifecycle --------------------------------------------

    def fit(
        self,
        X_train: np.ndarray,
        y_train: np.ndarray,
        X_test: np.ndarray,
        y_test: np.ndarray,
        params: dict[str, Any],
        feature_names: list[str],
        schema: FeatureSchema,
    ) -> TrainResult:
        self.feature_names = list(feature_names)
        self.schema = schema
        self.estimator = self._build_estimator(params)
        # Subclasses opt into per-iteration callbacks by overriding
        # `_fit_with_eval`. Default just calls plain fit().
        self._fit_with_eval(X_train, y_train, X_test, y_test, params)

        pred_train_log = self.estimator.predict(X_train)
        pred_test_log  = self.estimator.predict(X_test)

        metrics = _compute_metrics(y_train, pred_train_log, y_test, pred_test_log)
        return TrainResult(
            metrics=metrics,
            feature_importance=self.feature_importance(),
            train_size=int(len(y_train)),
            test_size=int(len(y_test)),
        )

    def predict_sec(self, X: np.ndarray) -> np.ndarray:
        """Predict in seconds. Training is in log-space — the inverse
        transform lives here so callers never see log values."""
        if self.estimator is None:
            raise RuntimeError("model not fitted or not loaded")
        return inverse_target(self.estimator.predict(X))

    # ---- persistence -------------------------------------------------

    def save(self, path: Path) -> None:
        if self.estimator is None or self.schema is None:
            raise RuntimeError("nothing to save — fit or load first")
        payload = {
            "algo": self.algo,
            "estimator": self.estimator,
            "schema": self.schema.as_dict(),
            "feature_names": self.feature_names,
        }
        path.parent.mkdir(parents=True, exist_ok=True)
        joblib.dump(payload, path)

    @classmethod
    def load(cls, path: Path) -> "BaseModel":
        """Reconstructs the right concrete class from the saved `algo`
        marker. Callers don't have to know which subclass produced an
        artifact — the registry knows."""
        payload = joblib.load(path)
        algo = payload["algo"]
        # Lazy import to avoid module-load-time costs of heavy backends.
        if algo == "linear":
            from .linear import LinearModel
            inst: BaseModel = LinearModel()
        elif algo == "rf":
            from .rf import RandomForestModel
            inst = RandomForestModel()
        elif algo == "xgboost":
            from .xgb import XGBoostModel
            inst = XGBoostModel()
        elif algo == "lightgbm":
            from .lgbm import LightGBMModel
            inst = LightGBMModel()
        elif algo == "mlp":
            from .mlp import MLPModel
            inst = MLPModel()
        else:
            raise ValueError(f"unknown algo in artifact: {algo}")
        inst.estimator = payload["estimator"]
        inst.schema = FeatureSchema.from_dict(payload["schema"])
        inst.feature_names = list(payload["feature_names"])
        return inst


# ---- canonical metrics ---------------------------------------------------


def _compute_metrics(
    y_train_log: np.ndarray,
    pred_train_log: np.ndarray,
    y_test_log: np.ndarray,
    pred_test_log: np.ndarray,
) -> dict[str, float]:
    """All metrics are computed in the original seconds space, not log
    space — they're what the dissertation reports, and what the user
    intuitively understands. Log-space metrics would mislead about
    "1 second error is fine for a 1-hour job".

    The Spearman rank coefficient is critical: SJF cares about ordering,
    not absolute error. A model with MAPE=30% but Spearman=0.95 still
    produces excellent SJF rankings; one with MAPE=5% but Spearman=0.6
    might not.
    """
    y_train     = inverse_target(y_train_log)
    pred_train  = inverse_target(pred_train_log)
    y_test      = inverse_target(y_test_log)
    pred_test   = inverse_target(pred_test_log)

    def _safe(fn, *args, default=float("nan")):
        try:
            return float(fn(*args))
        except Exception:
            return default

    mae_train  = _safe(mean_absolute_error, y_train, pred_train)
    mae_test   = _safe(mean_absolute_error, y_test, pred_test)
    rmse_test  = float(np.sqrt(_safe(mean_squared_error, y_test, pred_test, default=float("nan"))))

    mape_test = float("nan")
    if len(y_test) > 0:
        denom = np.where(y_test == 0, 1.0, y_test)
        mape_test = float(np.mean(np.abs((y_test - pred_test) / denom)))

    r2_test = _safe(r2_score, y_test, pred_test)

    spearman = float("nan")
    if len(y_test) >= 5:
        try:
            rho, _ = spearmanr(y_test, pred_test)
            spearman = float(rho) if not np.isnan(rho) else float("nan")
        except Exception:
            pass

    return {
        "mae_train_sec": mae_train,
        "mae_test_sec":  mae_test,
        "rmse_test_sec": rmse_test,
        "mape_test":     mape_test,
        "r2_test":       r2_test,
        "spearman_test": spearman,
    }
