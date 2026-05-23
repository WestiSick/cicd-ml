"""SQLAlchemy engine + thin loaders.

The ml-service reads the same Postgres the api-gateway writes to. We use
SQLAlchemy + pandas.read_sql for ergonomics — a Linear regression baseline
on 200 jobs doesn't need a hand-tuned cursor. For million-row windows we'd
switch to chunked reads; until then keep it simple.
"""
from __future__ import annotations

from contextlib import contextmanager
from typing import Any, Iterator

import pandas as pd
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from ..features.file_buckets import aggregate_commit_files, empty_bucket_columns

_ENGINE: Engine | None = None


def get_engine(dsn: str) -> Engine:
    """Lazy-singleton engine. ml-service is single-process so one engine
    instance is fine; FastAPI's threadpool uses the connection pool."""
    global _ENGINE
    if _ENGINE is None:
        _ENGINE = create_engine(dsn, pool_size=4, max_overflow=4, future=True)
    return _ENGINE


@contextmanager
def connection(dsn: str) -> Iterator[Any]:
    eng = get_engine(dsn)
    with eng.connect() as conn:
        yield conn


def load_jobs_df(dsn: str, repo_ids: list[int] | None = None, since: str | None = None) -> pd.DataFrame:
    """Return one row per completed job, joined with run + repo info.

    Filtered to jobs with non-null duration_sec — training is meaningless
    without a target. The optional repo_ids/since let callers slice for
    train/test splits or per-experiment subsets.
    """
    # LEFT JOIN commits: collector only fills `commits` for SHAs it
    # actually fetched, so NULL is normal for older runs predating the
    # commit-collection rollout. Feature engineering treats NULLs as 0.
    #
    # wr_lag CTE: hours_since_last_run per workflow_run, partitioned by
    # repo. Captures docker-layer-cache state for self-hosted runners
    # (the dominant predictor of bimodal warm/cold deploy duration that
    # commit-content features alone cannot explain — see thesis Chapter 4
    # error-analysis section). First run in a partition gets NULL from
    # LAG and is COALESCEd to 999 hours = treated as cold start.
    sql = """
        WITH wr_lag AS (
            SELECT id,
                   EXTRACT(EPOCH FROM (
                     created_at - LAG(created_at) OVER (
                       PARTITION BY repo_id ORDER BY created_at, id
                     )
                   )) / 3600.0 AS hours_since_last_run
            FROM workflow_runs
        )
        SELECT
            j.id              AS job_id,
            j.name            AS job_name,
            j.duration_sec    AS duration_sec,
            j.runner_name     AS runner_name,
            j.runner_group    AS runner_group,
            j.steps_count     AS steps_count,
            j.labels          AS labels,
            j.started_at      AS started_at,
            w.id              AS run_id,
            w.workflow_name   AS workflow_name,
            w.head_branch     AS head_branch,
            w.head_sha        AS head_sha,
            w.event           AS event,
            w.actor           AS actor,
            w.created_at      AS run_created_at,
            r.id              AS repo_id,
            r.owner           AS repo_owner,
            r.name            AS repo_name,
            c.files_changed   AS commit_files_changed,
            c.additions       AS commit_additions,
            c.deletions       AS commit_deletions,
            COALESCE(wl.hours_since_last_run, 999.0) AS hours_since_last_run
        FROM jobs j
        JOIN workflow_runs w ON j.run_id = w.id
        JOIN wr_lag wl       ON wl.id = w.id
        JOIN repos r         ON w.repo_id = r.id
        LEFT JOIN commits c  ON w.head_sha = c.sha
        WHERE j.duration_sec IS NOT NULL
          AND j.duration_sec > 0
    """
    params: dict[str, Any] = {}
    if repo_ids:
        sql += " AND r.id = ANY(:repo_ids)"
        params["repo_ids"] = repo_ids
    if since:
        sql += " AND w.created_at >= :since"
        params["since"] = since
    sql += " ORDER BY w.created_at ASC"

    with connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn, params=params)
    return attach_commit_file_features(df, dsn)


def attach_commit_file_features(df: pd.DataFrame, dsn: str) -> pd.DataFrame:
    """Merge per-bucket commit-content features into a jobs dataframe.

    Reusable so /predict's job_ids path and load_jobs_df produce a
    schema-identical result — keeping these paths aligned is the
    only way to ensure prediction uses the same feature set as
    training without silent zero-fills.

    Requires the input frame to contain a `head_sha` column; if
    missing, the function still returns a frame with the new columns
    populated with zeros so downstream transform() has a stable shape.
    """
    if df.empty or "head_sha" not in df.columns:
        for col in empty_bucket_columns():
            df[col] = 0 if df.empty else pd.Series(0, index=df.index)
        return df

    sha_series = df["head_sha"].dropna().unique().tolist()
    cf_df = _load_commit_file_buckets(dsn, sha_series)
    df = df.merge(cf_df, how="left", left_on="head_sha", right_on="sha")
    if "sha" in df.columns:
        df = df.drop(columns=["sha"])
    # Fill missing — a SHA without commit_files data (collector hadn't
    # fetched its diff yet, or it's a force-push that destroyed the
    # commit) gets zeros, not NaN. The model treats "no signal" and
    # "zero files of this type" identically.
    for col in empty_bucket_columns():
        if col not in df.columns:
            df[col] = 0
        df[col] = pd.to_numeric(df[col], errors="coerce").fillna(0)
    return df


def load_prediction_errors(dsn: str) -> dict[int, float]:
    """For every job that has a stored prediction, return the relative
    error |predicted - actual| / actual. Used by error-weighted training
    to up-weight jobs the previous model got most wrong, on the theory
    that those slices either contain new patterns (drift) or are under-
    represented in the training set.

    Joins predictions with jobs.duration_sec — rows without an actual
    duration are skipped (can't compute the error). Returns an empty
    dict on empty DB so callers can safely call this even on fresh
    installs.

    Returns: {job_id: relative_error}.
        relative_error of 0.0 = perfect prediction.
        relative_error of 0.5 = 50% off (e.g. predicted 30s, actual 20s).
        Capped at 5.0 to keep one extreme outlier from dominating the
        sample weights downstream.
    """
    sql = """
        SELECT p.job_id,
               GREATEST(p.predicted_sec, 0) AS predicted,
               j.duration_sec AS actual
        FROM predictions p
        JOIN jobs j ON j.id = p.job_id
        WHERE j.duration_sec IS NOT NULL
          AND j.duration_sec > 0
    """
    out: dict[int, float] = {}
    with connection(dsn) as conn:
        rows = conn.execute(text(sql)).all()
    for jid, pred, actual in rows:
        if actual is None or actual <= 0:
            continue
        err = abs(float(pred) - float(actual)) / float(actual)
        # Clamp extremes — a model that predicted 1s where actual was
        # 600s shouldn't make that single job 600× more important than
        # an average sample. 5× is generous but bounded.
        if err > 5.0:
            err = 5.0
        out[int(jid)] = err
    return out


def lookup_hours_since_last_run(dsn: str, owner: str, name: str) -> float:
    """How many hours since the most recent workflow_run for this repo.

    Used by predict_from_payload: at webhook time we don't yet have a
    workflow_runs row for the incoming push, so we can't use the LAG()
    in load_jobs_df. Instead we look up the previous run and return the
    delta to NOW — which is exactly what LAG would produce once this
    push lands in the table.

    Returns 999.0 when the repo has no prior runs (cold start). Also
    returns 999.0 on any error — the model treats large values as
    "cold cache" which is the safer default.
    """
    if not owner or not name:
        return 999.0
    sql = """
        SELECT EXTRACT(EPOCH FROM (now() - max(w.created_at))) / 3600.0
        FROM workflow_runs w
        JOIN repos r ON w.repo_id = r.id
        WHERE r.owner = :owner AND r.name = :name
    """
    try:
        with connection(dsn) as conn:
            row = conn.execute(text(sql), {"owner": owner, "name": name}).first()
        if row is None or row[0] is None:
            return 999.0
        v = float(row[0])
        # Guard against negative (clock skew) — clamp to 0.
        return max(0.0, v)
    except Exception:
        return 999.0


def _load_commit_file_buckets(dsn: str, shas: list[str]) -> pd.DataFrame:
    """Aggregate commit_files for the given SHAs into per-bucket feature
    columns. Returns one row per SHA (or empty frame if no rows found).

    Splits the SHA list into chunks because Postgres errors at >65k bind
    parameters, and a one-year window across several active repos can
    easily exceed that. 5000 SHAs per chunk leaves plenty of headroom.
    """
    if not shas:
        return pd.DataFrame(columns=["sha", *empty_bucket_columns()])

    sql = """
        SELECT sha, filename, additions, deletions
        FROM commit_files
        WHERE sha = ANY(:shas)
    """
    chunk = 5000
    frames: list[pd.DataFrame] = []
    with connection(dsn) as conn:
        for start in range(0, len(shas), chunk):
            piece = shas[start : start + chunk]
            frames.append(pd.read_sql(text(sql), conn, params={"shas": piece}))
    raw = pd.concat(frames, ignore_index=True) if frames else pd.DataFrame()
    return aggregate_commit_files(raw)


def fetch_active_model_row(dsn: str) -> dict[str, Any] | None:
    """Returns the currently-active model row or None if no model is active."""
    sql = """
        SELECT id, name, algo, params, metrics, artifact_path, feature_version
        FROM models WHERE is_active = TRUE LIMIT 1
    """
    with connection(dsn) as conn:
        row = conn.execute(text(sql)).mappings().first()
    return dict(row) if row else None


def list_models_rows(dsn: str) -> list[dict[str, Any]]:
    sql = """
        SELECT id, name, algo, params, metrics, artifact_path,
               training_job_id, feature_version, is_active, trained_at
        FROM models ORDER BY trained_at DESC
    """
    with connection(dsn) as conn:
        rows = conn.execute(text(sql)).mappings().all()
    return [dict(r) for r in rows]


def insert_model_row(
    dsn: str,
    *,
    name: str,
    algo: str,
    params: dict[str, Any],
    metrics: dict[str, Any],
    feature_importance: dict[str, float],
    artifact_path: str,
    training_job_id: int | None,
    feature_version: int,
) -> int:
    sql = """
        INSERT INTO models (name, algo, params, metrics, feature_importance,
                            artifact_path, training_job_id, feature_version, is_active)
        VALUES (:name, :algo, CAST(:params AS jsonb), CAST(:metrics AS jsonb),
                CAST(:feature_importance AS jsonb),
                :artifact_path, :training_job_id, :feature_version, FALSE)
        RETURNING id
    """
    import json as _json

    with connection(dsn) as conn:
        with conn.begin():
            mid = conn.execute(
                text(sql),
                {
                    "name": name,
                    "algo": algo,
                    "params": _json.dumps(params),
                    "metrics": _json.dumps(metrics),
                    "feature_importance": _json.dumps(feature_importance),
                    "artifact_path": artifact_path,
                    "training_job_id": training_job_id,
                    "feature_version": feature_version,
                },
            ).scalar_one()
    return int(mid)


def set_active_model(dsn: str, model_id: int) -> None:
    """Atomically flip the `is_active` flag — the partial unique index in
    the schema enforces "at most one active model" so we deactivate first
    then activate inside a transaction."""
    with connection(dsn) as conn:
        with conn.begin():
            conn.execute(text("UPDATE models SET is_active = FALSE WHERE is_active = TRUE"))
            conn.execute(text("UPDATE models SET is_active = TRUE WHERE id = :id"), {"id": model_id})


def insert_predictions(dsn: str, model_id: int, rows: list[tuple[int, float]]) -> int:
    """Bulk-upsert predictions. Returns the number of rows written.

    Conflict resolution: (job_id, model_id) is the PK, so re-running
    /predict for the same set with the same model just refreshes
    predicted_sec — useful when the feature pipeline changes.
    """
    if not rows:
        return 0
    sql = """
        INSERT INTO predictions (job_id, model_id, predicted_sec)
        VALUES (:job_id, :model_id, :predicted_sec)
        ON CONFLICT (job_id, model_id) DO UPDATE
          SET predicted_sec = EXCLUDED.predicted_sec,
              made_at = now()
    """
    with connection(dsn) as conn:
        with conn.begin():
            conn.execute(
                text(sql),
                [{"job_id": j, "model_id": model_id, "predicted_sec": p} for j, p in rows],
            )
    return len(rows)
