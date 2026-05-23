# syntax=docker/dockerfile:1.7

FROM python:3.12-slim AS base
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1
RUN apt-get update && apt-get install -y --no-install-recommends \
      libgomp1 \
 && rm -rf /var/lib/apt/lists/*
COPY requirements.txt .
# --prefer-binary picks wheels over sdists when both exist (so we don't
# compile from source on slow WSL2 disks). We deliberately do NOT pass
# --only-binary :all: — combined with WSL2-flaky downloads it has
# triggered spurious "hash mismatch" failures even on packages that
# don't pin hashes. Retry once on transient errors.
RUN pip install --prefer-binary -r requirements.txt \
 || pip install --prefer-binary -r requirements.txt

# ---- dev ----
FROM base AS dev
COPY . .
EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--reload"]

# ---- prod ----
# All wheels we need (torch-cpu, xgboost, lightgbm, scikit-learn,
# lightgbm) are prebuilt on manylinux2014, so the base image doesn't
# install a compiler — and prod has nothing to purge. Earlier versions
# of this file ran `apt-get purge build-essential` here, which broke
# the build (exit 100 — package was never installed). Don't re-add
# unless you also reintroduce build-essential in `base`.
FROM base AS prod
COPY . .
EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "2"]
