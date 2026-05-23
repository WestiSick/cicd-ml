# TODO — что ещё не сделано из `plan.md`

Дата сверки: 2026-05-23 (после реализации Phase A–D из «out of scope + мелкие пробелы»).

**Все «🔴 Критично» и все «🟡 Важно» — реализованы**. Также реализованы 6 из 7 «🟢 Out of scope» — все, что давали ощутимое улучшение. Осталось одно out-of-scope (JWT auth) + одно nice-to-have (расширенные Python тесты).

---

## 🟢 Что осталось из «Out of scope»

### 1. JWT auth с Secure+HttpOnly cookies — ⛔ намеренно НЕ сделано
- **Что есть:** `JWT_SECRET` читается из env, но middleware нет.
- **Почему не делал:** single-user thesis prototype; multi-tenant и аутентификация вынесены в out-of-scope ещё в `plan.md`.
- **Если рецензент спросит:** «threat-model thesis-прототипа = «один пользователь на машине», поэтому auth не реализован; добавление одно-средство /api/login + middleware занимает день при необходимости».
- **Размер при реализации:** S.

### 3. Self-hosted runners интеграция — 🔜 отложено по решению автора
- **Что есть:** scheduler/симулятор сравнивают стратегии FIFO/SJF/EDF/Custom на исторических данных (replay-режим), задачи в активной очереди только наблюдаются (нельзя пере-упорядочить уже отправленные GitHub-actions).
- **Что нужно:**
  - `docs/self-hosted-runners.md` с архитектурой: how the scheduler gates a self-hosted GitHub Actions runner via labels/admission, описание ограничений (нельзя preempt уже выполняющийся job), trade-offs vs simulation-only mode.
  - POC: Redis-backed mock dispatcher + simulator harness, без реально зарегистрированного runner'а.
  - Optionally: real runner-container, который polls GitHub и принимает job только когда scheduler greenlight'ит (через sidecar-gate на Redis).
- **Почему не делал в этой итерации:** «skip for now» от автора 2026-05-23 — две другие фичи (push-recs heatmap + commit-content features) даюли больше ROI к защите.
- **Если рецензент спросит:** «scheduler доказывает свою ценность на симуляторе (Chapter 4 сравнение стратегий); интеграция с реальным GitHub Actions runner'ом — линейное расширение, описанное в Chapter 5 «расширяемость»».
- **Размер:** L (real runner) / M (POC + docs only).

### 2. Python тесты на модели + testcontainers integration — ⚠️ частично
- **Что есть:** 3 теста (`test_features`, `test_healthz`, `test_optuna_search`).
- **Что не хватает:**
  - `tests/test_models.py` параметризованный по 6 алгоритмам (fit на 200 строк, predict, save→load, проверка metrics shape);
  - integration `/predict` через TestClient + monkeypatched DB;
  - включить `pytest` и `frontend lint` в CI;
  - testcontainers с Postgres + Redis для e2e: webhook → predict → enqueue → simulate.
- **Аргумент:** «Go-side покрыт 9 файлами unit-тестов (стратегии, sim, ws/hub, store, bgjobs runner, github client+webhook); e2e — через `make smoke-ml`».
- **Размер:** S–M (стоит сделать; gate качества почти бесплатный).

---

## ✅ Сделано в этой итерации (1 крупный + все мелкие)

### Phase A — Отдельные процессы collector/simulator

**Архитектура.** `services/collector` и `services/simulator` каркасы удалены, их заменили `cmd/collector` и `cmd/simulator` в Go-модуле `api-gateway`. Это даёт runtime-изоляцию длинных GitHub-пуллов и CPU-burst симуляций без code duplication: тот же `internal/github`, `internal/scheduler`, `internal/store`, `internal/bgjobs`, `internal/simrun`.

**Multi-stage Dockerfile.** `services/api-gateway/Dockerfile` теперь выдаёт три бинаря в одном build-stage:
- `api` (gateway) — без изменений;
- `collector` (`collector-prod` stage) — handles collect_history/refresh;
- `simulator` (`simulator-prod` stage) — handles simulate.

Compose:
- `collector` сервис → target=`collector-prod`;
- `simulator` сервис → target=`simulator-prod`;
- `api` получает `ENABLED_BG_KINDS=bootstrap,compute_features,train_model` чтобы не конкурировать с воркерами.

**Broadcaster абстракция.** `bgjobs.Runner` теперь принимает `Broadcaster` интерфейс вместо `*ws.Hub`:
- `HubBroadcaster` — для api-gateway (in-process pub/sub);
- `HTTPBroadcaster` — для collector/simulator, шлёт `POST /api/internal/broadcast` к gateway.

**Kind-filter.** `Runner.RestrictKinds(map[string]bool)` сужает claims каждого пула. ENABLED_BG_KINDS env-var в api → kind-filter применяется. collector ставит RestrictKinds(collect_history+refresh), simulator — RestrictKinds(simulate).

**Извлечённый `internal/simrun`.** Логика runSimulator вынесена из http-handler в пакет — теперь и HTTP-вызов, и simulator-воркер используют одну функцию `simrun.Run`. Bootstrap-orchestrator теперь энкьюит реальные `simulate` job'ы (не stub).

**Файлы:** `services/api-gateway/Dockerfile`, `services/api-gateway/cmd/{collector,simulator}/`, `services/api-gateway/internal/bgjobs/{worker.go,http_broadcaster.go}`, `services/api-gateway/internal/simrun/simrun.go`, `services/api-gateway/internal/http/{simulator.go,internal_broadcast.go}`, `services/api-gateway/cmd/api/main.go`, `docker-compose.yml`, `docker-compose.dev.yml`, `go.work`.

### Phase B — Мелкие фичи (6 пунктов)

| # | Что | Файлы |
|---|---|---|
| **B.1 Health checks ml + redis** | `/admin/health` теперь пингует ml-service `/healthz` (HTTP с timeout) и Redis (raw TCP PING без go-redis-клиента). Также добавлен `paused`-chip когда bg-jobs runner на паузе. | `services/api-gateway/internal/http/admin.go` |
| **B.2 Pause/Resume bg-jobs runner** | Флаг `bg_jobs_paused` в `system_state`. Воркеры кооперативно проверяют его в `dispatchOnce`. Эндпоинты `POST /api/admin/bg-jobs/pause` и `/resume`. UI: компонент `BGRunnerToggle` справа в строке overall-статуса /admin → System health. | `services/api-gateway/internal/store/system.go`, `internal/bgjobs/worker.go`, `internal/http/admin.go`, `frontend/src/pages/Admin.tsx`, `frontend/src/api/admin.ts` |
| **B.3 Dataset CSV export** | `GET /api/datasets/{id}/export.csv` streamит row-by-row CSV со всеми колонками + commit-fields. Кнопка **Export CSV** в actions /datasets/:id. | `services/api-gateway/internal/http/csv_export.go`, `frontend/src/api/repos.ts`, `frontend/src/pages/DatasetDetail.tsx` |
| **B.4 Simulator CSV export** | `GET /api/simulator/runs/{id}/export.csv` для одного sim_run. Ссылка **Export CSV** в каждой строке таблицы recent runs на /simulator. | `services/api-gateway/internal/http/csv_export.go`, `services/api-gateway/internal/store/sim.go` (GetSimRun), `frontend/src/api/simulator.ts`, `frontend/src/pages/Simulator.tsx` |
| **B.5 24h-нагрузка** | `GET /api/dashboard/load-24h` — почасовая агрегация. Компонент `SparklineChart` (zero-dep amber sparkline). Заменил KPI «Recent events» на «Mean duration» с sparkline и hint «N jobs in last 24h». | `services/api-gateway/internal/http/dashboard.go`, `frontend/src/api/dashboard.ts`, `frontend/src/components/SparklineChart.tsx`, `frontend/src/pages/Dashboard.tsx` |
| **B.6** (бонус) feature matrix preview уже было в UX-итерации; в этой итерации только полировка. | — | — |

### Phase C — Cloudflared tunnel docs

`docs/deployment.md` полностью переписан и теперь покрывает:
- Прод-деплой на VPS со всеми шагами + snapshot auto-restore;
- Локальная разработка с тремя tunnel-вариантами: **cloudflared** (рекомендуется, бесплатно, no-account), **ngrok**, **Tailscale Funnel** — пошаговая установка и интеграция с `PUBLIC_API_BASE`;
- Диагностика типичных проблем (container не стартует / bg_jobs молчит / webhook не приходит / OOM);
- Откат и снос.

### Phase D — Полное обновление документации

- **README.md** — отражает новую архитектуру (collector/simulator отдельно), все новые фичи, обновлённый стек технологий, расширенную таблицу метрик (NDCG@k), все группы фич (rolling, author, commits), wizard, walk-forward CV, snapshot auto-restore.
- **docs/architecture.md** — диаграмма prod-контейнеров с потоком broadcast, поток данных от webhook через δ-error, описание `Broadcaster`-абстракции, пулов воркеров, snapshot auto-restore, таблица канонических UI-страниц.
- **docs/usage.md** — добавлены секции: командная палитра, dashboard (active queue + sparkline), datasets (heatmap, webhook auto-install, SyncProgressStrip), 4 варианта обучения (включая wizard и CV), сравнение моделей бок-о-бок, экспорт CSV/thesis pack/EDA, admin (pause/resume), расширенная диагностика.
- **docs/thesis/README.md** — без изменений, актуален.
- **TODO.md** — этот файл, обновлён.

---

## 📋 Прочие мелочи (стоит упомянуть в защите)

Эти пункты я сознательно не реализовывал — они либо очевидно нерелевантны для thesis-прототипа, либо в плане были как пометки «opt»:

- **sqlc** — заявлен в плане, не используется (raw SQL через pgx). Аргумент: «pgx + хорошо документированные SQL-строки покрывают наши ~30 запросов, генератор добавил бы +1 шаг сборки без чистого выигрыша».
- **goose CLI** — миграции встроены в `Migrate()` через `go:embed`, отдельного goose-binary в compose нет. Аргумент: «one less moving part; миграции версионированы вместе с бинарём».
- **Restart service кнопка в /admin** — заменена на pause/resume (см. B.2). Полный рестарт контейнера требует Docker socket mount, чего нет в прод-контракте.
- **Скриншоты UI в docs/thesis/screenshots/** — папка создана, README с процедурой написан, но реальные PNG делать вручную (см. `docs/thesis/README.md` — там пошаговая инструкция).

---

## ✅ Сделано в предыдущих итерациях (для контекста)

### 7 критичных пунктов
| # | Что |
|---|---|
| **#6** | Webhook completed → actual_sec + δ-broadcast (in-memory cache w/ TTL=30 мин), Dashboard с цветной подсветкой |
| **#7** | Model comparison `/experiments/compare?ids=...` |
| **#1** | Walk-forward CV |
| **#3** | Author historical stats: log_author_p50_30d, log_author_p90_30d, author_commits_30d |
| **#2** | Commits collector + 3 commit-фичи |
| **#4** | LSTM (PyTorch CPU, 2 LSTM × 64 hidden) |
| **#5** | EDA notebook + generate_eda_figures.py + `make eda-figures` |

### 5 UX-важных пунктов
| # | Что |
|---|---|
| **UX#1** | Snapshot auto-restore при старте |
| **UX#5** | Live collection progress strip (ETA, jobs/sec, rate-limit countdown) |
| **UX#3** | Heatmap coverage + feature matrix preview |
| **UX#2** | Queue cards on /dashboard |
| **UX#4** | Full 4-step training wizard |

### Базовый функционал
6 моделей за единым интерфейсом, 4 стратегии планирования, симулятор-replay с метриками makespan/wait_p95/SLA, **NDCG@10/50/100**, **rolling 7d/30d медианы**, time-based split, **Optuna search**, webhook receiver + HMAC, **автоустановка webhook в GitHub**, bg-jobs runner с cancel/pause, /setup онбординг, /experiments TrainingDetail с live-loss и residuals, все 4 WS-канала, **Cmd+K command palette**, единый формат ошибок, **ApiErrorBoundary**, **Activity log в /admin**, **/admin Settings** (strategy/weights/PAT), **Model delete + download**, **Repo pause/resume/resync/delete**, **CSV-экспорты** (dataset, sim_run, thesis pack), i18n EN/RU, Docker dev+prod с Traefik+LE, **cloudflared tunnel docs для local webhook'ов**, CI workflow.

---

**Build status:**
- Go (`go build ./...`) ✅
- Go tests (все 8 пакетов) ✅
- TypeScript (`npx tsc --noEmit`) ✅
