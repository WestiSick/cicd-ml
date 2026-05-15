"""POST /features/build — materialise per-job feature vectors into Postgres.

Endpoint exists so the api-gateway's compute_features bg_job can stop
being a stub. The training pipeline doesn't yet read from this table
(it still recomputes from raw jobs every fit), but the rows are useful
on their own for the /datasets/:id feature-preview pane.
"""
from __future__ import annotations

from typing import Any

from fastapi import APIRouter
from pydantic import BaseModel

from ..config import load
from ..features.materialize import materialize_all

router = APIRouter()


class BuildFeaturesBody(BaseModel):
    repo_ids: list[int] | None = None


@router.post("/build")
async def build_features(body: BuildFeaturesBody) -> dict[str, Any]:
    s = load()
    return materialize_all(s.postgres_dsn, repo_ids=body.repo_ids)
