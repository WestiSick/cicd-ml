"""Stand-alone version of 01_eda.ipynb — run from CI / Make targets.

The notebook is the canonical exploratory artefact, but a .py mirror
exists so:

  1. CI can verify the figure generation without an nbconvert step.
  2. `make eda-figures` works without a Jupyter kernel installed.
  3. Diffs in the repo are readable (.ipynb is JSON, .py is text).

Keep the two in sync — the .py is the source of truth for figure code.
If you edit one, port the same change to the other.

Run inside the ml-service container so it can import `app.*` and reach
the postgres DSN configured by docker-compose:

    docker compose exec ml python /ml/notebooks/generate_eda_figures.py
"""
from __future__ import annotations

import os
import pathlib
import sys

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd

# Sibling-of-app path so this script works whether mounted into /ml or
# run from the host with PYTHONPATH=services/ml-service set.
for candidate in ("/app", "services/ml-service"):
    if os.path.isdir(candidate) and candidate not in sys.path:
        sys.path.insert(0, candidate)

from app.features.build import fit_schema, transform  # noqa: E402
from app.storage.db import load_jobs_df  # noqa: E402


DSN = os.environ.get("POSTGRES_DSN", "postgresql://cicdml:cicdml@db:5432/cicdml")
FIG_DIR = pathlib.Path(os.environ.get("THESIS_FIG_DIR", "/var/lib/cicdml/thesis/figures"))
FIG_DIR.mkdir(parents=True, exist_ok=True)

# Editorial palette — matches the dashboard. Keep this block in sync
# with the notebook's rcParams.
plt.rcParams.update({
    "figure.facecolor": "#0E0F11",
    "axes.facecolor":   "#16181C",
    "axes.edgecolor":   "#2E323A",
    "axes.labelcolor":  "#ECEDEE",
    "axes.titlecolor":  "#ECEDEE",
    "xtick.color":      "#9BA1A6",
    "ytick.color":      "#9BA1A6",
    "text.color":       "#ECEDEE",
    "grid.color":       "#23262C",
    "font.family":      "monospace",
    "font.size":        10,
})
ACCENT, INFO, OK, WARN = "#F2C94C", "#60A5FA", "#4ADE80", "#FBBF24"


def fig_duration_distribution(df: pd.DataFrame) -> None:
    fig, ax = plt.subplots(figsize=(8, 4))
    x = df["duration_sec"].clip(lower=1)
    bins = np.logspace(0, np.log10(x.max() + 1), 50)
    ax.hist(x, bins=bins, color=ACCENT, edgecolor="#16181C")
    ax.set_xscale("log")
    ax.set_xlabel("duration (sec, log scale)")
    ax.set_ylabel("count")
    ax.set_title(f"CI job duration distribution (n = {len(df):,})")
    ax.grid(True, alpha=0.3)
    fig.tight_layout()
    fig.savefig(FIG_DIR / "fig_3_1_duration_distribution.png", dpi=144)
    plt.close(fig)


def fig_top_jobs(df: pd.DataFrame) -> None:
    top = df["job_name"].value_counts().head(15).iloc[::-1]
    fig, ax = plt.subplots(figsize=(8, 5))
    ax.barh(range(len(top)), top.values, color=INFO)
    ax.set_yticks(range(len(top)))
    ax.set_yticklabels([t[:40] for t in top.index])
    ax.set_xlabel("runs")
    ax.set_title("Top-15 job_name by frequency")
    fig.tight_layout()
    fig.savefig(FIG_DIR / "fig_3_2_top_jobs.png", dpi=144)
    plt.close(fig)


def fig_branch_class(df: pd.DataFrame) -> None:
    def _cls(b: str) -> str:
        b = (b or "").lower()
        if b in ("main", "master"):       return "main"
        if b.startswith("release"):       return "release"
        if b.startswith(("feat", "feature", "dev")): return "feature"
        return "other"
    branch_map = df["head_branch"].fillna("").apply(_cls)
    means = df.groupby(branch_map)["duration_sec"].agg(["mean", "median", "count"]).sort_values("count", ascending=False)
    fig, ax = plt.subplots(figsize=(8, 4))
    palette = [OK, INFO, ACCENT, WARN][: len(means)]
    ax.bar(means.index, means["median"], color=palette)
    ax.set_ylabel("median duration (sec)")
    ax.set_title("Median CI duration by branch class")
    fig.tight_layout()
    fig.savefig(FIG_DIR / "fig_3_3_branch_class.png", dpi=144)
    plt.close(fig)


def fig_hour_of_day(df: pd.DataFrame) -> None:
    ts = pd.to_datetime(df["run_created_at"], utc=True, errors="coerce")
    df_h = df.assign(hour=ts.dt.hour).dropna(subset=["hour"])
    hourly = df_h.groupby("hour")["duration_sec"].median()
    fig, ax = plt.subplots(figsize=(8, 3.5))
    ax.plot(hourly.index, hourly.values, color=ACCENT, linewidth=2, marker="o")
    ax.set_xlabel("hour of day (UTC)")
    ax.set_ylabel("median duration (sec)")
    ax.set_xticks(range(0, 24, 2))
    ax.set_title("Median CI duration by hour of day")
    ax.grid(True, alpha=0.3)
    fig.tight_layout()
    fig.savefig(FIG_DIR / "fig_3_4_hour_of_day.png", dpi=144)
    plt.close(fig)


def fig_corr_matrix(df: pd.DataFrame) -> None:
    schema = fit_schema(df)
    X, names, _ = transform(df, schema)
    num_cols = schema.numeric_columns
    num_idx = [names.index(c) for c in num_cols if c in names]
    if len(num_idx) < 2:
        print("skipping corr matrix — not enough numeric features", file=sys.stderr)
        return
    Xnum = X[:, num_idx]
    corr = np.corrcoef(Xnum, rowvar=False)
    fig, ax = plt.subplots(figsize=(8, 7))
    im = ax.imshow(corr, cmap="RdBu_r", vmin=-1, vmax=1, aspect="auto")
    ax.set_xticks(range(len(num_cols)))
    ax.set_xticklabels(num_cols, rotation=45, ha="right")
    ax.set_yticks(range(len(num_cols)))
    ax.set_yticklabels(num_cols)
    fig.colorbar(im, ax=ax, label="Pearson r")
    ax.set_title("Numeric feature correlation matrix")
    fig.tight_layout()
    fig.savefig(FIG_DIR / "fig_3_5_corr_matrix.png", dpi=144)
    plt.close(fig)


def main() -> int:
    df = load_jobs_df(DSN)
    if len(df) == 0:
        print("dataset empty — collect data first via /datasets in the UI", file=sys.stderr)
        return 1
    print(f"loaded {len(df):,} rows; rendering figures into {FIG_DIR}")
    fig_duration_distribution(df)
    fig_top_jobs(df)
    fig_branch_class(df)
    fig_hour_of_day(df)
    fig_corr_matrix(df)
    print("done.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
