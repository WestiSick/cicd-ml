.PHONY: help up down dev prod logs ps psql redis-cli build test lint clean snapshot restore-snapshot

help:
	@echo "cicd-ml — make targets"
	@echo "  make dev               Start in dev mode (hot-reload, exposed ports)"
	@echo "  make prod              Start in prod mode (Traefik + Let's Encrypt)"
	@echo "  make down              Stop everything"
	@echo "  make logs              Tail all logs"
	@echo "  make ps                Show running services"
	@echo "  make psql              Open psql shell"
	@echo "  make redis-cli         Open redis-cli"
	@echo "  make build             Rebuild all images"
	@echo "  make test              Run all tests"
	@echo "  make lint              Run all linters"
	@echo "  make snapshot          Dump DB to db/seed/snapshot.sql.gz"
	@echo "  make restore-snapshot  Restore DB from db/seed/snapshot.sql.gz"
	@echo "  make clean             Remove containers and volumes (DESTRUCTIVE)"

# Compose file selection
DEV_COMPOSE  = -f docker-compose.yml -f docker-compose.dev.yml
PROD_COMPOSE = -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod

dev:
	docker compose $(DEV_COMPOSE) up -d --build

prod:
	docker compose $(PROD_COMPOSE) up -d --build

up: dev

down:
	docker compose $(DEV_COMPOSE) down

logs:
	docker compose $(DEV_COMPOSE) logs -f --tail=200

ps:
	docker compose $(DEV_COMPOSE) ps

psql:
	docker compose $(DEV_COMPOSE) exec db psql -U cicdml -d cicdml

redis-cli:
	docker compose $(DEV_COMPOSE) exec redis redis-cli

build:
	docker compose $(DEV_COMPOSE) build

test:
	docker compose $(DEV_COMPOSE) exec api go test ./... ; \
	docker compose $(DEV_COMPOSE) exec ml pytest

# Smoke-test the ML pipeline against the live stack. Useful after a fresh
# `make dev` to confirm api ⟷ ml ⟷ db wiring before recording any
# dissertation experiments.
smoke-ml:
	@echo "=== ml /healthz ==="
	@curl -fsS http://localhost:8000/healthz | head -c 200; echo
	@echo "=== api /api/models (baseline expected empty) ==="
	@curl -fsS http://localhost:8080/api/models | head -c 300; echo
	@echo "=== api POST /api/training (algo=linear, activate=true) ==="
	@curl -fsS -X POST http://localhost:8080/api/training \
	  -H 'Content-Type: application/json' \
	  -d '{"algo":"linear","activate":true,"name":"linear-smoke"}' ; echo
	@echo "=== sleep 8s for bg_job to run ==="
	@sleep 8
	@echo "=== api /api/models ==="
	@curl -fsS http://localhost:8080/api/models | head -c 1200; echo
	@echo "=== api POST /api/simulator/run (all strategies, with ML predictions) ==="
	@curl -fsS -X POST http://localhost:8080/api/simulator/run \
	  -H 'Content-Type: application/json' \
	  -d '{"window_start":"2025-01-01T00:00:00Z","window_end":"2026-06-01T00:00:00Z","runners":2,"sla_main_sec":1800,"sla_feature_sec":7200}' ; echo

lint:
	docker compose $(DEV_COMPOSE) exec api golangci-lint run ./... ; \
	docker compose $(DEV_COMPOSE) exec ml ruff check . ; \
	docker compose $(DEV_COMPOSE) exec frontend npm run lint

snapshot:
	docker compose $(DEV_COMPOSE) exec -T db pg_dump -U cicdml cicdml | gzip > db/seed/snapshot.sql.gz
	@echo "Snapshot saved to db/seed/snapshot.sql.gz"

restore-snapshot:
	gunzip -c db/seed/snapshot.sql.gz | docker compose $(DEV_COMPOSE) exec -T db psql -U cicdml -d cicdml

clean:
	docker compose $(DEV_COMPOSE) down -v
