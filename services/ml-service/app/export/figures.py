"""LaTeX-ready figure generation.

Every plot here is designed to be **dropped straight into Chapter 4**:

  - 300 DPI, vector PDF + raster PNG side by side.
  - Single-colour bars/lines (matplotlib's default tab10 cycle is too
    busy for a one-figure-per-page thesis layout).
  - mono ticks via the same monospace fallback chain we use in the UI.
  - Tight margins — full A4 width is 6.3in at 1in margins, so we size
    every figure to 5.5in wide and let the LaTeX `\\includegraphics`
    handle the rest.

The matplotlib `agg` backend is set explicitly because the ml container
has no X server — without it the import would try to talk to a display
and fail on first call.
"""
from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import matplotlib
matplotlib.use("Agg")  # headless container — no X11
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from sqlalchemy import text

from ..storage import db


# ---- Style ------------------------------------------------------------

# Single accent colour matching the UI's amber. Keeps the printed thesis
# figure visually consistent with screenshots from the live dashboard.
ACCENT = "#d4a017"
NEUTRAL_DARK  = "#16181c"
NEUTRAL_LIGHT = "#9ba1a6"
GRID = "#e5e7eb"


def _apply_style() -> None:
    plt.rcParams.update({
        "font.family": "monospace",
        "font.size": 9,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "axes.edgecolor": NEUTRAL_DARK,
        "axes.labelcolor": NEUTRAL_DARK,
        "axes.titleweight": "normal",
        "xtick.color": NEUTRAL_LIGHT,
        "ytick.color": NEUTRAL_LIGHT,
        "xtick.direction": "out",
        "ytick.direction": "out",
        "grid.color": GRID,
        "grid.linewidth": 0.5,
        "axes.grid": True,
        "axes.axisbelow": True,
        "figure.dpi": 110,
        "savefig.dpi": 300,
        "savefig.bbox": "tight",
    })


def _save(fig, out_dir: Path, name: str) -> list[str]:
    """Save both PNG and PDF — PDF for LaTeX, PNG for slides/preview.
    Returns list of paths written."""
    out_dir.mkdir(parents=True, exist_ok=True)
    paths: list[str] = []
    for ext in ("png", "pdf"):
        p = out_dir / f"{name}.{ext}"
        fig.savefig(p)
        paths.append(str(p))
    plt.close(fig)
    return paths


# ---- Figures ----------------------------------------------------------


def predicted_vs_actual(dsn: str, model_id: int, out_dir: Path) -> list[str]:
    """Scatter (predicted, actual) in log–log space with the y=x reference.

    Visualises model fit quality on the test set. The dissertation cites
    R² + Spearman from `models.metrics`; this plot is the visual companion.
    """
    sql = """
        SELECT j.duration_sec AS actual, p.predicted_sec AS predicted
        FROM predictions p
        JOIN jobs j ON p.job_id = j.id
        WHERE p.model_id = :mid AND j.duration_sec IS NOT NULL AND j.duration_sec > 0
    """
    with db.connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn, params={"mid": model_id})

    _apply_style()
    fig, ax = plt.subplots(figsize=(5.5, 4.4))
    if len(df) > 0:
        lo = max(1, float(min(df.actual.min(), df.predicted.min())))
        hi = float(max(df.actual.max(), df.predicted.max())) * 1.05
        ax.set_xscale("log")
        ax.set_yscale("log")
        ax.set_xlim(lo, hi)
        ax.set_ylim(lo, hi)
        ax.plot([lo, hi], [lo, hi], color=NEUTRAL_LIGHT, linestyle="--", linewidth=1, label="y = x (perfect)")
        ax.scatter(df.actual, df.predicted, c=ACCENT, alpha=0.5, s=10, edgecolors="none", label=f"n = {len(df)}")
        ax.legend(frameon=False, loc="upper left", fontsize=8)
    ax.set_xlabel("actual duration, sec")
    ax.set_ylabel("predicted duration, sec")
    ax.set_title(f"Predicted vs. actual — model #{model_id}", loc="left", fontsize=10)
    return _save(fig, out_dir, f"predicted_vs_actual_model_{model_id}")


def feature_importance_chart(dsn: str, model_id: int, out_dir: Path, top: int = 20) -> list[str]:
    """Horizontal bar chart of top-K feature importance values."""
    with db.connection(dsn) as conn:
        row = conn.execute(
            text("SELECT feature_importance FROM models WHERE id = :mid"),
            {"mid": model_id},
        ).mappings().first()
    fi: dict[str, float] = dict(row["feature_importance"] or {}) if row else {}
    items = sorted(fi.items(), key=lambda kv: kv[1], reverse=True)[:top]

    _apply_style()
    fig, ax = plt.subplots(figsize=(5.5, max(2.5, 0.25 * len(items) + 1)))
    if items:
        names = [n[:42] + ("…" if len(n) > 42 else "") for n, _ in items]
        values = [v for _, v in items]
        y = np.arange(len(items))
        ax.barh(y, values, color=ACCENT, height=0.7)
        ax.set_yticks(y)
        ax.set_yticklabels(names, fontsize=8)
        ax.invert_yaxis()
        ax.set_xlabel("importance")
    ax.set_title(f"Top {len(items)} features — model #{model_id}", loc="left", fontsize=10)
    return _save(fig, out_dir, f"feature_importance_model_{model_id}")


def model_comparison(dsn: str, out_dir: Path) -> list[str]:
    """One panel per metric (MAE / RMSE / R² / Spearman), bars across models.

    The single most-used thesis figure: "look how XGBoost beats Linear".
    Stable model ordering = trained_at ASC, so re-runs produce
    deterministic axis layouts.
    """
    sql = """
        SELECT id, name, algo,
               (metrics->>'mae_test_sec')::float  AS mae,
               (metrics->>'rmse_test_sec')::float AS rmse,
               (metrics->>'r2_test')::float       AS r2,
               (metrics->>'spearman_test')::float AS spearman
        FROM models ORDER BY trained_at ASC
    """
    with db.connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn)

    _apply_style()
    fig, axes = plt.subplots(2, 2, figsize=(7, 5.5))
    if len(df) == 0:
        for ax in axes.flat:
            ax.text(0.5, 0.5, "no models", ha="center", va="center", color=NEUTRAL_LIGHT)
            ax.set_xticks([])
            ax.set_yticks([])
    else:
        labels = [f"{r.algo}#{int(r.id)}" for r in df.itertuples()]
        x = np.arange(len(df))
        for ax, col, title, lower_better in [
            (axes[0, 0], "mae",      "MAE test, sec",   True),
            (axes[0, 1], "rmse",     "RMSE test, sec",  True),
            (axes[1, 0], "r2",       "R² test",         False),
            (axes[1, 1], "spearman", "Spearman test",   False),
        ]:
            vals = df[col].fillna(0).to_numpy()
            ax.bar(x, vals, color=ACCENT, width=0.65)
            ax.set_xticks(x)
            ax.set_xticklabels(labels, rotation=30, ha="right", fontsize=7)
            ax.set_title(title + (" ↓" if lower_better else " ↑"), loc="left", fontsize=9)
    fig.suptitle("Model comparison", fontsize=11, x=0.05, ha="left")
    fig.tight_layout(rect=(0, 0, 1, 0.96))
    return _save(fig, out_dir, "model_comparison")


def strategy_comparison(dsn: str, out_dir: Path) -> list[str]:
    """Per-strategy bars: wait p95, wait mean, makespan, SLA violations.

    Aggregates sim_runs across the latest simulation (highest id per
    strategy). Plot is the centrepiece of Chapter 4 — keep its layout
    stable across thesis iterations.
    """
    sql = """
        SELECT DISTINCT ON (strategy)
            strategy, makespan_sec, wait_p50_sec, wait_p95_sec,
            sla_violations,
            (extra->>'wait_mean_sec')::float AS wait_mean
        FROM sim_runs
        ORDER BY strategy, created_at DESC
    """
    with db.connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn)

    _apply_style()
    fig, axes = plt.subplots(2, 2, figsize=(7, 5.5))
    if len(df) == 0:
        for ax in axes.flat:
            ax.text(0.5, 0.5, "no simulations", ha="center", va="center", color=NEUTRAL_LIGHT)
    else:
        # Stable strategy order — alphabetical so the layout matches
        # across runs and thesis revisions.
        df = df.sort_values("strategy").reset_index(drop=True)
        labels = df["strategy"].tolist()
        x = np.arange(len(df))
        for ax, col, title, lower_better in [
            (axes[0, 0], "wait_mean",      "Wait mean, sec",  True),
            (axes[0, 1], "wait_p95_sec",   "Wait p95, sec",   True),
            (axes[1, 0], "makespan_sec",   "Makespan, sec",   True),
            (axes[1, 1], "sla_violations", "SLA violations",  True),
        ]:
            vals = df[col].fillna(0).to_numpy()
            ax.bar(x, vals, color=ACCENT, width=0.6)
            ax.set_xticks(x)
            ax.set_xticklabels(labels, fontsize=9)
            ax.set_title(title + " ↓", loc="left", fontsize=9)
    fig.suptitle("Scheduling-strategy comparison", fontsize=11, x=0.05, ha="left")
    fig.tight_layout(rect=(0, 0, 1, 0.96))
    return _save(fig, out_dir, "strategy_comparison")


def training_curves(dsn: str, training_job_id: int, out_dir: Path) -> list[str]:
    """train_loss + val_rmse line chart over iterations.

    Empty plot if the training run was a single-shot algo (Linear/RF/MLP)
    that didn't emit per-iteration metrics — caller can skip non-boosted.
    """
    sql = """
        SELECT iteration, train_loss, val_rmse, val_mae
        FROM training_metrics
        WHERE training_job_id = :tid
        ORDER BY iteration
    """
    with db.connection(dsn) as conn:
        df = pd.read_sql(text(sql), conn, params={"tid": training_job_id})

    _apply_style()
    fig, ax = plt.subplots(figsize=(5.5, 3.6))
    if len(df) >= 2:
        if df["train_loss"].notna().any():
            ax.plot(df["iteration"], df["train_loss"], color=NEUTRAL_DARK, linewidth=1.4, label="train")
        if df["val_rmse"].notna().any():
            ax.plot(df["iteration"], df["val_rmse"], color=ACCENT, linewidth=1.4, label="validation")
        ax.legend(frameon=False, loc="upper right", fontsize=8)
    ax.set_xlabel("iteration")
    ax.set_ylabel("RMSE (log-sec space)")
    ax.set_title(f"Training curves — run #{training_job_id}", loc="left", fontsize=10)
    return _save(fig, out_dir, f"training_curves_run_{training_job_id}")


# ---- Entry point ------------------------------------------------------


def export_all(dsn: str, out_root: str, timestamp: str) -> dict[str, Any]:
    """Generate every dissertation figure into out_root/<timestamp>/figures/.

    Returns a manifest the gateway echoes back to the UI.
    """
    out_dir = Path(out_root) / timestamp / "figures"
    out_dir.mkdir(parents=True, exist_ok=True)

    # Find the active model and its training_job_id.
    with db.connection(dsn) as conn:
        active = conn.execute(text("""
            SELECT id, training_job_id FROM models WHERE is_active = TRUE LIMIT 1
        """)).mappings().first()

    written: list[str] = []
    written += model_comparison(dsn, out_dir)
    written += strategy_comparison(dsn, out_dir)
    if active:
        mid = int(active["id"])
        written += predicted_vs_actual(dsn, mid, out_dir)
        written += feature_importance_chart(dsn, mid, out_dir, top=20)
        tjid = active["training_job_id"]
        if tjid is not None:
            written += training_curves(dsn, int(tjid), out_dir)

    return {
        "directory": str(out_dir),
        "files": [os.path.basename(p) for p in written],
        "active_model_id": int(active["id"]) if active else None,
    }
