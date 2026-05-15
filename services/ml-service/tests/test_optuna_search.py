"""Unit-test the Optuna search space wiring without hitting the DB.

We exercise the suggester functions directly with a stub trial — that
catches typos in parameter names that would otherwise surface only on
a long end-to-end training run.
"""
from __future__ import annotations

from typing import Any

import pytest

from app.training.optuna_search import (
    _suggest_linear,
    _suggest_lightgbm,
    _suggest_mlp,
    _suggest_rf,
    _suggest_xgboost,
)


class _StubTrial:
    """Returns the mid-point of every suggestion range — deterministic
    and minimal. Just enough to verify the suggester function shape."""

    def suggest_int(self, name: str, lo: int, hi: int, step: int = 1) -> int:
        return (lo + hi) // 2

    def suggest_float(self, name: str, lo: float, hi: float, log: bool = False) -> float:
        return (lo + hi) / 2

    def suggest_categorical(self, name: str, choices: list[Any]) -> Any:
        return choices[0]


@pytest.mark.parametrize(
    "suggester,expected_keys",
    [
        (_suggest_linear,   {"alpha"}),
        (_suggest_rf,       {"n_estimators", "max_depth", "min_samples_leaf"}),
        (_suggest_xgboost,  {"n_estimators", "max_depth", "learning_rate", "subsample", "colsample_bytree", "reg_alpha", "reg_lambda"}),
        (_suggest_lightgbm, {"n_estimators", "max_depth", "num_leaves", "learning_rate", "subsample", "colsample_bytree", "reg_alpha", "reg_lambda"}),
        (_suggest_mlp,      {"hidden_layer_sizes", "alpha", "learning_rate_init"}),
    ],
)
def test_suggester_returns_expected_keys(suggester, expected_keys):
    params = suggester(_StubTrial())
    assert set(params.keys()) == expected_keys
    for k, v in params.items():
        assert v is not None, f"{k} got None"
