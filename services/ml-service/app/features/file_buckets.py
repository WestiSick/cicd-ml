"""Classify changed file paths into semantic buckets.

The thesis hypothesis is that **what** a commit changed predicts CI
duration better than just **how much** it changed. A README-only commit
probably hits a quick docs lint; a backend code change triggers the full
test matrix; a frontend change runs the frontend build but skips the
backend test suite.

Bucket ordering (matches `classify_path` precedence):

  test       — anything in tests/__tests__/spec/specs paths, or
               test_*.py / *_test.go / *.spec.ts style filenames.
               Matched FIRST so test_foo.go counts as "test", not "backend".
  backend    — Go/Python/Java/Rust/Ruby/.../C# files OR paths under
               backend/api/server/services.
  frontend   — TypeScript/JavaScript/Vue/Svelte/CSS/HTML files OR paths
               under frontend/ui/web/app/client.
  docs       — Markdown, RST, plain-text, AsciiDoc, README-anything.
  config     — YAML/JSON/TOML/INI/env/lockfiles + canonical config
               filenames (Dockerfile, Makefile, package.json, etc.).
  other      — anything else (binary assets, images, fixtures).

These regexes are deliberately simple — we'd rather miss a few exotic
files than over-engineer for an edge case the model won't see often.
The classification is computed at feature-engineering time so we can
revise buckets without re-fetching from GitHub.
"""
from __future__ import annotations

import re
from typing import Iterable

import pandas as pd

# Order matters — test patterns checked first so /tests/test_foo.go
# is classified as "test" rather than falling through to "backend".
_PATTERNS: dict[str, re.Pattern[str]] = {
    "test": re.compile(
        r"(^|/)(tests?|__tests__|specs?|e2e)(/|$)"
        r"|(^|/)test_[^/]+$"
        r"|_test\.(go|py|js|ts|tsx|rb)$"
        r"|\.spec\.(ts|tsx|js|jsx)$"
        r"|Test\.java$",
        re.IGNORECASE,
    ),
    "backend": re.compile(
        r"\.(go|py|java|rs|rb|scala|kt|cs|php|cpp|cc|c|hpp|h|ex|exs|swift|m|mm|erl|clj)$"
        r"|(^|/)(backend|api|server|services?|internal|cmd|pkg)(/|$)",
        re.IGNORECASE,
    ),
    "frontend": re.compile(
        r"\.(tsx?|jsx?|mjs|cjs|vue|svelte|css|scss|sass|less|html?)$"
        r"|(^|/)(frontend|ui|web|app|client|public)(/|$)",
        re.IGNORECASE,
    ),
    "docs": re.compile(
        r"\.(md|mdx|rst|txt|adoc|asciidoc)$"
        r"|(^|/)docs?(/|$)"
        r"|(^|/)README"
        r"|(^|/)CHANGELOG"
        r"|(^|/)CONTRIBUTING"
        r"|(^|/)LICENSE",
        re.IGNORECASE,
    ),
    "config": re.compile(
        r"\.(ya?ml|json|toml|ini|cfg|conf|env|lock|properties|gradle)$"
        r"|(^|/)\.github(/|$)"
        r"|(^|/)\.gitlab(/|$)"
        r"|(^|/)(Dockerfile|Makefile|Procfile|Pipfile|Gemfile|Brewfile)(\.|$)"
        r"|(^|/)(requirements|setup)\.(txt|py|cfg)$"
        r"|(^|/)(go|package|yarn|composer)\.(mod|sum|json|lock)$"
        r"|(^|/)pyproject\.toml$",
        re.IGNORECASE,
    ),
}

BUCKETS: tuple[str, ...] = ("test", "backend", "frontend", "docs", "config", "other")


def classify_path(p: str) -> str:
    """Return the bucket name for a single filename.

    Empty / None → "other" (won't crash on bad GitHub payloads).
    """
    if not p:
        return "other"
    for bucket, pat in _PATTERNS.items():
        if pat.search(p):
            return bucket
    return "other"


def classify_series(s: pd.Series) -> pd.Series:
    """Vectorised classification — much faster than `.apply(classify_path)`
    on a 100k-row commit_files dataframe.

    Walks the patterns in priority order: rows that match `test` get
    assigned "test" and removed from the candidate pool, then the next
    pattern fills the next slot, and so on. Anything unmatched at the
    end falls to "other".
    """
    out = pd.Series(["other"] * len(s), index=s.index, dtype="object")
    remaining = pd.Series(True, index=s.index)
    s_str = s.fillna("").astype(str)
    for bucket, pat in _PATTERNS.items():
        if not remaining.any():
            break
        # str.contains with a compiled regex is the fastest path in pandas.
        m = s_str[remaining].str.contains(pat, regex=True, na=False)
        hit_idx = m[m].index
        out.loc[hit_idx] = bucket
        remaining.loc[hit_idx] = False
    return out


def aggregate_commit_files(df: pd.DataFrame) -> pd.DataFrame:
    """Per-commit aggregation: counts per bucket + LOC per bucket.

    Input columns: sha, filename, additions, deletions.
    Output columns (one row per sha):
        commit_test_files, commit_backend_files, commit_frontend_files,
        commit_docs_files, commit_config_files, commit_other_files,
        commit_test_loc,   commit_backend_loc,   commit_frontend_loc,
        commit_docs_loc,   commit_config_loc,    commit_other_loc,
        commit_is_docs_only   — 1 iff every changed file is docs/config
        commit_has_tests      — 1 iff at least one test file was touched
    """
    if df.empty:
        cols = (
            [f"commit_{b}_files" for b in BUCKETS]
            + [f"commit_{b}_loc" for b in BUCKETS]
            + ["commit_is_docs_only", "commit_has_tests"]
        )
        return pd.DataFrame(columns=["sha", *cols])

    w = df.copy()
    w["bucket"] = classify_series(w["filename"])
    w["loc"] = (
        pd.to_numeric(w.get("additions", 0), errors="coerce").fillna(0)
        + pd.to_numeric(w.get("deletions", 0), errors="coerce").fillna(0)
    ).clip(lower=0)

    # Counts: file-count per (sha, bucket).
    counts = (
        w.groupby(["sha", "bucket"], observed=True)
        .size()
        .unstack(fill_value=0)
        .reindex(columns=list(BUCKETS), fill_value=0)
        .add_prefix("commit_")
        .rename(columns={f"commit_{b}": f"commit_{b}_files" for b in BUCKETS})
    )

    # LOC: sum of additions+deletions per (sha, bucket).
    locs = (
        w.groupby(["sha", "bucket"], observed=True)["loc"]
        .sum()
        .unstack(fill_value=0)
        .reindex(columns=list(BUCKETS), fill_value=0)
        .rename(columns={b: f"commit_{b}_loc" for b in BUCKETS})
    )

    out = counts.join(locs).reset_index()

    # Derived flags.
    code_files = (
        out["commit_test_files"]
        + out["commit_backend_files"]
        + out["commit_frontend_files"]
        + out["commit_other_files"]
    )
    out["commit_is_docs_only"] = (code_files == 0).astype(int)
    out["commit_has_tests"] = (out["commit_test_files"] > 0).astype(int)
    return out


def empty_bucket_columns() -> list[str]:
    """List of feature column names this module contributes — used by
    callers that need to add NaN-filled fallbacks when no commit_files
    rows are present (so the model schema stays stable)."""
    cols = [f"commit_{b}_files" for b in BUCKETS]
    cols += [f"commit_{b}_loc" for b in BUCKETS]
    cols += ["commit_is_docs_only", "commit_has_tests"]
    return cols


__all__ = [
    "BUCKETS",
    "classify_path",
    "classify_series",
    "aggregate_commit_files",
    "empty_bucket_columns",
]


# Convenience iter for tests/doc — gives the per-bucket pretty name.
def bucket_labels() -> Iterable[str]:
    return BUCKETS
