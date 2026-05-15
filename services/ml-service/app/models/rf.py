"""Random Forest — the non-linear baseline.

The thesis compares Linear vs RF vs gradient boosting; RF is the "tree
model, no fancy tricks" baseline that disambiguates "did we get a lift
from non-linearity?" (RF beats Linear) from "did we get a lift from
boosting specifically?" (XGBoost beats RF).
"""
from __future__ import annotations

from typing import Any

import numpy as np
from sklearn.ensemble import RandomForestRegressor

from .base import BaseModel


class RandomForestModel(BaseModel):
    algo = "rf"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        return RandomForestRegressor(
            n_estimators=int(params.get("n_estimators", 200)),
            max_depth=params.get("max_depth", None),
            min_samples_leaf=int(params.get("min_samples_leaf", 2)),
            n_jobs=int(params.get("n_jobs", -1)),
            random_state=int(params.get("random_state", 42)),
        )

    def feature_importance(self) -> dict[str, float]:
        if self.estimator is None or not hasattr(self.estimator, "feature_importances_"):
            return {}
        importances = np.asarray(self.estimator.feature_importances_).ravel()
        if len(self.feature_names) != len(importances):
            return {}
        return {n: float(v) for n, v in zip(self.feature_names, importances)}
