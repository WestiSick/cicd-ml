"""POST /export/figures — renders the dissertation PNG/PDF figure set.

Synchronous; with our data sizes (hundreds of jobs, < 200 features) the
whole set takes well under a second. The api-gateway calls this from
its `export-thesis-pack` handler after writing the CSVs.

Body:
  { "timestamp": "20260514-192202" }   # the same dir the gateway just made
"""
from __future__ import annotations

import os
from typing import Any

from fastapi import APIRouter
from pydantic import BaseModel

from ..config import load
from ..export.figures import export_all
from . import errors

router = APIRouter()


class FigureExportBody(BaseModel):
    timestamp: str


@router.post("/figures")
async def export_figures(body: FigureExportBody) -> dict[str, Any]:
    if not body.timestamp or any(c in body.timestamp for c in ("/", "\\", "..")):
        raise errors.APIError(
            status=400,
            code="invalid_timestamp",
            message="timestamp must be a flat directory name",
            user_action="The gateway should pass the same stamp it just generated.",
        )
    s = load()
    out_root = os.getenv("THESIS_OUTPUT_DIR", str(s.models_dir.parent / "thesis"))
    manifest = export_all(s.postgres_dsn, out_root, body.timestamp)
    return manifest
