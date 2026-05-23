# cicd-ml

Веб-приложение для **прогнозирования времени выполнения и планирования очередей CI/CD на основе ML**. Практический артефакт магистерской диссертации.

Что внутри:

1. **Прогнозирование длительности job'ов** по историческим данным GitHub Actions — реализованы **6 алгоритмов**: Linear (Ridge), Random Forest, XGBoost, LightGBM, MLP (sklearn), LSTM (PyTorch CPU). Дополнительно — Optuna search и **walk-forward cross-validation**.
2. **Симулятор очередей** со стратегиями **FIFO / SJF / EDF / Custom**. На одном и том же потоке исторических job'ов сравниваются makespan, wait p50/p95, throughput, SLA-нарушения. Поддерживает sync HTTP-вызов и async через bg_jobs (отдельный simulator-контейнер).
3. **Real-time реакция на `git push`** через GitHub webhook → дашборд видит коммит за 1–2 секунды; на `workflow_run.completed` показывает δ% — signed prediction error с цветной подсветкой (≤10% зелёный, ≤30% жёлтый, >30% красный).
4. **Полностью UI-driven**: добавление репозиториев с **автоустановкой webhook через GitHub API**, запуск сбора с live-прогрессом (ETA, jobs/sec, rate-limit countdown), обучение моделей через wizard или быстрый запуск, **сравнение моделей бок-о-бок**, симуляции, экспорт CSV — всё в браузере.
5. **Snapshot auto-restore** для проверяющего: при наличии `db/seed/snapshot.sql.gz` на старте api-gateway заливает дамп и сразу пускает на `/dashboard` за 1-2 минуты вместо часов.
6. **Trace и контроль воркеров**: pause/resume bg-jobs runner из UI, health checks для всех сервисов (postgres / api / ml / redis / runner), cooperative cancel для тренировок.
7. **Готовый Docker Compose** для локальной разработки и прод-деплоя на VPS с доменом и автоматическим SSL (Traefik + Let's Encrypt). Поддержка cloudflared-tunnel для локального тестирования webhook'ов.
8. **i18n**: интерфейс на русском и английском, переключатель в правом верхнем углу.

---

## Быстрый старт (локально)

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

Откройте **http://localhost:5173**. Если в `db/seed/snapshot.sql.gz` есть pre-baked дамп — попадёте сразу на `/dashboard`. Иначе — на `/setup` для запуска сбора данных в фоне.

> **Совет.** В форме онбординга есть поле «GitHub Token» — без него лимит GitHub API 60 запросов/час (сбор займёт часы), с токеном `public_repo` — 5000 запросов/час (минуты). Создать: [github.com/settings/tokens](https://github.com/settings/tokens).

Для тестирования webhook'ов локально см. секцию про cloudflared-tunnel в [`docs/deployment.md`](docs/deployment.md#2-локальная-разработка--туннель-для-webhookов).

## Деплой на боевой сервер

```bash
git clone <repo-url> /opt/cicd-ml && cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod         # задать DOMAIN, LE_EMAIL, пароли, GITHUB_WEBHOOK_SECRET
chmod 600 .env.prod
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

DNS-запись `A` на ваш IP, через 30–90 секунд Traefik получит сертификат Let's Encrypt — откройте `https://<ваш-домен>`.

Подробности: [`docs/deployment.md`](docs/deployment.md).

## Документация

| Файл | О чём |
|---|---|
| [`docs/usage.md`](docs/usage.md) | Полное руководство пользователя — все сценарии в UI |
| [`docs/deployment.md`](docs/deployment.md) | Деплой на VPS, cloudflared tunnel, бэкапы, обновления, безопасность |
| [`docs/architecture.md`](docs/architecture.md) | Архитектура контейнеров, поток данных, дизайн-система |
| [`docs/thesis/README.md`](docs/thesis/README.md) | Процедура генерации фигур и скриншотов для диссертации |
| [`TODO.md`](TODO.md) | Что реализовано, что осталось, что вычеркнуто как out-of-scope |

## Структура репозитория

```
cicd-ml/
├── docker-compose.yml           # базовый стек (db, redis, api, collector, simulator, ml, frontend)
├── docker-compose.dev.yml       # dev-override (hot-reload, открытые порты)
├── docker-compose.prod.yml      # prod-override (Traefik + Let's Encrypt)
├── services/
│   ├── api-gateway/   (Go)      # REST + WS + webhook + bootstrap + bg-jobs runner
│   │   ├── cmd/api/             #   главный HTTP/WS бинарь
│   │   ├── cmd/collector/       #   collect_history/refresh воркер (отдельный контейнер)
│   │   ├── cmd/simulator/       #   simulate воркер (отдельный контейнер)
│   │   └── internal/{bgjobs,bootstrap,config,github,http,ml,scheduler,simrun,store,ws}
│   └── ml-service/    (Python)  # FastAPI: /train, /train/cv, /train/optuna, /predict,
│                                 #          /predict/from-payload, /features/build, /export/figures
├── frontend/          (React + TS + Vite)
├── db/
│   ├── migrations/                # Goose-стиль SQL, embedded в api-gateway
│   └── seed/snapshot.sql.gz       # pre-baked dump для быстрого старта (опционально)
├── ml/
│   └── notebooks/                 # EDA notebook + generate_eda_figures.py
└── docs/                          # документация + thesis-артефакты (фигуры, скриншоты)
```

`collector` и `simulator` — отдельные контейнеры/процессы, но собираются из того же Go-модуля что и `api-gateway` (multi-stage Dockerfile). Это даёт runtime-изоляцию длинных GitHub-пуллов и CPU-burst симуляций без code duplication. Все три бинаря работают через общую таблицу `bg_jobs` (SKIP LOCKED).

## Стек технологий

- **Backend (Go 1.23):** chi, pgx, go-redis, gorilla/websocket, zerolog
- **ML-сервис (Python 3.12):** FastAPI, scikit-learn, xgboost-cpu, lightgbm, optuna, **PyTorch CPU (LSTM)**, matplotlib
- **Хранилище:** PostgreSQL 16, Redis 7
- **Frontend:** React 18 + TypeScript + Vite, Radix UI, visx, sonner, TanStack Query, собственный i18n (en/ru)
- **Деплой:** Docker Compose, Traefik v3, Let's Encrypt

## Реализованные модели и стратегии

**Модели (`/experiments`)**: Linear (Ridge), Random Forest, XGBoost, LightGBM, MLP (sklearn), **LSTM (PyTorch)**. Каждая отдаёт MAE/RMSE/MAPE/R² + Spearman + **NDCG@10/50/100** (последнее критично для SJF — оценивает качество ранжирования).

**Hyperparameter tuning**:
- Быстрый запуск с дефолтами;
- Optuna search (10/30/50/100 trials, TPE-sampler);
- **Walk-forward cross-validation** (3/5/8 фолдов) с per-fold + mean ± std метриками;
- **Full wizard** — 4 шага: Algorithm → Dataset (репо чекбоксы + slider периода) → Hyperparameters (sliders) → Train/test split (interactive cutoff timeline с bar-chart дневных counts).

**Сравнение моделей** (`/experiments/compare?ids=1,2,3`): metrics table с подсветкой лучшего, overlaid scatter predicted vs actual, top-features bar chart с per-модель значениями.

**Стратегии (`/simulator`)**: FIFO, SJF, EDF (с per-branch SLA), Custom (взвешенный score). Симулятор event-driven, на 500+ job'ах работает < 1 секунды. Веса Custom настраиваются на `/admin → Settings`.

**Экспорт**:
- **`/datasets/{id} → Export CSV`** — все job'ы репо со всеми численными фичами для pandas/Excel;
- **`/simulator → строка → Export CSV`** — отдельный sim_run для pgfplotstable в LaTeX;
- **`/experiments → Export thesis pack`** — целевой `docs/thesis/` с CSV и PNG/PDF фигурами;
- **`make eda-figures`** — генерирует 5 EDA-фигур для Главы 3 диссертации (distribution, top jobs, branch class, hour-of-day, correlation matrix) через `ml/notebooks/generate_eda_figures.py`.

## Реализованные features (для feature engineering)

Все фичи — pure Python, в `services/ml-service/app/features/build.py`. Версионируются через `FEATURE_VERSION`.

| Группа | Фичи |
|---|---|
| Time | hour_of_day, day_of_week, is_weekend |
| Branch | branch_is_main, branch_is_release, branch_is_feature |
| Categorical (top-K one-hot) | workflow_name, job_name, head_branch, event, repo_owner, repo_name, runner_name |
| Numeric base | steps_count, log_repo_avg_30d |
| **Rolling per (repo, job_name)** | log_jobname_median_7d, log_jobname_median_30d, jobname_runs_30d |
| **Rolling per author** | log_author_p50_30d, log_author_p90_30d, author_commits_30d |
| **Commit diff** | log_commit_files_changed, log_commit_additions, log_commit_deletions |

Commits собираются автоматически коллектором при синхронизации репо (через `GitHub /repos/{owner}/{repo}/commits/{sha}` с deduplication по SHA).

## Лицензия

Академическое использование в рамках магистерской диссертации. Переиспользование с указанием авторства разрешено.
