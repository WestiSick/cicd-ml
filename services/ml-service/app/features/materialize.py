"""Pipeline for writing per-job feature vectors into the `features` table.

This is the "computed features" path the bootstrap orchestrator's
compute_features bg_job points at. Before this module the training
pipeline recomputed features from scratch on every fit — fine at our
current scale but wasteful, and it meant the on-disk artifact carried
the only canonical schema definition.

Materialised rows let:
  - Training skip recomputation (10-100× speedup at scale).
  - The /datasets dataset-detail page show a feature-matrix preview
    (plan §"3. /datasets/:id").
  - Out-of-process tooling (Jupyter, SHAP) read the same features the
    models saw, by joining `features` with `jobs`.

Implementation:
  - We compute features for every job that doesn't yet have a row in
    `features` at the current FEATURE_VERSION. Re-running is idempotent.
  - We do NOT include the target (duration_sec) in the feature_vector —
    the target lives in jobs.duration_sec and is joined at training
    time. Mixing them risks accidental target leakage.
"""
from __future__ import annotations

import json
import logging
from typing import Any

import numpy as np
from sqlalchemy import text

from ..features.build import FEATURE_VERSION, fit_schema, transform
from ..storage import db

log = logging.getLogger(__name__)


def materialize_all(dsn: str, repo_ids: list[int] | None = None) -> dict[str, Any]:
    """Recompute and persist feature vectors for every eligible job.

    Returns a small summary the api echoes back to the caller (and to
    the bg_jobs progress stream when called from there).
    """
    df = db.load_jobs_df(dsn, repo_ids=repo_ids)
    if df.empty:
        return {"jobs": 0, "written": 0, "feature_version": FEATURE_VERSION}

    # We pin the schema on the full dataset so re-runs use the same
    # vocabulary. In a strict offline-train setting this would split
    # off the training slice first — but materialisation here is for
    # serving (predict-time features), not training, so the larger
    # vocab is the right choice.
    schema = fit_schema(df)
    X, names, _ = transform(df, schema)

    # The feature_vector JSON we store is the dict-of-name→value form
    # rather than the dense matrix, so downstream tooling can read
    # individual features without rebuilding the matrix.
    rows: list[tuple[int, str]] = []
    job_ids = df["job_id"].astype(int).tolist()
    for i, jid in enumerate(job_ids):
        vec: dict[str, float] = {names[j]: float(X[i, j]) for j in range(len(names))}
        rows.append((jid, json.dumps(vec)))

    written = _bulk_upsert(dsn, rows)
    log.info("materialize_features: %d jobs, %d rows written", len(df), written)
    return {
        "jobs": int(len(df)),
        "written": int(written),
        "feature_version": FEATURE_VERSION,
        "feature_count": len(names),
    }


def _bulk_upsert(dsn: str, rows: list[tuple[int, str]]) -> int:
    """Insert/update in chunks to keep memory bounded.

    Even at 100k jobs this fits in a single statement on Postgres, but
    chunking makes the ON CONFLICT path's row-locking lighter and gives
    progress points if we wire this into a streaming worker later.
    """
    if not rows:
        return 0
    chunk = 1000
    total = 0
    with db.connection(dsn) as conn:
        with conn.begin():
            for i in range(0, len(rows), chunk):
                slc = rows[i : i + chunk]
                conn.execute(
                    text(
                        """
                        INSERT INTO features (job_id, feature_vector, feature_version)
                        VALUES (:jid, CAST(:fv AS jsonb), :fver)
                        ON CONFLICT (job_id) DO UPDATE
                          SET feature_vector  = EXCLUDED.feature_vector,
                              feature_version = EXCLUDED.feature_version,
                              computed_at     = now()
                        """
                    ),
                    [{"jid": jid, "fv": fv, "fver": FEATURE_VERSION} for jid, fv in slc],
                )
                total += len(slc)
    return total
