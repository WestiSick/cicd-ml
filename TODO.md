# TODO — что ещё не сделано из `plan.md`

Дата сверки: 2026-05-23 (после реализации всех 7 критичных пунктов). Снимок текущего состояния кода против исходной спецификации `plan.md`.

Все пункты раздела «🔴 Критично» — **реализованы** (см. блок ✅ внизу). Что осталось — UX-полировка и явные «out of scope» решения.

---

## 🔴 Критично для содержания диссертации

> ✅ Все пункты этой секции реализованы. Подробности — в блоке «Сделано в этой итерации» внизу.

---

## 🟡 Важно для UX и убедительности демо

Без этого можно защититься, но защита слабее.

### 1. Авто-bootstrap при первом старте + snapshot.sql.gz auto-restore
- **Что есть:** ручной запуск из `/setup`; Makefile-targets `make snapshot` / `make restore-snapshot`.
- **Чего не хватает:**
  - в `cmd/api/main.go` после `Migrate()` проверять флаг `bootstrap_done` — если `false` и файл `/var/lib/cicdml/seed/snapshot.sql.gz` есть, **автоматически восстановить и пометить done**;
  - bind mount `./db/seed/:/var/lib/cicdml/seed/:ro` в `docker-compose.yml`.
- **Зачем:** план обещает «без единой команды от пользователя» и «1–2 минуты вместо часов» для проверяющего. Сейчас рецензенту нужно либо ждать часы сбора, либо самому помнить про `make restore-snapshot`.
- **Размер:** S (одна функция `restoreSnapshotIfPresent` + одна строка в compose).

### 2. Карточки job в очереди с прогресс-барами на /dashboard
- **Что есть:** лента событий (Live feed) + δ-error для completed (только что добавлено).
- **Чего не хватает:**
  - вертикальный список карточек job'ов в активном состоянии (`queued/running`);
  - на каждой: репо, ветка, автор, имя job, прогноз, факт (если есть), δ-ошибка, taймер если running, прогресс-бар;
  - использовать существующий endpoint `GET /api/queue`;
  - KPI «средний wait_time» через агрегирование.
- **Зачем:** план описывает /dashboard как «живую очередь» — сейчас он больше «лог событий». Карточки = убедительная демо-картинка.
- **Размер:** M (компонент `<QueueCard>` + reducer состояния + WS-обновления).

### 3. Heatmap покрытия данных + feature matrix preview
- **Что есть:** /datasets/:id показывает гистограмму, top workflows/jobs, branches, conclusions.
- **Чего не хватает:**
  - endpoint `GET /api/datasets/coverage` возвращает матрицу `[repo × date_bucket] → count(jobs)`;
  - компонент `<HeatmapChart>` на главной /datasets;
  - таблица первых 50 строк фич с фильтром по job_name на /datasets/:id (читать из `features.feature_vector` JSONB).
- **Зачем:** план §«Карта временного покрытия — heatmap: репозиторий × дата, цвет = плотность job. Видно дыры в данных» — прямая цитата. Без heatmap «качество датасета» — голословное утверждение.
- **Размер:** M (1 endpoint + 1 heatmap component + 1 table view).

### 4. Полноценный wizard на /experiments
- **Что есть:** упрощённая форма (пилюли алгоритмов + чекбокс activate + Optuna trials + walk-forward CV — последнее только что добавлено).
- **Чего не хватает (по плану §7-4):**
  1. Multi-step wizard с шагами Algo / Dataset / Hyperparams / Split.
  2. Выбор датасета: чекбоксы репозиториев + slider период.
  3. Гиперпараметры с подсказками: для xgb — `n_estimators`, `max_depth`, `learning_rate` ползунки; для rf — `n_estimators`, `max_depth`; и т.д.
  4. **Timeline-визуализация cutoff** (вертикальная линия на графике количества runs по датам).
- **Зачем:** план явно описывает этот wizard в разделе фронтенда. Сейчас рецензент справедливо спросит «а tuning per repo как делали?». Hyperparams form даёт ответ «делали через UI, скриншот вот».
- **Размер:** M (мастер на Radix Tabs или 4 шага последовательно).

### 5. Live-прогресс сбора на /datasets
- **Что есть:** карточка показывает статус `idle/fetching/synced`, бесконечный прогресс.
- **Чего не хватает (по плану):**
  - ETA;
  - rate-limit counter (`4982/5000 remaining, reset in 23m`);
  - скорость (jobs/sec);
  - прогресс-бар по страницам GitHub API;
  - журнал последних событий сборки.
- **Зачем:** «во время сбора пользователь должен видеть что происходит» — план явно. Сейчас «fetching» висит непрозрачно для пользователя.
- **Размер:** S (расширить payload bg_job + рендеринг в UI; данные уже есть в `RateLimit` структуре `github.Client`).

---

## 🟢 Nice-to-have / можно вычеркнуть в «out of scope»

Эти можно НЕ делать, но **обязательно проговорить в защите** как явные решения «out of scope для thesis-прототипа».

### 6. JWT auth с Secure+HttpOnly cookies
- **Что есть:** ничего, `JWT_SECRET` читается из env но не используется.
- **Чего не хватает:** middleware + `/api/login` + cookies.
- **Аргумент в защите:** «single-user thesis prototype; multi-tenant и аутентификация вынесены в out of scope».

### 7. Redis sorted-set очередь и таблица `queue_state`
- **Что есть:** Redis в compose, но никто в него не пишет. `queue_state` создана, пустая. Эндпоинт `/api/queue` — SQL-view над `jobs`.
- **Аргумент в защите:** «при единичном scheduler-worker SQL-view достаточен; Redis sorted-set оправдан только при горизонтальном масштабировании».

### 8. Online-режим симулятора (real flow из webhooks)
- **Что есть:** только replay.
- **Аргумент в защите:** «online-режим = частный случай real-time push pipeline (уже реализован); replay даёт повторяемые измерения для главы 4».

### 9. Отдельные процессы `services/collector/` и `services/simulator/`
- **Что есть:** пустые каркасы (один heartbeat, один `fmt.Println`). Вся логика в `api-gateway`.
- **Аргумент в защите:** «один бинарь проще оперативно; вынос — задача горизонтального масштабирования».

### 10. Python тесты на модели + integration tests
- **Что есть:** 3 теста (`test_features`, `test_healthz`, `test_optuna_search`). CI **не запускает** `pytest`.
- **Чего не хватает:** `tests/test_models.py` параметризованный по 6 алгоритмам (fit на 200 строк, predict, save→load, проверка metrics shape) + интеграция `/predict` с TestClient + monkeypatched DB + включить `pytest` в `.github/workflows/ci.yml`.
- **Аргумент в защите:** «Go-side покрыт unit-тестами + табличными для стратегий, e2e через `make smoke-ml`».
- **Размер:** S (но реально стоит сделать — gate качества почти бесплатный).

### 11. testcontainers integration + e2e
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
- **Mini-график загрузки за 24ч** на /dashboard — нет.
- **Скриншоты UI** в `docs/thesis/screenshots/` — папка создана, README с процедурой написан, но реальные PNG сделать вручную (см. `docs/thesis/README.md`).

---

## 📊 Приоритизация: что делать в ближайшую неделю

Если есть время на 5 пунктов из «🟡 Важно», в этом порядке:

1. **#1 Snapshot auto-restore** (S) — «защита за 2 минуты» вместо «приходите завтра».
2. **#5 Live-прогресс сбора** (S) — самая дешёвая UX-победа.
3. **#3 Heatmap покрытия** (M) — закрывает аргумент «качество датасета».
4. **#2 Карточки очереди на dashboard** (M) — усиливает live-демо.
5. **#10 Python тесты на модели** (S) — gate качества для защиты.

Если ещё неделя:
6. **#4 Полный wizard /experiments** (M) — закрывает «как делали tuning» вопрос.

---

## ✅ Сделано в этой итерации (7 критичных пунктов)

| # | Что | Файлы |
|---|---|---|
| **#6 Webhook completed → δ broadcast** | `workflow_run.completed` теперь вычисляет `actual_sec = updated_at - run_started_at`, ищет предыдущий прогноз через in-memory кэш с TTL=30 мин, броадкастит `delta_pct`. Dashboard EventRow показывает actual + δ% с цветной подсветкой (зелёный/жёлтый/красный). | `internal/http/webhook.go`, `internal/http/prediction_cache.go` (новый), `internal/http/server.go`, `frontend/src/pages/Dashboard.tsx`, `frontend/src/lib/format.ts` |
| **#7 Model comparison side-by-side** | Новая страница `/experiments/compare?ids=1,2,3` с таблицей метрик (best per row подсвечен), overlaid PvA scatter с цветными сериями и стек-bar feature importance. Чекбоксы на /experiments + кнопка «Compare selected (N)». | `frontend/src/pages/ModelCompare.tsx` (новый), `frontend/src/pages/Experiments.tsx`, `frontend/src/App.tsx` |
| **#1 Walk-forward cross-validation** | `time_based_cv(df, n_splits=5)` в `features/build.py` + `cross_validate()` в `pipeline.py` возвращает per-fold + mean ± std метрик. Endpoint `POST /api/training/cv` (через ml-service `/train/cv`). UI: блок «Walk-forward CV» с пилюлями фолдов и таблицей результатов. | `services/ml-service/app/features/build.py`, `services/ml-service/app/training/pipeline.py`, `services/ml-service/app/api/train.py`, `services/api-gateway/internal/ml/client.go`, `services/api-gateway/internal/http/training.go`, `frontend/src/pages/Experiments.tsx`, `frontend/src/api/models.ts` |
| **#3 Author historical stats** | 3 новые numeric-фичи: `log_author_p50_30d`, `log_author_p90_30d`, `author_commits_30d`. Rolling по 30-дневному окну per actor с shift(1) против target leakage. | `services/ml-service/app/features/build.py` |
| **#2 Commit diff features + collector** | `Client.GetCommit()` + `store.UpsertCommit()` / `CommitExists()` (дедуп по SHA). Collector после upsert run автоматически тянет commit, persists `additions/deletions/files_changed`. ml-service LEFT JOIN commits в `load_jobs_df`, добавлены фичи `log_commit_files_changed/additions/deletions`. | `services/api-gateway/internal/github/client.go`, `services/api-gateway/internal/github/sync.go`, `services/api-gateway/internal/store/commits.go` (новый), `services/ml-service/app/storage/db.py`, `services/ml-service/app/features/build.py` |
| **#4 LSTM модель** | Полная реализация на PyTorch (CPU-wheel): 2 stacked LSTM слоя × 64 hidden, Adam, MSE на log-target. Подключена в `factory_by_algo("lstm")`. Стримит per-epoch метрики через тот же `post_metric` канал что и boosted-модели. Артефакт serialise via state_dict bytes + joblib. | `services/ml-service/requirements.txt`, `services/ml-service/app/models/lstm.py` (новый), `services/ml-service/app/models/lgbm.py` |
| **#5 EDA notebook + thesis figures** | `ml/notebooks/01_eda.ipynb` (5 фигур: distribution, top jobs, branch class, hour-of-day, correlation matrix) + standalone `generate_eda_figures.py` для CI. Тёмная палитра matplotlib совпадает с UI. `make eda-figures` запускает скрипт в контейнере. `docs/thesis/figures/` и `docs/thesis/screenshots/` созданы с README по процедуре. | `ml/notebooks/01_eda.ipynb`, `ml/notebooks/generate_eda_figures.py`, `docker-compose.dev.yml`, `Makefile`, `docs/thesis/README.md` |

**Build status:** Go (`go build ./...`) ✅, TypeScript (`npx tsc --noEmit`) ✅, Python (`py_compile`) ✅.
