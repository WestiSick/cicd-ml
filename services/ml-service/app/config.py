"""Service configuration via environment variables (12-factor).

Mirrors the Go api-gateway config style: all settings come from env vars
passed by the compose files. Defaults are dev-friendly; prod values come
from `.env.prod`.
"""
from __future__ import annotations

import os
from pathlib import Path

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    postgres_dsn: str = "postgresql://cicdml:cicdml_dev_password@db:5432/cicdml"
    models_dir: Path = Path("/var/lib/cicdml/models")
    ml_port: int = 8000
    log_level: str = "info"

    class Config:
        env_file = ".env"


def load() -> Settings:
    dsn = os.getenv("POSTGRES_DSN", Settings().postgres_dsn)
    # The Go side uses `postgres://...` (pgx-friendly); SQLAlchemy only
    # recognises `postgresql://` as a dialect entry-point. Normalise here
    # so the same compose env var works for both languages.
    if dsn.startswith("postgres://"):
        dsn = "postgresql://" + dsn[len("postgres://"):]
    s = Settings(
        postgres_dsn=dsn,
        models_dir=Path(os.getenv("MODELS_DIR", str(Settings().models_dir))),
        ml_port=int(os.getenv("ML_PORT", "8000")),
        log_level=os.getenv("LOG_LEVEL", "info"),
    )
    s.models_dir.mkdir(parents=True, exist_ok=True)
    return s
