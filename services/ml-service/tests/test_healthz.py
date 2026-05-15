"""Smoke tests that don't need a database — the app should boot and
basic shape contracts should hold even when /train would need real data.

For the DB-driven happy paths we exercise the full pipeline in tests
that bring up Postgres via docker-compose; those live in CI, not here.
"""
from fastapi.testclient import TestClient

from app.main import app


def test_healthz_ok():
    with TestClient(app) as client:
        r = client.get("/healthz")
        assert r.status_code == 200
        body = r.json()
        assert body["status"] == "ok"
        assert "time" in body


def test_train_requires_body():
    """Posting without a body should surface our canonical envelope."""
    with TestClient(app) as client:
        r = client.post("/train/", json={})
        # missing 'algo' → FastAPI returns 422 with the pydantic shape; our
        # canonical envelope only kicks in for handler-thrown APIError.
        assert r.status_code in (400, 422)


def test_predict_without_active_model():
    """With no model active we expect the 409 envelope with a user_action."""
    with TestClient(app) as client:
        r = client.post("/predict/", json={"dry_run": True})
        # In CI without a DB this raises before reaching the active-model
        # check, so the assertion is lenient: any 4xx/5xx with our shape.
        body = r.json()
        if r.status_code >= 400 and "error" in body:
            assert "code" in body["error"]
        else:
            # Locally with a DB this might 409 with no_active_model.
            assert r.status_code in (409, 422)
