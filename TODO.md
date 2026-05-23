# TODO — что ещё не сделано из `plan.md`

Дата сверки: 2026-05-23 (после реализации всех 7 критичных + всех 5 UX-важных пунктов).

Все пункты разделов **«🔴 Критично»** и **«🟡 Важно»** — реализованы. Осталось то, что в плане заявлено как nice-to-have или explicit out-of-scope.

---

## 🔴 Критично для содержания диссертации

> ✅ Полностью реализовано — см. блок «Сделано в предыдущих итерациях» ниже.

---

## 🟡 Важно для UX и убедительности демо

> ✅ Полностью реализовано — см. блок «Сделано в этой итерации» ниже.

---

## 🟢 Nice-to-have / можно вычеркнуть в «out of scope»

Эти можно НЕ делать, но **обязательно проговорить в защите** как явные решения «out of scope для thesis-прототипа».

### 1. JWT auth с Secure+HttpOnly cookies
- **Что есть:** ничего, `JWT_SECRET` читается из env, но не используется.
- **Чего не хватает:** middleware + `/api/login` + cookies.
- **Аргумент в защите:** «single-user thesis prototype; multi-tenant и аутентификация вынесены в out of scope».

### 2. Redis sorted-set очередь и таблица `queue_state`
- **Что есть:** Redis в compose, но никто в него не пишет. `queue_state` создана, пустая. Эндпоинт `/api/queue` — SQL-view над `jobs`.
- **Аргумент в защите:** «при единичном scheduler-worker SQL-view достаточен; Redis sorted-set оправдан только при горизонтальном масштабировании».

### 3. Online-режим симулятора (real flow из webhooks)
- **Что есть:** только replay.
- **Аргумент в защите:** «online-режим = частный случай real-time push pipeline (уже реализован); replay даёт повторяемые измерения для главы 4».

### 4. Отдельные процессы `services/collector/` и `services/simulator/`
- **Что есть:** пустые каркасы. Вся логика в `api-gateway`.
- **Аргумент в защите:** «один бинарь проще оперативно; вынос — задача горизонтального масштабирования».

### 5. Python тесты на модели + integration tests
- **Что есть:** 3 теста (`test_features`, `test_healthz`, `test_optuna_search`). CI **не запускает** `pytest`.
- **Чего не хватает:** `tests/test_models.py` параметризованный по 6 алгоритмам (fit на 200 строк, predict, save→load, проверка metrics shape) + интеграция `/predict` с TestClient + monkeypatched DB + включить `pytest` в `.github/workflows/ci.yml`.
- **Аргумент в защите:** «Go-side покрыт unit-тестами + табличными для стратегий, e2e через `make smoke-ml`».
- **Размер:** S (стоит сделать — gate качества почти бесплатный).

### 6. testcontainers integration + e2e
- **Что есть:** ничего; нет зависимости в `go.mod`.
- **Чего не хватает:** Postgres + Redis testcontainers; e2e: webhook → predict → enqueue → simulate.
- **Аргумент в защите:** «покрытие unit-тестами Go = 9 файлов, ручной smoke через `make smoke-ml`».

---

## 📋 Мелкие пробелы

- **Health checks для ml-service и redis** — сейчас `/admin/health` пингует только Postgres и bg-jobs runner. Добавить ping ml-service `/healthz` и Redis `PING`.
- **«Restart service» кнопка** в /admin → System health — план обещает, но требует Docker socket mount → лучше документировать как «pause/resume bg-jobs runner» через флаг.
- **Setup tunnel (cloudflared) для webhook** — план обещает встроенный туннель; реалистично — добавить инструкцию в `docs/deployment.md` про `ngrok`/`cloudflared` для локальной разработки.
- **Export dataset (CSV/Parquet)** на /datasets — endpoint + кнопка.
- **Отдельный Export CSV/PNG на /simulator** — сейчас только глобальный thesis pack на /experiments.
- **sqlc** — заявлен в плане, не используется (raw SQL через pgx). Аргумент: «pgx + хорошо документированные SQL-строки покрывают наши 20 запросов».
- **goose CLI** — миграции встроены в `Migrate()`, отдельного goose-binary в compose нет. Аргумент: «one less moving part».
- **Mini-график загрузки за 24ч** на /dashboard — нет (есть active queue + mean wait, чего обычно достаточно).
- **Скриншоты UI** в `docs/thesis/screenshots/` — папка создана, README с процедурой написан, но реальные PNG сделать вручную (см. `docs/thesis/README.md`).

---

## ✅ Сделано в этой итерации (5 UX-важных пунктов)

| # | Что | Файлы |
|---|---|---|
| **UX#1 Snapshot auto-restore** | При старте api-gateway после `Migrate()` проверяется `bootstrap_done` — если `false` и есть `/var/lib/cicdml/seed/snapshot.sql.gz`, gunzip + multi-statement `tx.Exec` через pgx, флаг `bootstrap_done=true` в той же транзакции. Makefile snapshot теперь использует `pg_dump --inserts` (pure SQL без COPY). Bind mount `./db/seed:/var/lib/cicdml/seed:ro` в compose. | `services/api-gateway/internal/store/snapshot.go` (новый), `services/api-gateway/cmd/api/main.go`, `Makefile`, `docker-compose.yml` |
| **UX#5 Live collection progress** | `sync.go` теперь сериализует в `bg_jobs.logs_tail` JSON-blob `syncStats {phase, page, runs_seen/total, jobs_per_sec, eta_seconds, rate_remaining/limit, rate_reset_unix}`. Hook `useRepoSyncProgress` слушает /ws/bg-jobs + REST-seed; компонент `SyncProgressStrip` рендерит бар + ETA + rate-counter с 1Hz-отсчётом до reset. | `services/api-gateway/internal/github/sync.go`, `frontend/src/hooks/useRepoSyncProgress.ts` (новый), `frontend/src/components/SyncProgressStrip.tsx` (новый), `frontend/src/pages/Datasets.tsx` |
| **UX#3 Heatmap coverage + feature preview** | `GET /api/datasets/coverage?days=90` возвращает разреженный grid `[repo × day → count]`. Компонент `HeatmapChart` рендерит matrix с log-saturation amber-палитрой. `GET /api/datasets/{id}/features?limit=50&job_name=...` отдаёт первые N строк фичей с unpacked JSONB; `FeaturePreview` показывает top-12 наиболее часто встречающихся колонок с фильтром по job_name. | `services/api-gateway/internal/http/datasets.go`, `frontend/src/components/HeatmapChart.tsx` (новый), `frontend/src/pages/Datasets.tsx`, `frontend/src/pages/DatasetDetail.tsx` |
| **UX#2 Queue cards on /dashboard** | Новый компонент `QueueCard` с predicted/elapsed/actual/δ + live-таймером (1Hz пока running) + прогресс-баром elapsed/predicted. Hook `useDashboardQueue` ведёт `Map<run_id, card>` из REST-seed (`/api/queue`) + live-overlay (/ws/queue), с auto-sweep completed карточек через 30s. Сортировка: running → queued → completed. KPI «mean duration» заменил «recent events». | `frontend/src/components/QueueCard.tsx` (новый), `frontend/src/hooks/useDashboardQueue.ts` (новый), `frontend/src/api/queue.ts` (новый), `frontend/src/pages/Dashboard.tsx` |
| **UX#4 Full training wizard** | 4-шаговый wizard `TrainWizard`: (1) Algo чипы; (2) Repo чекбоксы + slider 3/6/12 мес; (3) per-algo гиперпараметр-слайдеры с диапазонами matching `BaseModel._build_estimator` дефолтов; (4) interactive cutoff timeline (`GET /api/datasets/timeline`) — bar-chart дневных count'ов с кликабельной cutoff-линией, train (amber) / test (grey) подсветкой. Activate-on-finish + Submit. Поле `since` пробрасывается через `startTraining`. | `frontend/src/components/TrainWizard.tsx` (новый), `services/api-gateway/internal/http/datasets.go`, `services/api-gateway/internal/http/training.go`, `frontend/src/api/repos.ts`, `frontend/src/api/models.ts`, `frontend/src/pages/Experiments.tsx` |

---

## ✅ Сделано в предыдущих итерациях

### 7 критичных пунктов (прошлая итерация)

| # | Что |
|---|---|
| **#6** | Webhook completed → actual_sec + δ-broadcast (in-memory cache w/ TTL=30 мин), Dashboard EventRow с цветной подсветкой |
| **#7** | Model comparison `/experiments/compare?ids=...` — metrics table, overlaid scatter, stacked feature importance |
| **#1** | Walk-forward CV (`time_based_cv` + `cross_validate` + `POST /train/cv` + UI) |
| **#3** | Author historical stats: `log_author_p50_30d`, `log_author_p90_30d`, `author_commits_30d` |
| **#2** | Commits collector (`Client.GetCommit`, `store.UpsertCommit`, дедуп по SHA) + 3 commit-фичи |
| **#4** | LSTM (PyTorch CPU-wheel, 2 LSTM × 64 hidden, per-epoch streaming) |
| **#5** | EDA notebook + `generate_eda_figures.py` + `make eda-figures` + `docs/thesis/README.md` |

### Базовый функционал

Архитектура каталогов, БД-схема (15 таблиц), 4 стратегии планирования (FIFO/SJF/EDF/Custom), симулятор-replay с метриками makespan/wait_p95/SLA, **NDCG@10/50/100**, **rolling 7d/30d медианы**, 6 моделей за единым интерфейсом, time-based split, **Optuna search**, webhook receiver + HMAC, **автоустановка webhook в GitHub**, bg-jobs runner с cancel, /setup онбординг, /experiments TrainingDetail с live-loss и residuals plot, все 4 WS-канала, **Cmd+K command palette**, единый формат ошибок, **ApiErrorBoundary**, **Activity log в /admin**, **/admin Settings** (strategy/weights/PAT), **Model delete + download**, **Repo pause/resume/resync/delete**, i18n EN/RU, Docker dev+prod с Traefik+LE, **thesis-pack export endpoint**, CI workflow.

---

**Build status:** Go (`go build ./...`) ✅, Go tests ✅, TypeScript (`npx tsc --noEmit`) ✅.
