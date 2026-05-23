# Архитектура

Архитектура системы `cicd-ml` — практического артефакта магистерской диссертации по ML-предсказанию и планированию очередей CI/CD.

## Контейнеры в проде

```
                       ┌────────────┐
                       │  Traefik   │── 80/443 ── публичный домен
                       └─────┬──────┘
                             │ HTTPS + Let's Encrypt
            ┌────────────────┼─────────────────┐
            │                │                 │
       ┌────▼────┐      ┌────▼─────┐     ┌─────▼─────┐
       │   api   │      │ frontend │     │ /webhooks │
       │ :8080   │      │  :80     │     │  /github  │
       └────┬────┘      └──────────┘     └───────────┘
            │ docker-сеть, недоступна извне
   ┌────────┼─────────────┬─────────────┐
   │        │             │             │
┌──▼──┐  ┌──▼──┐    ┌─────▼─────┐ ┌─────▼─────┐
│ ml  │  │ db  │    │ collector │ │ simulator │
└─────┘  └─────┘    └───────────┘ └───────────┘
         Postgres   bg_jobs:        bg_jobs:
                    collect_history simulate
                    + refresh
```

- **api-gateway** (Go, chi) — REST + WebSocket, GitHub webhook receiver, HMAC validation, bootstrap orchestrator, bg-jobs runner для `bootstrap`/`compute_features`/`train_model`. Внутри также: simulator-engine, scheduler, snapshot auto-restore.
- **collector** (Go) — отдельный воркер, забирает `collect_history`/`refresh` bg_jobs. Длинные GitHub-пуллы не блокируют user-facing HTTP traffic.
- **simulator** (Go) — отдельный воркер для `simulate` bg_jobs. CPU-burst задачи изолированы от gateway.
- **ml** (Python, FastAPI) — `/train`, `/train/cv`, `/train/optuna`, `/predict`, `/predict/from-payload`, `/features/build`, `/export/figures`. 6 моделей за единым `BaseModel` API.
- **db** — Postgres 16.
- **redis** — Redis 7 (зарезервирован под распределённую очередь; сейчас только в health-check).
- **frontend** — собранный React-bundle через nginx.

`collector` и `simulator` собираются из того же Go-модуля что и api-gateway (`services/api-gateway`). Это — разные `cmd/{collector,simulator}/main.go` бинари, выпеченные одним Dockerfile через multi-stage. Так избегаем code-duplication.

Воркеры пушат WS-broadcast обратно в gateway через `POST /api/internal/broadcast` — эндпоинт закрыт от внешнего доступа Traefik-правилами.

## Поток данных

1. **Подключение репозитория.** Пользователь добавляет репо через `POST /api/repos` или `/setup`. api-gateway:
   - регистрирует репо в `repos`;
   - автоматически энкьюит `bg_job` типа `collect_history`;
   - параллельно фоном вызывает `EnsureWebhook` через GitHub API — если PAT даёт права, webhook ставится автоматически (статус виден на карточке репо).

2. **Сбор данных.** **Collector**-контейнер забирает `collect_history` из `bg_jobs` (SKIP LOCKED). Последовательно тянет страницы GitHub Actions API, делает UPSERT в `workflow_runs`, `jobs`, `commits` с чекпоинтами. При rate-limit (403/429) ждёт reset, прогресс сериализуется в `bg_jobs.logs_tail` как JSON `{phase, page, runs_seen/total, jobs_per_sec, eta_seconds, rate_remaining/limit, rate_reset_unix}`. Фронт читает это на /datasets как живую прогресс-полоску.

3. **Извлечение фич.** `compute_features` bg_job → api-gateway проксирует в ml-service `/features/build`. ml-service читает `jobs ⨝ workflow_runs ⨝ repos ⨝ commits`, считает feature_vector по каждому job'у:
   - **Time**: hour_of_day, day_of_week, is_weekend
   - **Branch class**: main / release / feature one-hot
   - **Categorical** (top-K one-hot): workflow_name, job_name, head_branch, event, repo_owner, repo_name, runner_name
   - **Numeric**: steps_count, log_repo_avg_30d
   - **Rolling** per (repo, job_name): `log_jobname_median_7d/30d`, `jobname_runs_30d`
   - **Author**: `log_author_p50/p90_30d`, `author_commits_30d`
   - **Commit diff**: `log_commit_files_changed/additions/deletions`

   Пишет в `features` (JSONB + версия схемы).

4. **Обучение.** `train_model` bg_job. api-gateway зовёт ml-service `/train` или `/train/optuna`. ml-service:
   - читает features + target из БД;
   - time-based split (80/20) — `time_based_split`;
   - обучает выбранную модель: **Linear, RF, XGBoost, LightGBM, MLP (sklearn), LSTM (PyTorch CPU)** — все за единым `BaseModel` интерфейсом;
   - стримит per-iteration метрики в `training_metrics` через `POST /api/internal/training/{id}/metric`;
   - сохраняет артефакт в shared volume `model-artifacts`;
   - записывает строку в `models` с метриками (MAE, RMSE, MAPE, R², Spearman, **NDCG@10/50/100**) и feature_importance;
   - предсказывает на тестовой выборке → пишет в `predictions`.

   Альтернатива: `/train/cv` — walk-forward cross-validation, возвращает per-fold + mean ± std метрики, не персистит модель.

5. **Predict (webhook).** На `workflow_run.requested` api-gateway зовёт `ml-service /predict/from-payload` (single-row inference из webhook-данных, без БД). Сохраняет прогноз в in-memory `predictionCache` (TTL=30 мин). Броадкастит на `/ws/queue` с полем `predicted_sec`.

6. **Δ-error (webhook completed).** На `workflow_run.completed` api-gateway вычисляет `actual_sec = updated_at - run_started_at`, ищет прогноз в кэше по `(repo, run_id)`, считает `delta_pct = 100 * (predicted - actual) / actual`. Броадкаст с обоими полями → фронт показывает δ% с цветной индикацией (≤10% зелёный, ≤30% жёлтый, >30% красный).

7. **Планирование.** Активная стратегия (FIFO/SJF/EDF/Custom) сидит в `system_state`. Сейчас стратегия выбирается на странице /admin и применяется в `simrun.Run` симуляторе. Прод-режим (Redis sorted-set с реальной диспетчеризацией) — out of scope диссертации.

8. **Симуляция.** Два режима:
   - **Sync HTTP**: `POST /api/simulator/run` → `simrun.Run` (синхронно, <1с) → запись в `sim_runs`. Использует `internal/scheduler` для алгоритмов.
   - **Async bg_job**: `simulate` kind, который **simulator**-контейнер забирает из bg_jobs. Тот же `simrun.Run`. Bootstrap-orchestrator энкьюит такой job в финальной фазе.

## Канонический формат ошибок (UI feedback contract)

Каждый эндпоинт — Go или Python — возвращает ошибки в одной форме:

```json
{
  "error": {
    "code": "github_rate_limited",
    "message": "GitHub API rate limit exceeded",
    "details": { "reset_at": "2026-05-14T19:42:00Z" },
    "user_action": "Wait until reset or add a GitHub token in /admin → Settings."
  }
}
```

Фронтенд читает `user_action` и показывает его в toast'е через sonner. Стек-трейсы и внутренние коды в UI не попадают.

`ApiErrorBoundary` ловит любую необработанную render-ошибку и показывает «Reload» с tooltip, чтобы пользователь не упёрся в белый экран.

## bg_jobs: универсальный механизм фоновых задач

Таблица `bg_jobs` — единственный источник истины для любых долгих операций. Воркеры читают свой kind, обновляют `progress / total / message / logs_tail`, runner транслирует каждое изменение в `/ws/bg-jobs`. Фронт подписывается один раз — карточки прогресса появляются везде, где они логически уместны.

**Пулы воркеров в api-gateway** (избегает head-of-line blocking):

| Pool    | Воркеров | Kinds                                                        |
|---------|----------|---------------------------------------------------------------|
| io      | 1        | `collect_history`, `refresh` (GitHub rate-limit делает параллелизм вредным) |
| compute | 3        | `bootstrap`, `compute_features`, `train_model`, `simulate`    |

**Когда деплоятся отдельные контейнеры** (collector + simulator):
- gateway ставит `ENABLED_BG_KINDS=bootstrap,compute_features,train_model` через env-var → его пулы фильтруются (`RestrictKinds`), не претендуют на kinds, обслуживаемые внешними воркерами;
- collector регистрирует handlers для `collect_history`/`refresh` и берёт через SKIP LOCKED;
- simulator — для `simulate`.

Можно также пускать в **single-binary mode** (gateway берёт все kinds): просто не запускать контейнеры collector/simulator и убрать `ENABLED_BG_KINDS`. Тот же код, та же таблица.

**Cancel + Pause:**
- Каждый bg_job можно остановить через `POST /api/bg-jobs/{id}/cancel`. Воркер кооперативно завершает работу через ctx-cancel watcher (раз в секунду проверяет `bg_jobs.status`).
- Глобально приостановить всех воркеров (отложить новые claims, в-flight продолжают) — через `POST /api/admin/bg-jobs/pause`. Видно на /admin → System health.

**Bootstrap-orchestrator** живёт в `compute` пуле и сам ждёт между фазами через polling `bg_jobs.status` — это гарантирует, что `train_model` не запустится пока `collect_history` не завершилась.

**Snapshot auto-restore.** На старте api-gateway, после `Migrate()`, проверяет `/var/lib/cicdml/seed/snapshot.sql.gz`. Если файл есть и `bootstrap_done=false` — gunzip + multi-statement Exec через pgx + флаг в той же транзакции. Прод-демо за минуту вместо часов.

## WebSocket-каналы

| Канал | Назначение |
|---|---|
| `/ws/bootstrap`        | Прогресс bootstrap-чейна. |
| `/ws/bg-jobs`          | Прогресс любого фонового job'а. Подписаны: /datasets (sync), /experiments (training). |
| `/ws/queue`            | Live-очередь + webhook-feed для /dashboard. |
| `/ws/training/:id`     | Per-iteration loss/RMSE для конкретного training run. |

Все каналы транслируют JSON. api-gateway держит in-process pub/sub (sync.RWMutex + buffered channels). Когда воркеры в отдельных контейнерах, они пушат через `POST /api/internal/broadcast` (HTTP-обёртка над тем же pub/sub).

## База данных

Ключевые таблицы (миграции `db/migrations/`, embedded в api-gateway через `go:embed`):

- `repos` — подключённые репозитории, статус сбора, denormalised counters + **webhook tracking** (`webhook_id`, `webhook_url`, `webhook_status`, `webhook_error`).
- `workflow_runs`, `jobs`, `commits` — сырые данные GitHub Actions.
- `features` — материализованные feature_vectors (JSONB + версия схемы).
- `models` — обученные модели + метрики + feature_importance.
- `predictions` — `(job_id, model_id) → predicted_sec`.
- `bg_jobs` — все фоновые операции с прогрессом.
- `training_metrics` — per-iteration `(train_loss, val_rmse, val_mae)`.
- `sim_runs` — результаты симуляций стратегий.
- `webhook_events` — последние 50 GitHub webhook'ов с результатом HMAC.
- `activity_log` — журнал пользовательских действий для /admin → Activity log.
- `system_state` — single-row settings: `bootstrap_done`, `active_strategy`, `custom_weights`, `github_pat`, `bg_jobs_paused`.

Миграции применяются автоматически на старте gateway. Внешний инструмент типа goose-CLI не нужен.

## Канонические UI-страницы

| Path | Назначение | Ключевые компоненты |
|---|---|---|
| `/setup` | Онбординг при пустой БД. PAT, репо, период, модели → 4-фазный bootstrap | `SetupProgress`, `useActiveBootstrap` |
| `/dashboard` | Live-очередь + KPI: active model, strategy, mean duration (с 24h sparkline) | `QueueCard`, `SparklineChart`, `useDashboardQueue` |
| `/datasets` | Карточки репо с автоустановкой webhook'а, live sync-прогресс, heatmap покрытия | `WebhookBadge`, `SyncProgressStrip`, `HeatmapChart` |
| `/datasets/:id` | Per-repo stats: distribution, top workflows/jobs, branches, conclusions, **feature matrix preview**, CSV export | `BarChart`, `FeaturePreview` |
| `/experiments` | Trained models таблица; быстрый запуск + Optuna + **walk-forward CV** + **полный TrainWizard** | `CVResultTable`, `TrainWizard`, `ModelRowEl` |
| `/experiments/compare?ids=...` | Head-to-head метрики моделей + overlaid scatter + feature importance overlay | `ModelCompare` |
| `/experiments/jobs/:id` | Live training: loss-curve, scatter, residuals, logs, Cancel | `LineChart`, `ScatterPlot`, `ResidualsPlot` |
| `/simulator` | Replay стратегий на окне истории, CSV-экспорт каждого run | `BarChart`, `ChartCard` |
| `/admin` | Settings (strategy/weights/PAT), Activity log, System health с pause/resume bg-jobs, Webhooks log | `SettingsBlock`, `BGRunnerToggle` |

Командная палитра **Cmd/Ctrl+K** даёт быстрый jump по страницам + shortcut'ы `Alt+D/S/E/I/A` для прыжков на dashboard/datasets/experiments/simulator/admin.

## Дизайн-система

- Дизайн-токены в `frontend/src/styles/tokens.css` — единственное место, где определены цвета, шрифты, размеры.
- Два шрифта: **Inter / Geist** (UI/display) + **JetBrains Mono** (все технические значения: id, sha, sec, метрики).
- Один акцентный цвет: тёплый янтарь `#F2C94C`. Статусные цвета (зелёный/жёлтый/красный/синий) — только для статусов.
- Углы 6–8px, без теней, без градиентов. Плотность Linear-уровня (строка таблицы 36–40 px).
- Анимации: 120 мс hover, 180 мс entry, 240 мс modal. Без bouncy-springs.
- Никаких shadcn-дефолтов, MUI-дефолтов, фиолетово-синих градиентов и glassmorphism.

## i18n

Своя реализация без библиотек, всё в `frontend/src/i18n/`:

- `types.ts` — TypeScript-union всех ключей. Если в `en.ts` или `ru.ts` пропущен ключ — TS падает на билде.
- `en.ts` / `ru.ts` — плоские словари.
- `index.tsx` — `LocaleProvider`, `useT()`, `<LanguageSwitcher />`.

Локаль хранится в `localStorage["cicd-ml.locale"]`. Первый визит автоопределяется по `navigator.language`.

`<LanguageSwitcher compact />` смонтирован в шапке `AppShell` (доступен на любой странице кроме `/setup`) и в `/setup` (правый верхний угол).

## Внутренние Go-пакеты (api-gateway)

```
services/api-gateway/
├── cmd/
│   ├── api/main.go         — основной HTTP/WS бинарь
│   ├── collector/main.go   — collect_history/refresh воркер
│   └── simulator/main.go   — simulate воркер
└── internal/
    ├── bgjobs/             — пулы воркеров, Broadcaster (HubBroadcaster + HTTPBroadcaster)
    ├── bootstrap/          — orchestrator + StubHandlers + FinishOnDone
    ├── config/             — env-driven Config struct
    ├── github/             — Client (REST), Syncer (collect_history), Installer (webhook)
    ├── http/               — chi-routes + handlers
    ├── ml/                 — HTTP client до ml-service
    ├── scheduler/          — FIFO/SJF/EDF/Custom + event-driven Sim engine
    ├── simrun/             — обёртка scheduler+store для синхронного запуска (используется и HTTP, и simulator-воркером)
    ├── store/              — pgx-based DB layer + migrations (embedded)
    └── ws/                 — Hub (pub/sub) + serve upgrade
```

## Почему Go + Python (а не один язык)

- **Go** для всего I/O-bound: HTTP-сервер, Postgres pool, ингест GitHub API, WebSocket fan-out, scheduler. Дешёвые горутины, предсказуемая латентность.
- **Python** для ML: каждая нужная библиотека (XGBoost, LightGBM, scikit-learn, Optuna, PyTorch, matplotlib) здесь best-in-class. Ad-hoc анализ в `ml/notebooks/` использует то же окружение что и сервис.

Граница узкая: api-gateway зовёт ml-service через HTTP (JSON в обе стороны). Никакого shared-кода — только схема БД и формат ошибок выступают контрактом.
