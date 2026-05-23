# TODO — что ещё не сделано из `plan.md`

Дата сверки: 2026-05-23. Снимок текущего состояния кода против исходной спецификации `plan.md` (867 строк). Цель документа — честный punch-list для остатка работ перед защитой диссертации.

Сортировка внутри блоков — **по приоритету для защиты**, не по сложности.

---

## 🔴 Критично для содержания диссертации

Это пробелы, которые рецензент **заметит и спросит**. Без них защита уязвима.

### 1. Walk-forward cross-validation
- **Что есть:** один time-based split (`train_frac=0.8`) в `services/ml-service/app/features/build.py`.
- **Чего не хватает:** функция `time_based_cv(df, n_splits=5)` + метод `train_with_cv()` в `services/ml-service/app/training/pipeline.py`, который возвращает усреднённые MAE/RMSE/Spearman/NDCG со std по фолдам.
- **Зачем:** без CV метрики из одного split — это одна точка. План явно требует «cross-validation с временным окном» (раздел «ML-сервис», п. «Эксперименты»). Без этого глава 3 = одна цифра вместо доверительного интервала.
- **Размер:** S (1 функция + 1 эндпоинт `/train/cv` + 1 кнопка в UI).

### 2. Commit diff features + сбор `commits`
- **Что есть:** таблицы `commits` и `commit_files` объявлены в схеме (`commit_files` фактически отсутствует), но сборщик их не наполняет.
- **Чего не хватает:**
  - расширить `services/api-gateway/internal/github/sync.go` чтобы тянуть `GET /repos/{owner}/{repo}/commits/{sha}` для каждого `workflow_run`;
  - писать в `commits` (`files_changed`, `additions`, `deletions`, `author`);
  - добавить новую миграцию `00004_commit_files.sql`;
  - добавить numeric features `commit_files_changed`, `commit_additions`, `commit_deletions`, `commit_test_ratio`, `commit_config_ratio` в `services/ml-service/app/features/build.py`.
- **Зачем:** план явно перечисляет это как «базовый набор» (раздел «Признаки (feature engineering) → Commit»). Без commit-фич рецензент скажет «вы предсказываете длительность без знания диффа — странно для CI».
- **Размер:** M (сборщик + миграция + 5 фич + пересчёт `materialize_all`).

### 3. Author historical stats
- **Что есть:** ничего — `author` есть в `workflow_runs.actor`, но не агрегирован.
- **Чего не хватает:** numeric features `author_p50_30d`, `author_p90_30d`, `author_commits_30d` (по аналогии с уже реализованными `log_jobname_median_*`). Должны вычисляться в `_enrich()`.
- **Зачем:** план явно: «Author: историческое среднее, p50/p90 длительности, число коммитов автора». Это сильный сигнал — разные авторы триггерят разные шейпы пайплайнов.
- **Размер:** S (rolling по `actor` группе аналогично текущему rolling по job_name).

### 4. LSTM-модель
- **Что есть:** в `/setup` есть чекбокс **«LSTM (PyTorch)»**, но при выборе обучение упадёт с `unknown algo`. `torch` даже не в `requirements.txt`.
- **Чего не хватает:**
  - добавить `torch` в `services/ml-service/requirements.txt`;
  - реализовать `services/ml-service/app/models/lstm.py` на последовательностях rolling-фич, упорядоченных по времени per `(repo_id, job_name)`;
  - подключить в `factory_by_algo` в `models/lgbm.py`;
  - стримить per-эпоху метрики через существующий `_streaming_training_job_id` механизм.
- **Зачем:** план выделяет это в отдельную главу «использование temporal-зависимостей». Сейчас обещание в UI расходится с реальностью.
- **Размер:** M (модель + dataset shaping + интеграция в pipeline).

### 5. EDA notebook + thesis figures
- **Что есть:** `docs/thesis/figures/` и `docs/thesis/screenshots/` — **пустые папки**. `ml/notebooks/` — не существует.
- **Чего не хватает:**
  - создать `ml/notebooks/01_eda.ipynb` с: распределение длительностей (log-bin), корреляционная матрица фич, top-50 job_name, breakdown по веткам, scatter по часам/дням;
  - прогнать на собранных данных, сохранить PNG в `docs/thesis/figures/`;
  - снять 5–6 скриншотов /dashboard, /datasets/:id, /experiments, /experiments/jobs/:id, /simulator в **тёмной теме на 1440×900 без курсора** (требование плана).
- **Зачем:** план явно заявляет это как «артефакт этапа 2». Пустые папки `docs/thesis/*` в репозитории — первое, что заметит рецензент.
- **Размер:** S (один notebook, один прогон, ручные скриншоты).

### 6. Real-time цепочка: `workflow_run.completed` → δ-ошибка прогноза
- **Что есть:** webhook ловит `requested/queued/in_progress`, на `requested` зовёт `ml.PredictFromPayload` и броадкастит `predicted_sec` в `/ws/queue`.
- **Чего не хватает:** на `workflow_run.completed`:
  - UPSERT в `jobs` (started_at, completed_at, duration_sec) по `head_sha`;
  - вычислить `delta = actual - predicted` из последней записи в `predictions`;
  - броадкаст WS-события `job.completed` с этим полем;
  - на Dashboard.tsx обновить запись **по run_id/sha** вместо добавления новой; показать δ-ошибку с цветной подсветкой.
- **Зачем:** это центральная вау-демо защиты («push → прогноз → факт → δ за 1 секунду»). Сейчас цепочка обрывается на `in_progress` и δ пользователь видит только после следующего sync collector-а (минуты).
- **Размер:** M (Go-handler + новый dashboard reducer + ScatterPlot-обновление).

### 7. Model comparison side-by-side
- **Что есть:** на /experiments — таблица моделей; на /experiments/jobs/:id — scatter одной модели.
- **Чего не хватает:** новая страница `/experiments/compare?ids=1,2,3` (или модал) с:
  - таблицей метрик по моделям колонками;
  - общим scatter `predicted-vs-actual` с разными цветами для серий;
  - общим horizontal-bar chart `top-20 features` (overlaid);
  - кнопкой «Compare» на каждой строке списка моделей с чекбоксом «add to comparison».
- **Зачем:** это **главный артефакт главы 4** — «сравнение моделей». Один скриншот «6 моделей бок-о-бок» решает половину защиты. Сейчас рецензент должен листать и держать цифры в голове.
- **Размер:** S (страница + endpoint `/api/models/compare?ids=...` опционально).

---

## 🟡 Важно для UX и убедительности демо

Без этого можно защититься, но защита слабее.

### 9. Карточки job в очереди с прогресс-барами на /dashboard
- **Что есть:** только лента событий (Live feed).
- **Чего не хватает:**
  - вертикальный список карточек job'ов в активном состоянии (`queued/running`);
  - на каждой: репо, ветка, автор, имя job, прогноз, факт (если есть), δ-ошибка, taймер если running, прогресс-бар;
  - использовать существующий endpoint `GET /api/queue`, который я уже добавил;
  - KPI «средний wait_time» через агрегирование.
- **Зачем:** план описывает /dashboard как «живую очередь» — сейчас он больше «лог событий». Карточки = убедительная демо-картинка.
- **Размер:** M (компонент `<QueueCard>` + reducer состояния + WS-обновления).

### 10. Heatmap покрытия данных + feature matrix preview
- **Что есть:** /datasets/:id показывает гистограмму, top workflows/jobs, branches, conclusions.
- **Чего не хватает:**
  - endpoint `GET /api/datasets/coverage` возвращает матрицу `[repo × date_bucket] → count(jobs)`;
  - компонент `<HeatmapChart>` на главной /datasets;
  - таблица первых 50 строк фич с фильтром по job_name на /datasets/:id (читать из `features.feature_vector` JSONB).
- **Зачем:** план §«Карта временного покрытия — heatmap: репозиторий × дата, цвет = плотность job. Видно дыры в данных» — прямая цитата. Без heatmap «качество датасета» — голословное утверждение.
- **Размер:** M (1 endpoint + 1 heatmap component + 1 table view).

### 11. Полноценный wizard на /experiments
- **Что есть:** упрощённая форма (пилюли алгоритмов + чекбокс activate + Optuna trials).
- **Чего не хватает (по плану §7-4):**
  1. Multi-step wizard с шагами Algo / Dataset / Hyperparams / Split.
  2. Выбор датасета: чекбоксы репозиториев + slider период.
  3. Гиперпараметры с подсказками: для xgb — `n_estimators`, `max_depth`, `learning_rate` ползунки; для rf — `n_estimators`, `max_depth`; и т.д.
  4. **Timeline-визуализация cutoff** (вертикальная линия на графике количества runs по датам).
- **Зачем:** план явно описывает этот wizard в разделе фронтенда. Сейчас рецензент справедливо спросит «а tuning per repo как делали?». Hyperparams form даёт ответ «делали через UI, скриншот вот».
- **Размер:** M (мастер на Radix Tabs или 4 шага последовательно).

### 12. Live-прогресс сбора на /datasets
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

### 13. JWT auth с Secure+HttpOnly cookies
- **Что есть:** ничего, `JWT_SECRET` читается из env но не используется.
- **Чего не хватает:** middleware + `/api/login` + cookies.
- **Аргумент в защите:** «single-user thesis prototype; multi-tenant и аутентификация вынесены в «out of scope».

### 14. Redis sorted-set очередь и таблица `queue_state`
- **Что есть:** Redis в compose, но никто в него не пишет. `queue_state` создана, пустая. Эндпоинт `/api/queue` — SQL-view над `jobs`.
- **Аргумент в защите:** «при единичном scheduler-worker SQL-view достаточен; Redis sorted-set оправдан только при горизонтальном масштабировании».

### 15. Online-режим симулятора (real flow из webhooks)
- **Что есть:** только replay.
- **Аргумент в защите:** «online-режим = частный случай real-time push pipeline (уже реализован); replay даёт повторяемые измерения для главы 4».

### 16. Отдельные процессы `services/collector/` и `services/simulator/`
- **Что есть:** пустые каркасы (один heartbeat, один `fmt.Println`). Вся логика в `api-gateway`.
- **Аргумент в защите:** «один бинарь проще оперативно; вынос — задача горизонтального масштабирования».

### 17. Python тесты на модели + integration tests
- **Что есть:** 3 теста (`test_features`, `test_healthz`, `test_optuna_search`). CI **не запускает** `pytest`.
- **Чего не хватает:** `tests/test_models.py` параметризованный по 5 алгоритмам (fit на 200 строк, predict, save→load, проверка metrics shape) + интеграция `/predict` с TestClient + monkeypatched DB + включить `pytest` в `.github/workflows/ci.yml`.
- **Аргумент в защите:** «Go-side покрыт unit-тестами + табличными для стратегий, e2e через `make smoke-ml`».
- **Размер:** S (но реально стоит сделать — gate качества почти бесплатный).

### 18. testcontainers integration + e2e
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

---

## 📊 Приоритизация: что делать в ближайшую неделю

Если есть только время на 5 пунктов, делать в этом порядке:

1. **#5 EDA notebook + thesis figures** (S) — самое заметное «зияние» в репозитории.
2. **#8 Snapshot auto-restore** (S) — «защита за 2 минуты» вместо «приходите завтра».
3. **#1 Walk-forward CV** (S) — закрывает главный методологический пробел главы 3.
4. **#6 Webhook completed → δ broadcast** (M) — главная вау-демо защиты.
5. **#7 Model comparison** (S) — главный визуал главы 4.

Если ещё неделя:
6. **#2 Commit features** (M).
7. **#4 LSTM** или явное удаление из UI (M).
8. **#3 Author stats** (S).

---

## ✅ Что **сделано** уже

Для полноты — что НЕ нужно делать (вынесено отдельно):

- Архитектура каталогов, БД-схема (14 таблиц), 4 стратегии планирования (FIFO/SJF/EDF/Custom), симулятор-replay с метриками makespan/wait_p95/SLA, **NDCG@10/50/100**, **rolling 7d/30d медианы**, 5 моделей за единым интерфейсом, time-based split, **Optuna search**, webhook receiver + HMAC, **автоустановка webhook в GitHub при добавлении репо**, bg-jobs runner с cancel, /setup онбординг, /experiments TrainingDetail с live-loss и residuals plot, все 4 WS-канала, **Cmd+K command palette**, единый формат ошибок, **ApiErrorBoundary**, **Activity log в /admin**, **/admin Settings** (strategy/weights/PAT), **Model delete + download**, **Repo pause/resume/resync/delete**, i18n EN/RU, Docker dev+prod с Traefik+LE, **thesis-pack export endpoint**, CI workflow.
