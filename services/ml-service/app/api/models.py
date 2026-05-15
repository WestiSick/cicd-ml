"""GET /models — list trained models + metadata.

Reads directly from Postgres (no cache) so /experiments always reflects
the latest state. Volume is tiny (dozens of rows at most).
"""
from __future__ import annotations

from typing import Any

from fastapi import APIRouter

from ..config import load
from ..storage import db
from . import errors

router = APIRouter()


@router.get("/")
async def list_models() -> dict[str, Any]:
    s = load()
    rows = db.list_models_rows(s.postgres_dsn)
    return {"models": rows}


@router.post("/{model_id}/activate")
async def activate_model(model_id: int) -> dict[str, Any]:
    s = load()
    rows = db.list_models_rows(s.postgres_dsn)
    if not any(int(r["id"]) == model_id for r in rows):
        raise errors.APIError(
            status=404,
            code="model_not_found",
            message=f"No model with id {model_id}",
            user_action="Refresh the page — the model may have been deleted.",
        )
    db.set_active_model(s.postgres_dsn, model_id)
    return {"active_model_id": model_id}
