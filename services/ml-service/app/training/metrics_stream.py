"""Per-iteration metric streaming back to api-gateway.

Why HTTP and not direct DB writes:
  - The api-gateway owns the WebSocket fan-out (`/ws/training/:id`).
    Routing through it means UI gets the event the same moment the row
    lands in Postgres.
  - One canonical write path simplifies reasoning about ordering — no
    chance of two processes both writing iter=N with conflicting values.

The hook is intentionally fire-and-forget: if the gateway is briefly
unreachable, training continues and the loss curve will simply have
gaps. That's the right trade-off for visualisation — never block a
training run on a transient network blip.
"""
from __future__ import annotations

import logging
import os
from typing import Callable

import httpx

log = logging.getLogger(__name__)

# Same internal compose network → DNS name `api`. In prod the gateway
# also serves us; we never route through Traefik for internal calls.
_GATEWAY_BASE = os.getenv("GATEWAY_INTERNAL_BASE", "http://api:8080")

# Shared async-ish HTTP client. Short timeout — we don't want to block
# the training loop. httpx is sync here on purpose: the training pipeline
# is itself sync, and threading per-call would complicate things.
_HTTP = httpx.Client(timeout=2.0)


def post_metric(
    training_job_id: int,
    iteration: int,
    train_loss: float = 0.0,
    val_mae: float = 0.0,
    val_rmse: float = 0.0,
    val_mape: float = 0.0,
) -> None:
    """Fire-and-forget POST to api-gateway. Swallows network errors —
    metric loss is acceptable; halting training would not be."""
    if training_job_id <= 0:
        return  # not part of a tracked bg_job (e.g. ad-hoc /train call)
    try:
        _HTTP.post(
            f"{_GATEWAY_BASE}/api/internal/training/{training_job_id}/metric",
            json={
                "iteration": iteration,
                "train_loss": train_loss,
                "val_mae": val_mae,
                "val_rmse": val_rmse,
                "val_mape": val_mape,
            },
        )
    except Exception as e:  # noqa: BLE001  intentionally broad
        log.debug("metric stream skipped: %s", e)


def make_xgb_lgbm_callback(training_job_id: int) -> Callable | None:
    """Returns a callback object compatible with xgboost/lightgbm `eval_set`
    callbacks. None if streaming is disabled."""
    if training_job_id <= 0:
        return None

    def _cb(env) -> None:
        # XGBoost callback receives a CallbackEnv; LightGBM gives a
        # similar tuple-like object. Both expose .iteration and an
        # evaluation_result_list of (data_name, metric_name, value, _).
        try:
            iteration = int(getattr(env, "iteration", 0))
            results = getattr(env, "evaluation_result_list", []) or []
            val_mae = 0.0
            val_rmse = 0.0
            train_loss = 0.0
            for r in results:
                # Each r is like ("train", "rmse", 1.234, _)
                if not r or len(r) < 3:
                    continue
                ds, name, val = r[0], r[1], r[2]
                name = str(name).lower()
                ds = str(ds).lower()
                if ds.startswith("train") and "rmse" in name:
                    train_loss = float(val)
                if ds.startswith(("valid", "eval", "test")):
                    if "rmse" in name:
                        val_rmse = float(val)
                    if "mae" in name or "l1" in name:
                        val_mae = float(val)
            post_metric(
                training_job_id=training_job_id,
                iteration=iteration,
                train_loss=train_loss,
                val_mae=val_mae,
                val_rmse=val_rmse,
            )
        except Exception as e:  # noqa: BLE001
            log.debug("metric callback skipped: %s", e)

    return _cb
