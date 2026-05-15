"""Unit tests for the feature pipeline — no DB, no models.

Two invariants we care about:
  1. The shape and column order at predict time matches fit time.
  2. Unseen categorical values route to "__other__" instead of throwing.
"""
import numpy as np
import pandas as pd
import pytest

from app.features.build import (
    FEATURE_VERSION,
    FeatureSchema,
    fit_schema,
    inverse_target,
    time_based_split,
    transform,
)


@pytest.fixture
def sample_df() -> pd.DataFrame:
    return pd.DataFrame(
        {
            "job_id":       [1, 2, 3, 4, 5, 6, 7, 8, 9, 10],
            "duration_sec": [10, 20, 30, 40, 50, 60, 70, 80, 90, 100],
            "workflow_name":["ci"] * 8 + ["release"] * 2,
            "job_name":     ["build"] * 5 + ["test"] * 3 + ["deploy"] * 2,
            "head_branch":  ["main"] * 4 + ["feature/x"] * 4 + ["release/1.0"] * 2,
            "event":        ["push"] * 7 + ["pull_request"] * 3,
            "repo_owner":   ["a"] * 10,
            "repo_name":    ["b"] * 10,
            "repo_id":      [1] * 10,
            "runner_name":  ["ubuntu-latest"] * 9 + ["macos-latest"] * 1,
            "steps_count":  [5, 6, 7, 8, 9, 10, 11, 12, 13, 14],
            "run_created_at": pd.to_datetime([
                "2026-01-01T10:00", "2026-01-02T11:00",
                "2026-01-03T12:00", "2026-01-04T13:00",
                "2026-01-05T14:00", "2026-01-06T15:00",
                "2026-01-07T16:00", "2026-01-08T17:00",
                "2026-01-09T18:00", "2026-01-10T19:00",
            ], utc=True),
        }
    )


def test_schema_version_pinned(sample_df):
    schema = fit_schema(sample_df)
    assert schema.version == FEATURE_VERSION


def test_transform_train_then_predict_shape_matches(sample_df):
    schema = fit_schema(sample_df)
    X_train, names, y = transform(sample_df, schema)
    assert X_train.shape[0] == len(sample_df)
    assert X_train.shape[1] == len(names)
    assert y is not None and y.shape[0] == len(sample_df)

    # Predict-time input is missing duration_sec — y should be None and
    # the column count must match exactly.
    predict_df = sample_df.drop(columns=["duration_sec"])
    X_pred, names_pred, y_pred = transform(predict_df, schema)
    assert y_pred is None
    assert X_pred.shape[1] == X_train.shape[1]
    assert names_pred == names


def test_unseen_category_routes_to_other(sample_df):
    schema = fit_schema(sample_df)
    out_of_vocab = sample_df.head(1).copy()
    out_of_vocab["job_name"] = "this-name-never-existed"
    X, _, _ = transform(out_of_vocab, schema)
    # The "__other__" feature for job_name should be 1 in this row.
    schema_names = schema.feature_names()
    other_col = schema_names.index("job_name=__other__")
    assert X[0, other_col] == 1.0


def test_log_target_round_trips():
    arr = np.array([0.0, 10.0, 100.0, 3600.0, 86400.0])
    log = np.log1p(arr)
    back = inverse_target(log)
    np.testing.assert_allclose(back, arr, rtol=1e-6)


def test_time_split_returns_disjoint_indexes(sample_df):
    train, test = time_based_split(sample_df, train_frac=0.7)
    assert len(set(train) & set(test)) == 0
    assert len(train) + len(test) == len(sample_df)
    # Train must precede test in time.
    train_max = sample_df.loc[train, "run_created_at"].max()
    test_min  = sample_df.loc[test,  "run_created_at"].min()
    assert train_max <= test_min


def test_schema_roundtrip_dict(sample_df):
    schema = fit_schema(sample_df)
    d = schema.as_dict()
    schema2 = FeatureSchema.from_dict(d)
    assert schema2.version == schema.version
    assert schema2.numeric_columns == schema.numeric_columns
    assert schema2.categories == schema.categories
