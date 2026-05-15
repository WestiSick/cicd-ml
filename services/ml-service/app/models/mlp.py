"""MLP — multi-layer perceptron baseline via sklearn.

We deliberately use `sklearn.neural_network.MLPRegressor` rather than
PyTorch for the baseline thesis comparison. Reasons:

  - The dataset (hundreds of jobs) is far below the threshold where a
    PyTorch loop would pay back its overhead and 600 MB of CUDA wheels.
  - sklearn's MLP shares the BaseModel interface with the other algos —
    same fit/predict/save path, same metrics, same artifact format.
  - The thesis hypothesis is that gradient boosting wins on tabular CI
    data; MLP is here as the "deep learning baseline" for completeness,
    not as a contender. sklearn's implementation is sufficient to
    demonstrate that.

If a future iteration needs LSTM (which DOES want PyTorch for proper
sequence handling), add `torch` to requirements then and create a
sibling lstm.py — the import will be lazy so non-LSTM trainings
remain cheap.
"""
from __future__ import annotations

from typing import Any

import numpy as np
from sklearn.neural_network import MLPRegressor

from ..training.metrics_stream import post_metric
from .base import BaseModel


class MLPModel(BaseModel):
    algo = "mlp"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        # Defaults chosen for a ~500-row CI dataset:
        #   - two hidden layers, narrowing — stops the model from
        #     memorising training noise on such a small set.
        #   - adam + early_stopping — robust against the cold-start
        #     hyperparameter problem we want to demonstrate exists.
        #   - max_iter capped at 300; with early stopping it usually
        #     converges in 50–150.
        return MLPRegressor(
            hidden_layer_sizes=tuple(params.get("hidden_layer_sizes", (64, 32))),
            activation=params.get("activation", "relu"),
            solver=params.get("solver", "adam"),
            alpha=float(params.get("alpha", 1e-3)),
            learning_rate_init=float(params.get("learning_rate_init", 1e-3)),
            max_iter=int(params.get("max_iter", 300)),
            early_stopping=bool(params.get("early_stopping", True)),
            n_iter_no_change=int(params.get("n_iter_no_change", 20)),
            validation_fraction=float(params.get("validation_fraction", 0.1)),
            random_state=int(params.get("random_state", 42)),
            verbose=False,
        )

    def _fit_with_eval(self, X_train, y_train, X_test, y_test, params):
        """sklearn's MLP exposes `loss_curve_` after fit — we replay it
        as iteration metrics so the UI gets a real loss curve, just like
        XGBoost/LightGBM.
        """
        self.estimator.fit(X_train, y_train)
        job_id = int(params.get("_streaming_training_job_id", 0))
        if job_id <= 0:
            return
        loss_curve = getattr(self.estimator, "loss_curve_", None) or []
        # loss_curve_ is in the model's loss space (squared error on the
        # log target). To give the UI an approximate sqrt-of-loss as
        # "train RMSE", convert.
        for i, loss in enumerate(loss_curve):
            try:
                tr = float(loss) ** 0.5
            except Exception:
                continue
            post_metric(
                training_job_id=job_id,
                iteration=i + 1,
                train_loss=tr,
            )

    def feature_importance(self) -> dict[str, float]:
        """MLPs don't expose a built-in feature importance signal. We
        return an empty dict — the model row's `feature_importance`
        column simply stays `{}`, and the UI shows the empty state on
        that pane. Permutation importance is on the roadmap but adds
        ~1s × n_features compute per model and isn't critical for the
        thesis baseline."""
        return {}
