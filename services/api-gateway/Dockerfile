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

# ---- build ----
FROM base AS build
COPY services/api-gateway/ .
COPY db/migrations ./internal/store/migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- prod ----
FROM gcr.io/distroless/static-debian12 AS prod
WORKDIR /
COPY --from=build /out/api /api
COPY db/migrations /migrations
EXPOSE 8080
ENTRYPOINT ["/api"]
