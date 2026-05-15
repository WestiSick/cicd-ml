"""FastAPI entrypoint for ml-service.

Endpoints (scaffold — implementations follow in subsequent iterations):

    POST /train           — start training a model (background)
    POST /predict         — batch or single prediction
    GET  /models          — list trained models + metrics
    POST /models/{id}/activate — pick the active model
    GET  /metrics         — current active model's freshest test-set metrics
    GET  /healthz         — liveness

Error envelope matches the Go api-gateway contract:
    {"error": {"code": "...", "message": "...", "user_action": "..."}}
"""
from __future__ import annotations

from fastapi import FastAPI
from fastapi.responses import JSONResponse

from .config import load
from .api import errors, export, features, healthz, models, predict, train

settings = load()

app = FastAPI(
    title="cicd-ml ML service",
    version="0.1.0",
    description="Prediction and training service for CI/CD job duration models.",
)

app.include_router(healthz.router)
app.include_router(train.router, prefix="/train", tags=["train"])
app.include_router(predict.router, prefix="/predict", tags=["predict"])
app.include_router(models.router, prefix="/models", tags=["models"])
app.include_router(export.router, prefix="/export", tags=["export"])
app.include_router(features.router, prefix="/features", tags=["features"])


@app.exception_handler(errors.APIError)
async def api_error_handler(_, exc: errors.APIError) -> JSONResponse:
    """Renders APIError using the canonical envelope.

    Keeping this in one place ensures every handler that raises APIError ends
    up matching the Go-side error shape — which the frontend's API layer
    relies on to surface `user_action` to the user.
    """
    return JSONResponse(status_code=exc.status, content={"error": exc.body()})
