"""Linear regression baseline.

Why: it's the worst-case sanity check. If a more sophisticated model
isn't beating Linear by a meaningful margin on the held-out test set,
something is wrong with either the features or the data — and the
thesis Chapter 4 makes that comparison explicit.
"""
from __future__ import annotations

from typing import Any

import numpy as np
from sklearn.linear_model import Ridge

from .base import BaseModel


class LinearModel(BaseModel):
    algo = "linear"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        # Ridge with a small L2 — pure LinearRegression on one-hot matrices
        # blows up on rare-category collinearity. The alpha is small enough
        # that the model is effectively "linear regression with a safety
        # net", which is what the thesis comparison wants.
        alpha = float(params.get("alpha", 1e-3))
        return Ridge(alpha=alpha, fit_intercept=True)

    def feature_importance(self) -> dict[str, float]:
        if self.estimator is None or not hasattr(self.estimator, "coef_"):
            return {}
        coefs = np.asarray(self.estimator.coef_).ravel()
        if len(self.feature_names) != len(coefs):
            return {}
        # Absolute coefficient magnitude — the standard Linear-model proxy.
        return {n: float(abs(c)) for n, c in zip(self.feature_names, coefs)}
