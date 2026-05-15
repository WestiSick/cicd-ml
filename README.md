# cicd-ml

Intelligent CI/CD execution-time prediction and queue scheduling, based on ML.

This is the practical artifact for the master's thesis  
**"Development of an intelligent system for predicting execution time and planning CI/CD queues based on ML"**.

The system:

1. Predicts the duration of CI/CD jobs from historical GitHub Actions data.
2. Schedules a build queue using those predictions, comparing **FIFO / SJF / EDF / Custom** strategies.
3. Reacts to real `git push` events in real time and shows them on a dashboard with predicted vs actual time.
4. Trains models, ingests datasets, runs simulations — **entirely from the web UI**. No CLI required for normal use.
5. Is packaged as a single Docker Compose stack for both local development and a domain-bound VPS deployment with automatic SSL.

---

## Quick start (local)

```powershell
# Windows PowerShell
git clone <repo-url> cicd-ml
cd cicd-ml
copy .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

```bash
# Linux / macOS
git clone <repo-url> cicd-ml && cd cicd-ml
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

Then open **http://localhost:5173** and complete the onboarding flow. The
backend will collect history and pre-train all selected models in the
background. You can close the tab — progress resumes on return.

> **Tip:** Provide a GitHub Personal Access Token in the onboarding form to
> raise the API rate limit from 60 to 5000 req/h. Create one at
> [github.com/settings/tokens](https://github.com/settings/tokens) with the
> `public_repo` scope.

## Production deployment

Deploy to a VPS with a domain and free Let's Encrypt certificate:

```bash
# On the VPS
git clone <repo-url> /opt/cicd-ml && cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod     # set DOMAIN, LE_EMAIL and strong secrets
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Point a DNS `A` record to your VPS IP, wait 30–90 seconds for the certificate,
then open `https://<your-domain>`.

Full deployment guide: [`docs/deployment.md`](docs/deployment.md).

## Documentation

| File | What it covers |
|---|---|
| [`docs/usage.md`](docs/usage.md)           | Step-by-step UI scenarios |
| [`docs/deployment.md`](docs/deployment.md) | VPS deployment, backups, updates, security |
| [`docs/architecture.md`](docs/architecture.md) | System architecture, data flow, design system |

## Repository layout

```
cicd-ml/
├── docker-compose.yml           # base stack
├── docker-compose.dev.yml       # dev override (hot reload, exposed ports)
├── docker-compose.prod.yml      # prod override (Traefik + Let's Encrypt)
├── services/
│   ├── api-gateway/   (Go)      # REST + WebSocket, webhook receiver, scheduler
│   ├── collector/     (Go)      # GitHub Actions ingestion worker
│   ├── simulator/     (Go)      # historical strategy replay
│   └── ml-service/    (Python)  # FastAPI: train / predict / models
├── frontend/          (React + TS + Vite)
├── db/migrations/                # goose migrations
├── ml/                           # notebooks, feature pipeline, model registry
└── docs/                         # documentation + thesis artifacts
```

## Tech stack

- **Backend (Go 1.23):** chi, pgx, go-redis, gorilla/websocket
- **ML service (Python 3.12):** FastAPI, scikit-learn, XGBoost, LightGBM, PyTorch, Optuna
- **Storage:** PostgreSQL 16, Redis 7
- **Frontend:** React 18 + TypeScript + Vite, Radix UI, visx, sonner, react-query
- **Deploy:** Docker Compose, Traefik v3, Let's Encrypt

## License

For academic use as part of a master's thesis. Re-use permitted with attribution.
