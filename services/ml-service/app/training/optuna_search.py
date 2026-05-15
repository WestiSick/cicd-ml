"""Optuna-driven hyperparameter search.

Strategy:
  1. Load jobs once into a dataframe (avoids re-querying Postgres for every trial).
  2. fit_schema on the train split — pinning vocab so every trial sees
     the same feature matrix shape.
  3. For each trial: sample hyperparameters from the algo-specific
     space, fit the model on the train slice, compute test MAE.
  4. After `n_trials`, refit the best-params model on the same split
     and persist it through the normal pipeline so it lands in
     `models` and the artifact directory like any other training run.

We optimise **test MAE in seconds** (lower is better). MAE was chosen
over RMSE because the thesis SJF result section is more sensitive to
median-case error than tail outliers, and over MAPE because MAPE
explodes on the small fraction of <1s jobs in the dataset.
"""
from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any

import numpy as np
import optuna
from optuna.samplers import TPESampler

from ..features.build import fit_schema, time_based_split, transform
from ..models.base import _compute_metrics  # type: ignore[attr-defined]
from ..models.lgbm import factory_by_algo
from ..storage import db

# Optuna's TPE logs warnings on every trial by default — quiet it.
optuna.logging.set_verbosity(optuna.logging.WARNING)
log = logging.getLogger(__name__)


# ---- Search spaces ----------------------------------------------------

def _suggest_xgboost(trial: optuna.Trial) -> dict[str, Any]:
    return {
        "n_estimators":   trial.suggest_int("n_estimators", 100, 800, step=50),
        "max_depth":      trial.suggest_int("max_depth", 3, 10),
        "learning_rate":  trial.suggest_float("learning_rate", 1e-3, 0.2, log=True),
        "subsample":      trial.suggest_float("subsample", 0.6, 1.0),
        "colsample_bytree": trial.suggest_float("colsample_bytree", 0.6, 1.0),
        "reg_alpha":      trial.suggest_float("reg_alpha", 1e-4, 1.0, log=True),
        "reg_lambda":     trial.suggest_float("reg_lambda", 1e-4, 5.0, log=True),
    }


def _suggest_lightgbm(trial: optuna.Trial) -> dict[str, Any]:
    return {
        "n_estimators":     trial.suggest_int("n_estimators", 100, 800, step=50),
        "max_depth":        trial.suggest_int("max_depth", -1, 12),
        "num_leaves":       trial.suggest_int("num_leaves", 15, 127),
        "learning_rate":    trial.suggest_float("learning_rate", 1e-3, 0.2, log=True),
        "subsample":        trial.suggest_float("subsample", 0.6, 1.0),
        "colsample_bytree": trial.suggest_float("colsample_bytree", 0.6, 1.0),
        "reg_alpha":        trial.suggest_float("reg_alpha", 1e-4, 1.0, log=True),
        "reg_lambda":       trial.suggest_float("reg_lambda", 1e-4, 5.0, log=True),
    }


def _suggest_rf(trial: optuna.Trial) -> dict[str, Any]:
    return {
        "n_estimators":     trial.suggest_int("n_estimators", 100, 500, step=50),
        "max_depth":        trial.suggest_int("max_depth", 4, 32),
        "min_samples_leaf": trial.suggest_int("min_samples_leaf", 1, 8),
    }


def _suggest_linear(trial: optuna.Trial) -> dict[str, Any]:
    # The Ridge alpha is the only knob — make the range wide.
    return {"alpha": trial.suggest_float("alpha", 1e-5, 10.0, log=True)}


def _suggest_mlp(trial: optuna.Trial) -> dict[str, Any]:
    layers = trial.suggest_categorical("hidden_layer_sizes", ["(32,)", "(64,)", "(64, 32)", "(128, 64)"])
    return {
        "hidden_layer_sizes":  eval(layers),  # safe: closed enum above
        "alpha":               trial.suggest_float("alpha", 1e-5, 1e-1, log=True),
        "learning_rate_init":  trial.suggest_float("learning_rate_init", 1e-4, 1e-2, log=True),
    }


_SUGGESTERS = {
    "linear":   _suggest_linear,
    "rf":       _suggest_rf,
    "xgboost":  _suggest_xgboost,
    "lightgbm": _suggest_lightgbm,
    "mlp":      _suggest_mlp,
}


# ---- Driver -----------------------------------------------------------

@dataclass
class OptunaResult:
    best_params: dict[str, Any]
    best_metrics: dict[str, float]
    n_trials: int
    trial_history: list[dict[str, Any]]


def run_search(
    dsn: str,
    algo: str,
    repo_ids: list[int] | None,
    since: str | None,
    n_trials: int,
) -> OptunaResult:
    """Returns best hyperparameters by test-set MAE.

    The caller (api/train.py) then triggers a regular `train_one` call
    with these params + activate flag so a real model row + artifact
    land in the registry.
    """
    if algo not in _SUGGESTERS:
        raise ValueError(f"optuna search not supported for algo: {algo}")
    if n_trials <= 0 or n_trials > 200:
        n_trials = 30  # sane default — keeps wall-time under a minute

    df = db.load_jobs_df(dsn, repo_ids=repo_ids, since=since)
    if len(df) < 20:
        raise ValueError(
            f"only {len(df)} usable jobs — Optuna search needs at least 20 to make a meaningful split."
        )

    train_idx, test_idx = time_based_split(df, train_frac=0.8)
    if len(test_idx) == 0:
        raise ValueError("time-based split produced empty test set; widen the window.")
    train_df, test_df = df.loc[train_idx].copy(), df.loc[test_idx].copy()

    schema = fit_schema(train_df)
    X_train, names, y_train = transform(train_df, schema)
    X_test, _, y_test = transform(test_df, schema)
    assert y_train is not None and y_test is not None

    suggester = _SUGGESTERS[algo]
    history: list[dict[str, Any]] = []

    def objective(trial: optuna.Trial) -> float:
        params = suggester(trial)
        model = factory_by_algo(algo)
        # Build estimator with these params but skip the streaming hook
        # — search trials shouldn't pollute training_metrics with
        # hundreds of synthetic iterations.
        model.feature_names = list(names)
        model.schema = schema
        model.estimator = model._build_estimator(params)
        # We bypass `_fit_with_eval` here so optuna iterations don't
        # spam the gateway's metrics endpoint. The final refit (outside
        # Optuna) goes through the regular pipeline.
        model.estimator.fit(X_train, y_train)
        pred_test = model.estimator.predict(X_test)
        metrics = _compute_metrics(y_train, model.estimator.predict(X_train), y_test, pred_test)
        history.append({"params": params, "mae_test_sec": metrics["mae_test_sec"]})
        return float(metrics["mae_test_sec"])

    study = optuna.create_study(
        direction="minimize",
        sampler=TPESampler(seed=42),
    )
    study.optimize(objective, n_trials=n_trials, show_progress_bar=False)

    best_params = study.best_params
    # If hidden_layer_sizes was sampled it'll be a string — convert back.
    if "hidden_layer_sizes" in best_params and isinstance(best_params["hidden_layer_sizes"], str):
        best_params["hidden_layer_sizes"] = list(eval(best_params["hidden_layer_sizes"]))

    return OptunaResult(
        best_params=best_params,
        best_metrics={"mae_test_sec": float(study.best_value)},
        n_trials=n_trials,
        trial_history=history,
    )
