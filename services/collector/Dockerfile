# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS base
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY services/collector/go.mod services/collector/go.sum* ./
RUN go mod download || true

FROM base AS dev
RUN go install github.com/air-verse/air@v1.61.7
COPY services/collector/ .
CMD ["air", "-c", ".air.toml"]

FROM base AS build
COPY services/collector/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector

FROM gcr.io/distroless/static-debian12 AS prod
COPY --from=build /out/collector /collector
ENTRYPOINT ["/collector"]
