# syntax=docker/dockerfile:1.7

# ---- common builder ----
FROM golang:1.23-alpine AS base
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY services/api-gateway/go.mod services/api-gateway/go.sum* ./
RUN go mod download || true

# ---- dev (hot reload via air) ----
# Migrations are embedded into the api binary (go:embed) — we don't need
# the goose CLI inside the container. The Makefile's `make migrate` target
# is therefore optional / dev-convenience only.
FROM base AS dev
RUN go install github.com/air-verse/air@v1.61.7
COPY services/api-gateway/ .
# Bring the canonical migrations into the embed path so `go:embed` finds them.
# Single source of truth lives in /db/migrations at repo root.
COPY db/migrations ./internal/store/migrations
EXPOSE 8080
CMD ["air", "-c", ".air.toml"]

# ---- build (all three binaries from the same module) ----
# api: HTTP/WS gateway + bootstrap orchestrator + bg-jobs runner
# collector: standalone collect_history/refresh worker
# simulator: standalone simulate worker
#
# Building all three in one stage keeps the image cache hot — go mod
# download is shared, source copy is shared, only the final `go build`
# is per-binary.
FROM base AS build
COPY services/api-gateway/ .
COPY db/migrations ./internal/store/migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api       ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator

# ---- prod: api (the default — preserves existing image contract) ----
FROM gcr.io/distroless/static-debian12 AS prod
WORKDIR /
COPY --from=build /out/api /api
COPY db/migrations /migrations
EXPOSE 8080
ENTRYPOINT ["/api"]

# ---- prod: collector (separate image so the compose service runs only this binary) ----
FROM gcr.io/distroless/static-debian12 AS collector-prod
WORKDIR /
COPY --from=build /out/collector /collector
ENTRYPOINT ["/collector"]

# ---- prod: simulator ----
FROM gcr.io/distroless/static-debian12 AS simulator-prod
WORKDIR /
COPY --from=build /out/simulator /simulator
ENTRYPOINT ["/simulator"]

# ---- dev variants for the worker binaries — air-watching the api
#       source tree, but with a different entrypoint per service.
FROM base AS collector-dev
RUN go install github.com/air-verse/air@v1.61.7
COPY services/api-gateway/ .
COPY db/migrations ./internal/store/migrations
CMD ["air", "-c", ".air.collector.toml"]

FROM base AS simulator-dev
RUN go install github.com/air-verse/air@v1.61.7
COPY services/api-gateway/ .
COPY db/migrations ./internal/store/migrations
CMD ["air", "-c", ".air.simulator.toml"]
