"""Feature engineering for CI/CD job-duration regression.

This module is the single source of truth for what we feed the models.
The dissertation's Chapter 3 references exactly this file's feature list —
keep the docstrings honest if the implementation changes.

Feature groups (`FEATURE_VERSION = 3`):

  - Time:        hour_of_day, day_of_week, is_weekend
  - Categorical: workflow_name, job_name, head_branch, event, repo, runner
                 (one-hot encoded with vocab pinned at fit time)
  - Numeric:     steps_count, log_repo_avg_30d
  - Branch class: branch_is_main, branch_is_release, branch_is_feature
  - Commit diff (aggregate): log_commit_files_changed, log_commit_additions,
                             log_commit_deletions
  - Commit content (v2): per-bucket file counts (backend / frontend / test /
                         docs / config / other), per-bucket LOC, and the
                         flags `commit_is_docs_only` and `commit_has_tests`
  - Rolling per-(repo, job_name): log_jobname_median_7d/30d, jobname_runs_30d
  - Author historical: log_author_p50_30d, log_author_p90_30d, author_commits_30d

Target: log(duration_sec + 1). Log-transform stabilises variance — CI
durations are heavily right-skewed (most under 1 min, long tail to hours).
"""
from __future__ import annotations

from dataclasses import dataclass
import math
from typing import Any

import numpy as np
import pandas as pd

from .file_buckets import BUCKETS as _COMMIT_FILE_BUCKETS

# v1 → v2: per-file commit-content features (backend/frontend/test/docs/
#   config bucket counts + LOC + is_docs_only/has_tests flags).
# v2 → v3: log_hours_since_last_run — gap between this workflow_run and
#   the previous one in the same repo. Captures docker-layer-cache state
#   on self-hosted runners, which dominates the bimodal warm/cold deploy
#   duration that commit-content features alone cannot explain (e.g.
#   thesis Chapter 4 santehlavka case: 30 short 22s deploys + 22 long
#   200s deploys with near-identical commit profiles).
# Old models stay loadable because `feature_version` is stamped per-model
# — the loader uses the stored schema, not this module-level constant.
FEATURE_VERSION = 3

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
        "log_jobname_median_7d",
        "log_jobname_median_30d",
        "jobname_runs_30d",
        "log_author_p50_30d",
        "log_author_p90_30d",
        "author_commits_30d",
        # Commit diff features — aggregate counts populated by the
        # collector. Missing rows fall back to 0 via NaN → 0 coercion
        # in transform().
        "log_commit_files_changed",
        "log_commit_additions",
        "log_commit_deletions",
        # Commit-content features (v2). Per-bucket file counts and LOC
        # let the model distinguish "README-only push" (docs_files=1,
        # is_docs_only=1 → fast pipeline) from "rewrote half the
        # backend" (backend_files=42, backend_loc=2400 → long pipeline).
        # Log-transformed only for LOC because file counts are already
        # bounded (GitHub caps Files at 300/commit).
        *[f"commit_{b}_files" for b in _COMMIT_FILE_BUCKETS],
        *[f"log_commit_{b}_loc" for b in _COMMIT_FILE_BUCKETS],
        "commit_is_docs_only",
        "commit_has_tests",
        # v3: cache-temperature proxy. Long gaps since the last run in
        # this repo strongly correlate with cold docker-layer cache on
        # self-hosted runners → long deploy duration. log1p compresses
        # the heavy right tail (gaps of weeks happen but shouldn't
        # dominate the feature scale).
        "log_hours_since_last_run",
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

    # Rolling per-(repo_id, job_name) medians over 7 and 30 days. The
    # dissertation Chapter 3 cites these as the strongest single-feature
    # predictors after `log_repo_avg_30d` — same job in the same repo is
    # the best baseline for how long that job will take next.
    #
    # Time-aware rolling keyed on run_created_at; per-(repo, job_name)
    # because matrix-different jobs in the same workflow can have very
    # different shapes (e.g. unit-tests vs e2e).
    #
    # At predict time the caller can either pass pre-computed values via
    # `log_jobname_median_7d`/`_30d` columns on the input row, or rely on
    # the schema-routing fallback (zero) — both work, the former gives
    # better predictions.
    needed = {"job_name", "repo_id", "run_created_at", "duration_sec", "job_id"}
    if needed.issubset(out.columns):
        ts = pd.to_datetime(out["run_created_at"], utc=True, errors="coerce")
        work = pd.DataFrame({
            "job_id":       out["job_id"].values,
            "repo_id":      out["repo_id"].values,
            "job_name":     out["job_name"].astype(str).values,
            "_ts":          ts.values,
            "duration_sec": pd.to_numeric(out["duration_sec"], errors="coerce").values,
        }).dropna(subset=["_ts"])
        # closed='left' would prevent the row's own label from leaking
        # into its own feature. Pandas API doesn't accept closed='left'
        # with a time-window directly on a Series — workaround: shift
        # within group before rolling.
        work = work.sort_values("_ts").set_index("_ts")
        grouped = work.groupby(["repo_id", "job_name"], dropna=False)
        med7  = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("7D").median())
        med30 = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("30D").median())
        cnt30 = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("30D").count())
        rolling = pd.DataFrame({
            "log_jobname_median_7d":  np.log1p(med7.fillna(0).values),
            "log_jobname_median_30d": np.log1p(med30.fillna(0).values),
            "jobname_runs_30d":       cnt30.fillna(0).values,
        }, index=work["job_id"].values)
        # Map back into the output frame on job_id (unique).
        joined = out.merge(rolling, how="left", left_on="job_id", right_index=True)
        for c in ("log_jobname_median_7d", "log_jobname_median_30d", "jobname_runs_30d"):
            out[c] = pd.to_numeric(joined[c], errors="coerce").fillna(0).values
    else:
        out["log_jobname_median_7d"]  = 0.0
        out["log_jobname_median_30d"] = 0.0
        out["jobname_runs_30d"]       = 0

    # Author historical stats — rolling 30d p50/p90 of duration plus
    # commit-count per author. The dissertation Chapter 3 lists these
    # alongside the job_name rolling features as the second-strongest
    # cluster of signals after log_repo_avg_30d. Different authors trigger
    # different shapes (e.g. core-team commits hit cached pipelines;
    # bot/dependabot commits trigger full rebuilds).
    #
    # Same shift+rolling pattern as job_name to avoid same-row leakage.
    author_needed = {"actor", "run_created_at", "duration_sec", "job_id"}
    if author_needed.issubset(out.columns):
        ts = pd.to_datetime(out["run_created_at"], utc=True, errors="coerce")
        work = pd.DataFrame({
            "job_id":       out["job_id"].values,
            "actor":        out["actor"].astype(str).values,
            "_ts":          ts.values,
            "duration_sec": pd.to_numeric(out["duration_sec"], errors="coerce").values,
        }).dropna(subset=["_ts"])
        work = work.sort_values("_ts").set_index("_ts")
        grouped = work.groupby("actor", dropna=False)
        p50 = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("30D").median())
        p90 = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("30D").quantile(0.9))
        cnt = grouped["duration_sec"].apply(lambda s: s.shift(1).rolling("30D").count())
        rolling = pd.DataFrame({
            "log_author_p50_30d": np.log1p(p50.fillna(0).values),
            "log_author_p90_30d": np.log1p(p90.fillna(0).values),
            "author_commits_30d": cnt.fillna(0).values,
        }, index=work["job_id"].values)
        joined = out.merge(rolling, how="left", left_on="job_id", right_index=True)
        for c in ("log_author_p50_30d", "log_author_p90_30d", "author_commits_30d"):
            out[c] = pd.to_numeric(joined[c], errors="coerce").fillna(0).values
    else:
        out["log_author_p50_30d"] = 0.0
        out["log_author_p90_30d"] = 0.0
        out["author_commits_30d"] = 0

    # Commit diff features — log-transformed because both `additions`
    # and `deletions` have heavy right tails (one mega-refactor PR with
    # 50k LOC dominates). log1p compresses the tail without hiding
    # ordinary commits in the [0, 1k] range.
    for src, dst in (
        ("commit_files_changed", "log_commit_files_changed"),
        ("commit_additions",     "log_commit_additions"),
        ("commit_deletions",     "log_commit_deletions"),
    ):
        if src in out.columns:
            out[dst] = np.log1p(
                pd.to_numeric(out[src], errors="coerce").fillna(0).clip(lower=0).astype(float)
            )
        else:
            out[dst] = 0.0

    # Per-bucket commit-content features. File counts pass through as-is
    # (already bounded); LOC values are log-transformed for the same
    # right-tail reason as the aggregate `additions`/`deletions` above.
    # Missing columns (no commit_files data for this SHA) fall back to 0.
    for bucket in _COMMIT_FILE_BUCKETS:
        files_col = f"commit_{bucket}_files"
        loc_col   = f"commit_{bucket}_loc"
        log_loc   = f"log_commit_{bucket}_loc"
        if files_col in out.columns:
            out[files_col] = pd.to_numeric(out[files_col], errors="coerce").fillna(0).clip(lower=0)
        else:
            out[files_col] = 0
        if loc_col in out.columns:
            out[log_loc] = np.log1p(
                pd.to_numeric(out[loc_col], errors="coerce").fillna(0).clip(lower=0).astype(float)
            )
        else:
            out[log_loc] = 0.0
    for flag in ("commit_is_docs_only", "commit_has_tests"):
        if flag in out.columns:
            out[flag] = pd.to_numeric(out[flag], errors="coerce").fillna(0).clip(lower=0, upper=1).astype(int)
        else:
            out[flag] = 0

    # Cache-temperature feature. db.load_jobs_df / lookup_hours_since_
    # last_run produce raw hours; we log-transform here so the model
    # sees diminishing returns at the tail (a 240-hour gap is not
    # 10× as cold as a 24-hour gap in practice — cache wholly evicts
    # within ~24h on most CI runners). Missing column → treat as cold
    # (999h) so behaviour stays safe for predict paths that forgot to
    # supply the column.
    if "hours_since_last_run" in out.columns:
        raw = pd.to_numeric(out["hours_since_last_run"], errors="coerce").fillna(999.0).clip(lower=0)
    else:
        raw = pd.Series(999.0, index=out.index)
    out["log_hours_since_last_run"] = np.log1p(raw.astype(float))

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


def time_based_cv(df: pd.DataFrame, n_splits: int = 5) -> list[tuple[pd.Index, pd.Index]]:
    """Walk-forward time-series cross-validation.

    The dataset is sorted by `run_created_at` and divided into n+1 contiguous
    chunks. For fold k (1..n) the training set is everything before the k-th
    boundary, and the test set is the k-th chunk:

        fold 1: train = [0, t1),   test = [t1, t2)
        fold 2: train = [0, t2),   test = [t2, t3)
        ...
        fold n: train = [0, tn),   test = [tn, T]

    This is the standard for time-series problems and matches what the
    dissertation Chapter 3 cites — random k-fold would leak future data
    into training. Each fold's test slice is the same width on average.

    Returns a list of (train_idx, test_idx) pairs. Falls back to a single
    fold if the dataset is too small to slice meaningfully.
    """
    if "run_created_at" not in df.columns or len(df) < (n_splits + 1) * 5:
        train_idx, test_idx = time_based_split(df, train_frac=0.8)
        return [(train_idx, test_idx)]

    ts = pd.to_datetime(df["run_created_at"], utc=True, errors="coerce")
    # Quantile boundaries — equal-frequency chunks rather than equal-time,
    # so a quiet week doesn't get tiny chunks while a busy week gets huge.
    qs = [(k + 1) / (n_splits + 1) for k in range(n_splits + 1)]
    cutoffs = [ts.quantile(q) for q in qs]
    sorted_idx = ts.sort_values().index

    folds: list[tuple[pd.Index, pd.Index]] = []
    for k in range(n_splits):
        lo, hi = cutoffs[k], cutoffs[k + 1]
        train_mask = ts <= lo
        test_mask = (ts > lo) & (ts <= hi)
        train_idx = df.index[train_mask]
        test_idx = df.index[test_mask]
        if len(train_idx) >= 5 and len(test_idx) >= 1:
            folds.append((train_idx, test_idx))
    if not folds:
        # Degenerate fallback so callers always get at least one fold.
        return [time_based_split(df, 0.8)]
    # Tag the unused sorted_idx so linters don't flag — kept as a
    # debugging hook for visualising fold boundaries on a timeline.
    _ = sorted_idx
    return folds


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
