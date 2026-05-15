"""Feature engineering for CI/CD job-duration regression.

This module is the single source of truth for what we feed the models.
The dissertation's Chapter 3 references exactly this file's feature list —
keep the docstrings honest if the implementation changes.

Feature groups (`FEATURE_VERSION = 1`):

  - Time:        hour_of_day, day_of_week, is_weekend
  - Categorical: workflow_name, job_name, head_branch, event, repo, runner
                 (one-hot encoded with vocab pinned at fit time)
  - Numeric:     steps_count, log_repo_avg_30d
  - Branch class: branch_is_main, branch_is_release, branch_is_feature

What we don't have yet (deliberate cut for the baseline):
  - Commit diff features (no `commits` data collected yet)
  - Rolling per-job-name historical stats (next iteration)
  - Author historical stats (needs a longer time window than we have)

Target: log(duration_sec + 1). Log-transform stabilises variance — CI
durations are heavily right-skewed (most under 1 min, long tail to hours).
"""
from __future__ import annotations

from dataclasses import dataclass
import math
from typing import Any

import numpy as np
import pandas as pd

FEATURE_VERSION = 1

# Top-K trick: rather than one-hot-encoding hundreds of unique workflow_names
# (some appear once), we keep the K most frequent values per column and
# bucket the rest as "__other__". Keeps the feature matrix narrow and
# avoids exploding test-set categories that didn't exist at train time.
TOP_K_PER_COLUMN: dict[str, int] = {
    "workflow_name": 20,
    "job_name":      40,
    "head_branch":   15,
    "event":         5,
    "repo_owner":    10,
    "repo_name":     20,
    "runner_name":   10,
}


@dataclass
class FeatureSchema:
    """Vocabulary pinned at fit time, reused at predict time.

    Serialised alongside the model artifact (joblib) so predict-time
    transformations match exactly what training saw. The model would
    happily produce numbers either way — pinning the schema is what
    makes those numbers meaningful.
    """
    version: int
    categories: dict[str, list[str]]   # column → ordered list of allowed values (incl. "__other__")
    numeric_columns: list[str]          # numerics included after one-hots

    def feature_names(self) -> list[str]:
        out: list[str] = []
        for col, vals in self.categories.items():
            for v in vals:
                out.append(f"{col}={v}")
        out.extend(self.numeric_columns)
        return out

    def as_dict(self) -> dict[str, Any]:
        return {
            "version": self.version,
            "categories": self.categories,
            "numeric_columns": list(self.numeric_columns),
        }

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "FeatureSchema":
        return cls(
            version=int(d["version"]),
            categories={k: list(v) for k, v in d["categories"].items()},
            numeric_columns=list(d["numeric_columns"]),
        )


# ---- Building the schema from raw rows ---------------------------------


def fit_schema(df: pd.DataFrame) -> FeatureSchema:
    """Computes a FeatureSchema from a training dataframe.

    Numeric columns are fixed (we don't auto-discover them — the dissertation
    needs a stable feature list across runs). Categorical vocabularies use
    value_counts to keep only top-K, plus a synthetic "__other__" bucket.
    """
    enriched = _enrich(df)

    categories: dict[str, list[str]] = {}
    for col, top_k in TOP_K_PER_COLUMN.items():
        if col not in enriched.columns:
            continue
        vc = enriched[col].fillna("__missing__").astype(str).value_counts()
        top = list(vc.head(top_k).index)
        if "__missing__" in top:
            top.remove("__missing__")
        # Always include __missing__ and __other__ so predict-time can route
        # unseen values without raising.
        categories[col] = top + ["__missing__", "__other__"]

    numeric_columns = [
        "steps_count",
        "log_repo_avg_30d",
        "hour_of_day",
        "day_of_week",
        "is_weekend",
        "branch_is_main",
        "branch_is_release",
        "branch_is_feature",
    ]
    return FeatureSchema(version=FEATURE_VERSION, categories=categories, numeric_columns=numeric_columns)


# ---- Transformations ---------------------------------------------------


def _enrich(df: pd.DataFrame) -> pd.DataFrame:
    """Add derived columns the schema/transform rely on.

    Computed twice: once at fit and once at predict, with identical logic.
    Keeping it in one function avoids skew (predict-time bug class: forgot
    to apply the same scaler).
    """
    out = df.copy()

    # Time features from run_created_at.
    if "run_created_at" in out.columns:
        ts = pd.to_datetime(out["run_created_at"], utc=True, errors="coerce")
        out["hour_of_day"] = ts.dt.hour.fillna(0).astype(int)
        out["day_of_week"] = ts.dt.dayofweek.fillna(0).astype(int)
        out["is_weekend"] = (out["day_of_week"] >= 5).astype(int)
    else:
        out["hour_of_day"] = 0
        out["day_of_week"] = 0
        out["is_weekend"] = 0

    # Branch class — coarse signal that turns out to dominate in practice.
    branch = out.get("head_branch", pd.Series(dtype=str)).fillna("").astype(str).str.lower()
    out["branch_is_main"]    = ((branch == "main") | (branch == "master")).astype(int)
    out["branch_is_release"] = branch.str.startswith("release").astype(int)
    out["branch_is_feature"] = branch.str.startswith(("feat", "feature", "dev")).astype(int)

    # Per-repo 30-day average duration — cheap proxy for "is this repo slow?"
    # We compute it from the dataframe itself: at fit time it's a 30d
    # rolling average; at predict time the caller can pass a precomputed
    # value via a `log_repo_avg_30d` column on the input row.
    if "log_repo_avg_30d" not in out.columns:
        if "repo_id" in out.columns and "run_created_at" in out.columns:
            ts = pd.to_datetime(out["run_created_at"], utc=True, errors="coerce")
            tmp = out.assign(_ts=ts).sort_values("_ts")
            # Simple cumulative repo-mean. Strict rolling-30d would require
            # a time-based index per repo; the cheap version below correlates
            # with it strongly enough for a baseline.
            tmp["log_repo_avg_30d"] = tmp.groupby("repo_id")["duration_sec"].transform(
                lambda s: np.log1p(s.expanding(min_periods=1).mean())
            )
            out["log_repo_avg_30d"] = tmp.sort_index()["log_repo_avg_30d"].fillna(0).values
        else:
            out["log_repo_avg_30d"] = 0.0

    if "steps_count" in out.columns:
        out["steps_count"] = pd.to_numeric(out["steps_count"], errors="coerce").fillna(0).astype(int)
    else:
        out["steps_count"] = 0

    return out


def transform(df: pd.DataFrame, schema: FeatureSchema) -> tuple[np.ndarray, list[str], np.ndarray | None]:
    """Build the feature matrix for a dataframe using a pinned schema.

    Returns (X, feature_names, y_or_None). `y` is included when
    `duration_sec` is present so the same call site handles both train
    and inference paths.
    """
    enriched = _enrich(df)

    blocks: list[np.ndarray] = []
    names: list[str] = []

    for col, vals in schema.categories.items():
        if col not in enriched.columns:
            # Missing column at predict time: all-zero block, except the
            # __missing__ bucket which still fires.
            series = pd.Series(["__missing__"] * len(enriched), index=enriched.index)
        else:
            series = enriched[col].fillna("__missing__").astype(str)
        # Route unseen → "__other__"
        allowed = set(vals)
        routed = series.where(series.isin(allowed), other="__other__")
        # One-hot
        for v in vals:
            blocks.append((routed == v).astype(np.float32).to_numpy().reshape(-1, 1))
            names.append(f"{col}={v}")

    for col in schema.numeric_columns:
        if col in enriched.columns:
            arr = pd.to_numeric(enriched[col], errors="coerce").fillna(0).astype(np.float32).to_numpy()
        else:
            arr = np.zeros(len(enriched), dtype=np.float32)
        blocks.append(arr.reshape(-1, 1))
        names.append(col)

    X = np.hstack(blocks) if blocks else np.zeros((len(enriched), 0), dtype=np.float32)

    y = None
    if "duration_sec" in enriched.columns:
        # Log-transform the target. Predictions are inverse-transformed back
        # to seconds before being returned to callers.
        y = np.log1p(pd.to_numeric(enriched["duration_sec"], errors="coerce").fillna(0).astype(np.float32).to_numpy())

    return X.astype(np.float32), names, y


def inverse_target(y_log: np.ndarray) -> np.ndarray:
    """expm1 is the exact inverse of log1p — and clamps to 0 if numerics
    accidentally go negative for some weird model output."""
    out = np.expm1(y_log)
    return np.clip(out, a_min=0.0, a_max=None)


# ---- Splits ------------------------------------------------------------


def time_based_split(df: pd.DataFrame, train_frac: float = 0.8) -> tuple[pd.Index, pd.Index]:
    """Index split by `run_created_at` quantile.

    Time-based splits are non-negotiable for CI duration: the data has
    week-on-week drift (toolchain changes, new tests) and any random shuffle
    would leak future information into training. The thesis Chapter 3
    cites this choice explicitly.
    """
    if "run_created_at" not in df.columns or len(df) < 5:
        # Fallback for tiny datasets — keep the order, just split positionally.
        n = max(1, int(math.floor(len(df) * train_frac)))
        return df.index[:n], df.index[n:]
    ts = pd.to_datetime(df["run_created_at"], utc=True, errors="coerce")
    cutoff = ts.quantile(train_frac)
    train_idx = df.index[ts <= cutoff]
    test_idx = df.index[ts > cutoff]
    if len(test_idx) == 0 or len(train_idx) == 0:
        n = max(1, int(math.floor(len(df) * train_frac)))
        return df.index[:n], df.index[n:]
    return train_idx, test_idx
