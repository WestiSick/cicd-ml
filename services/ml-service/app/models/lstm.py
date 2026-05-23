"""LSTM — temporal-dependency baseline (PyTorch).

The dissertation's Chapter 4 includes LSTM specifically to demonstrate that
*temporal* models add value on rolling features that capture how the same
(repo, job_name) pair evolves over time. The hypothesis going in: with
strong rolling features (`log_jobname_median_*` already in the feature
vector) the marginal lift of LSTM over LightGBM should be small — confirming
that classical boosting is sufficient for this scale of data.

Architectural decisions:
  - Two stacked LSTM cells with hidden_size=64. Anything bigger overfits
    on hundreds-to-thousands of rows.
  - Single timestep input. The "sequence" dimension is just the feature
    vector (the same row LightGBM sees). LSTM here is acting as a
    fancy MLP — the per-(repo, job) temporal pattern is already encoded
    in the rolling features rather than in a true sequence input. A future
    iteration could build true sequences by grouping per (repo, job_name)
    and feeding the last K observations, but that requires a much larger
    dataset to avoid one-row groups.
  - Adam + MSE on log-duration. Same target as every other model so
    metrics are comparable.
  - CPU-only torch — we pin the +cpu wheel in requirements.txt to avoid
    the 2 GB CUDA download. With our row counts there's nothing to
    accelerate.

Streaming per-epoch metrics: we hook into the same `post_metric` channel
the boosted models use, so the /experiments/jobs/:id live chart shows the
loss curve in real time.
"""
from __future__ import annotations

from typing import Any

import numpy as np

from ..training.metrics_stream import post_metric
from .base import BaseModel


# Module-level lazy import — keeps non-LSTM trainings cheap and the
# FastAPI startup time fast (torch import is ~1s).
def _load_torch():
    import torch
    import torch.nn as nn
    return torch, nn


class LSTMModel(BaseModel):
    algo = "lstm"

    def _build_estimator(self, params: dict[str, Any]) -> Any:
        # We don't construct the nn.Module here — torch isn't imported
        # yet at registration time. The real estimator gets built inside
        # `_fit_with_eval`. The BaseModel.fit contract just expects
        # something to assign to self.estimator before predict is called.
        return _LSTMShim(
            hidden_size=int(params.get("hidden_size", 64)),
            num_layers=int(params.get("num_layers", 2)),
            dropout=float(params.get("dropout", 0.2)),
            lr=float(params.get("lr", 0.005)),
            epochs=int(params.get("epochs", 50)),
            batch_size=int(params.get("batch_size", 64)),
            seed=int(params.get("random_state", 42)),
        )

    def _fit_with_eval(self, X_train, y_train, X_test, y_test, params):
        # The shim does its own torch setup so we don't drag torch into
        # the module-level import graph. Streaming happens inside.
        job_id = int(params.get("_streaming_training_job_id", 0))
        self.estimator.fit(X_train, y_train, X_test, y_test, streaming_job_id=job_id)

    def feature_importance(self) -> dict[str, float]:
        # Neural nets don't ship coefficients in a directly interpretable
        # form. We could compute permutation importance but that's a
        # heavy add (re-predict N times); not worth it for a baseline
        # model that's there mostly as a comparison point.
        return {}


class _LSTMShim:
    """Wraps the torch model + training loop + predict path so the
    BaseModel contract (save/load via joblib) doesn't need torch in
    scope at the persistence layer.

    The shim's `predict` signature mirrors sklearn estimators
    (`predict(X) -> np.ndarray`). joblib serialises the shim including
    the state_dict bytes, so reloading on a fresh container brings the
    weights back without needing the original torch graph.
    """

    def __init__(
        self,
        hidden_size: int,
        num_layers: int,
        dropout: float,
        lr: float,
        epochs: int,
        batch_size: int,
        seed: int,
    ):
        self.hidden_size = hidden_size
        self.num_layers = num_layers
        self.dropout = dropout
        self.lr = lr
        self.epochs = epochs
        self.batch_size = batch_size
        self.seed = seed
        self._input_size: int | None = None
        # State persisted to disk via joblib. _net_state holds the
        # serialised torch.state_dict bytes; the live nn.Module is
        # rebuilt lazily on predict so the persistence layer never
        # touches torch internals directly.
        self._net_state: bytes | None = None

    def fit(self, X_train, y_train, X_test, y_test, streaming_job_id: int = 0):
        torch, nn = _load_torch()
        torch.manual_seed(self.seed)
        np.random.seed(self.seed)

        self._input_size = int(X_train.shape[1])
        net = _build_net(torch, nn, self._input_size, self.hidden_size, self.num_layers, self.dropout)
        opt = torch.optim.Adam(net.parameters(), lr=self.lr)
        loss_fn = nn.MSELoss()

        Xt = torch.from_numpy(np.asarray(X_train, dtype=np.float32))
        yt = torch.from_numpy(np.asarray(y_train, dtype=np.float32)).unsqueeze(-1)
        Xv = torch.from_numpy(np.asarray(X_test, dtype=np.float32))
        yv = torch.from_numpy(np.asarray(y_test, dtype=np.float32)).unsqueeze(-1)

        n = Xt.shape[0]
        idx = np.arange(n)

        for ep in range(self.epochs):
            net.train()
            np.random.shuffle(idx)
            running_loss = 0.0
            steps = 0
            for start in range(0, n, self.batch_size):
                end = min(start + self.batch_size, n)
                batch_idx = idx[start:end]
                xb = Xt[batch_idx].unsqueeze(1)  # (B, 1, F) — sequence-of-1
                yb = yt[batch_idx]
                opt.zero_grad()
                pred = net(xb)
                loss = loss_fn(pred, yb)
                loss.backward()
                opt.step()
                running_loss += float(loss.item())
                steps += 1
            train_loss = running_loss / max(1, steps)

            net.eval()
            with torch.no_grad():
                val_pred_log = net(Xv.unsqueeze(1)).squeeze(-1).numpy()
                val_true_log = yv.squeeze(-1).numpy()
            # Metrics computed in seconds-space for live chart consistency.
            val_pred = np.expm1(val_pred_log).clip(min=0)
            val_true = np.expm1(val_true_log).clip(min=0)
            val_rmse = float(np.sqrt(np.mean((val_pred - val_true) ** 2)))
            val_mae  = float(np.mean(np.abs(val_pred - val_true)))

            if streaming_job_id > 0:
                post_metric(
                    training_job_id=streaming_job_id,
                    iteration=ep + 1,
                    train_loss=train_loss,
                    val_rmse=val_rmse,
                    val_mae=val_mae,
                )

        # Serialise weights for joblib persistence.
        import io
        buf = io.BytesIO()
        torch.save(net.state_dict(), buf)
        self._net_state = buf.getvalue()

    def predict(self, X) -> np.ndarray:
        torch, nn = _load_torch()
        if self._input_size is None or self._net_state is None:
            raise RuntimeError("LSTM model not trained — call fit() first")
        net = _build_net(torch, nn, self._input_size, self.hidden_size, self.num_layers, self.dropout)
        import io
        net.load_state_dict(torch.load(io.BytesIO(self._net_state), weights_only=True))
        net.eval()
        Xt = torch.from_numpy(np.asarray(X, dtype=np.float32)).unsqueeze(1)
        with torch.no_grad():
            return net(Xt).squeeze(-1).numpy()


def _build_net(torch, nn, input_size: int, hidden_size: int, num_layers: int, dropout: float):
    """Two stacked LSTM cells → linear regression head. Module-scoped
    construction so save/load can rebuild from hyperparameters + state_dict
    without needing a pickled nn.Module reference."""

    class _Net(nn.Module):
        def __init__(self):
            super().__init__()
            self.lstm = nn.LSTM(
                input_size=input_size,
                hidden_size=hidden_size,
                num_layers=num_layers,
                dropout=dropout if num_layers > 1 else 0.0,
                batch_first=True,
            )
            self.head = nn.Linear(hidden_size, 1)

        def forward(self, x):
            # x: (B, 1, F) — single-step sequence
            out, _ = self.lstm(x)
            last = out[:, -1, :]
            return self.head(last)

    return _Net()
