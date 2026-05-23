"""Unit tests for the per-file commit classifier.

The bucket assignment drives a sizeable fraction of the feature vector
in v2 — a misrouted regex would silently bias predictions. Locking the
classification down with table tests is the cheap insurance.
"""
import pandas as pd
import pytest

from app.features.file_buckets import (
    BUCKETS,
    aggregate_commit_files,
    classify_path,
    classify_series,
    empty_bucket_columns,
)


@pytest.mark.parametrize(
    "path,expected",
    [
        # Test paths (matched first, beats backend/frontend extension)
        ("tests/test_foo.py",                 "test"),
        ("internal/foo/foo_test.go",          "test"),
        ("frontend/src/Button.spec.tsx",      "test"),
        ("__tests__/widget.test.ts",          "test"),
        ("e2e/login.spec.js",                 "test"),
        # Backend
        ("services/api-gateway/main.go",      "backend"),
        ("app/models/base.py",                "backend"),
        ("src/lib.rs",                        "backend"),
        ("backend/server/handler.scala",      "backend"),
        # Frontend
        ("frontend/src/App.tsx",              "frontend"),
        ("ui/components/Header.vue",          "frontend"),
        ("public/index.html",                 "frontend"),
        ("styles/global.css",                 "frontend"),
        # Docs
        ("README.md",                         "docs"),
        ("docs/architecture.md",              "docs"),
        ("CHANGELOG",                         "docs"),
        ("LICENSE",                           "docs"),
        # Config
        (".github/workflows/ci.yml",          "config"),
        ("Dockerfile",                        "config"),
        ("package.json",                      "config"),
        ("go.mod",                            "config"),
        ("pyproject.toml",                    "config"),
        # Other
        ("assets/logo.png",                   "other"),
        ("data/fixture.bin",                  "other"),
        ("",                                  "other"),
    ],
)
def test_classify_path_table(path, expected):
    assert classify_path(path) == expected


def test_classify_series_matches_classify_path():
    """Vectorised path must produce identical results to row-wise."""
    paths = [
        "tests/test_a.py",
        "src/api.go",
        "ui/App.tsx",
        "README.md",
        "package.json",
        "image.png",
    ]
    s = pd.Series(paths)
    vec = classify_series(s).tolist()
    row = [classify_path(p) for p in paths]
    assert vec == row


def test_aggregate_commit_files_counts_and_locs():
    df = pd.DataFrame(
        [
            # SHA aaa: docs-only push (README) — fast pipeline expected
            {"sha": "aaa", "filename": "README.md", "additions": 3, "deletions": 1},
            # SHA bbb: backend code change + corresponding test
            {"sha": "bbb", "filename": "services/api/main.go",        "additions": 40, "deletions": 5},
            {"sha": "bbb", "filename": "services/api/main_test.go",   "additions": 25, "deletions": 0},
            # SHA ccc: frontend + config (lockfile bump)
            {"sha": "ccc", "filename": "frontend/src/Foo.tsx",        "additions": 12, "deletions": 4},
            {"sha": "ccc", "filename": "frontend/package-lock.json",  "additions": 200, "deletions": 180},
        ]
    )
    out = aggregate_commit_files(df).set_index("sha")

    # All expected columns present
    for col in empty_bucket_columns():
        assert col in out.columns

    # aaa — docs-only
    assert out.loc["aaa", "commit_docs_files"] == 1
    assert out.loc["aaa", "commit_backend_files"] == 0
    assert out.loc["aaa", "commit_is_docs_only"] == 1
    assert out.loc["aaa", "commit_has_tests"] == 0
    assert out.loc["aaa", "commit_docs_loc"] == 4

    # bbb — backend + test
    assert out.loc["bbb", "commit_backend_files"] == 1
    assert out.loc["bbb", "commit_test_files"] == 1
    assert out.loc["bbb", "commit_is_docs_only"] == 0
    assert out.loc["bbb", "commit_has_tests"] == 1
    assert out.loc["bbb", "commit_backend_loc"] == 45  # 40 + 5
    assert out.loc["bbb", "commit_test_loc"] == 25

    # ccc — frontend + config (no code-only files outside frontend so it
    # is NOT docs-only because frontend counts as code)
    assert out.loc["ccc", "commit_frontend_files"] == 1
    assert out.loc["ccc", "commit_config_files"] == 1
    assert out.loc["ccc", "commit_is_docs_only"] == 0
    assert out.loc["ccc", "commit_has_tests"] == 0


def test_aggregate_commit_files_empty():
    out = aggregate_commit_files(pd.DataFrame(columns=["sha", "filename", "additions", "deletions"]))
    # Empty input → empty frame but with the schema cols defined so
    # downstream consumers can rely on the column set.
    for col in empty_bucket_columns():
        assert col in out.columns


def test_buckets_constant_matches_expected_set():
    assert set(BUCKETS) == {"test", "backend", "frontend", "docs", "config", "other"}
