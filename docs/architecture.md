# Архитектура

Архитектура системы `cicd-ml` — практического артефакта магистерской
диссертации по ML-предсказанию и планированию очередей CI/CD.

## Компоненты

```
                ┌──────────────────────────────────────────────┐
                │             Frontend (React + TS)            │
                │  /dashboard /datasets /experiments /admin    │
                │  /simulator /setup /experiments/jobs/:id     │
                └────────────────┬──────────────┬──────────────┘
                                 │ REST         │ WebSocket
                                 ▼              ▼
                    ┌──────────────────────────────────┐
                    │       api-gateway (Go, chi)      │
                    │  REST · WS · webhooks · auth     │
                    │  scheduler · bootstrap · bg_jobs │
                    │  github collector · simulator    │
                    └──┬──────────────┬────────────────┘
                       │ HTTP         │
                       │              ▼
                       │   ┌──────────────────┐  ┌──────────────────┐
                       │   │  ml-service      │  │     Redis        │
                       │   │  (FastAPI)       │  │  pub/sub (зарез.)│
                       │   │ train · predict  │  └──────────────────┘
                       │   │ features · export│
                       │   │ optuna · figures │
                       │   └────────┬─────────┘
                       │            │
                       ▼            ▼
            ┌────────────────────────────────────────┐
            │            PostgreSQL                  │
            │  repos · workflow_runs · jobs ·         │
            │  features · models · predictions ·      │
            │  bg_jobs · training_metrics ·           │
            │  sim_runs · webhook_events ·            │
            │  activity_log · system_state            │
            └────────────────────────────────────────┘
```

**Важно**: collector и simulator существуют как отдельные Go-модули
(`services/collector`, `services/simulator`), но фактическая работа
по сбору данных и симуляции живёт **внутри api-gateway** — это
прагматичное упрощение для текущего масштаба. Отдельные бинарники
зарезервированы под будущее, когда понадобится разнести нагрузку.

## Поток данных

1. **Подключение репозитория.** Пользователь добавляет репозиторий через
   `POST /api/repos` или `/setup`. api-gateway **автоматически**
   энкьюит `bg_job` типа `collect_history`.

2. **Сбор данных.** Background-воркер `io-pool` (1 goroutine) забирает
   `collect_history` — последовательно тянет страницы GitHub Actions API,
   делает UPSERT в `workflow_runs`, `jobs`, `commits` с чекпоинтами.
   При rate-limit (403/429) ждёт reset, прогресс обновляется.

3. **Извлечение фич.** `compute_features` bg_job → ml-service читает
   `jobs ⨝ workflow_runs ⨝ repos`, считает feature_vector по каждому
   job'у и записывает в `features` (JSONB, версия схемы фиксирована).

4. **Обучение.** Пользователь создаёт `bg_job` типа `train_model`.
   api-gateway вызывает ml-service `/train` (или `/train/optuna`).
   ml-service:
   - читает features + target из БД,
   - делает time-based split (80/20),
   - обучает выбранную модель (Linear / RF / XGBoost / LightGBM / MLP),
   - стримит per-iteration метрики в `training_metrics`,
   - сохраняет артефакт в shared volume `model-artifacts`,
   - записывает строку в `models` с метриками и feature_importance,
   - предсказывает на тестовой выборке → пишет в `predictions`.

5. **Предсказание.** На webhook `workflow_run.requested` api-gateway
   вычисляет feature_vector нового job'а, зовёт `ml-service /predict`,
   пишет результат в `predictions`, броадкастит через `/ws/queue` →
   фронт показывает карточку прогноза.

6. **Планирование.** Активная модель + активная стратегия (FIFO/SJF/
   EDF/Custom) определяют порядок job'ов в Redis sorted set по
   `predicted_sec`. (Гибридный режим — фактическое исполнение всё ещё
   делает GitHub Actions, мы лишь записываем решение.)

7. **Симуляция.** `POST /api/simulator/run` синхронно: api-gateway
   читает window из БД, проигрывает event-driven через каждую стратегию,
   пишет результат в `sim_runs`.

## Канонический формат ошибок (UI feedback contract)

Каждый эндпоинт — Go или Python — возвращает ошибки в одной форме:

```json
{
  "error": {
    "code": "github_rate_limited",
    "message": "GitHub API rate limit exceeded",
    "details": { "reset_at": "2026-05-14T19:42:00Z" },
    "user_action": "Wait until reset or add a GitHub token in /admin."
  }
}
```

Фронтенд читает `user_action` и показывает его в toast'е через sonner.
Внутренние коды и стек-трейсы в UI не попадают — они только в логах.

## bg_jobs: универсальный механизм фоновых задач

Таблица `bg_jobs` — единственный источник истины для любых
долгих операций (сбор данных, фичи, обучение, симуляция, bootstrap).
Воркеры читают свой kind, обновляют `progress / total / message`,
api-gateway транслирует каждое изменение в `/ws/bg-jobs`. Фронтенд
подписывается на этот канал один раз — карточки прогресса появляются
везде, где они логически уместны.

**Пулы воркеров** (избегает head-of-line blocking):

| Pool    | Воркеров | Kinds                                                                 |
|---------|----------|------------------------------------------------------------------------|
| io      | 1        | `collect_history`, `refresh` (GitHub rate-limit делает параллелизм вредным) |
| compute | 3        | `bootstrap`, `compute_features`, `train_model`, `simulate`             |

Bootstrap-orchestrator живёт в `compute` пуле и **сам ждёт** между
фазами через polling `bg_jobs.status` — это гарантирует, что
`train_model` не запустится пока `collect_history` не завершилась.

## WebSocket-каналы

| Канал | Назначение |
|---|---|
| `/ws/bootstrap`        | Прогресс bootstrap-чейна. |
| `/ws/bg-jobs`          | Прогресс любого фонового job'а. |
| `/ws/queue`            | Live-очередь + webhook-feed для `/dashboard`. |
| `/ws/training/:id`     | Per-iteration loss/RMSE для конкретного training run. |

Все каналы транслируют JSON-сообщения. api-gateway держит in-process
pub/sub (sync.RWMutex + buffered channels). Когда понадобится горизонтальное
масштабирование — Redis Pub/Sub уже подключён, останется добавить мост.

## База данных

Ключевые таблицы (`db/migrations/`):

- `repos` — подключённые репозитории, статус сбора, denormalised counters.
- `workflow_runs`, `jobs`, `commits` — сырые данные GitHub Actions.
- `features` — материализованные feature_vectors (JSONB + версия схемы).
- `models` — обученные модели + метрики + feature_importance.
- `predictions` — `(job_id, model_id) → predicted_sec`.
- `bg_jobs` — все фоновые операции с прогрессом.
- `training_metrics` — per-iteration `(train_loss, val_rmse, val_mae)`.
- `sim_runs` — результаты симуляций стратегий.
- `webhook_events` — последние 50 GitHub webhook'ов с результатом HMAC.
- `activity_log` — журнал пользовательских действий для `/admin`.
- `system_state` — single-row settings (`bootstrap_done`,
  `active_strategy`, `custom_weights`).

Миграции встроены в api-gateway (`go:embed`) и накатываются на старте.
Внешний инструмент типа goose-CLI не нужен.

## Дизайн-система

> Полная спецификация — в плане проекта. Ключевое:
>
> - Дизайн-токены в `frontend/src/styles/tokens.css` — единственное
>   место, где определены цвета, шрифты, размеры.
> - Два шрифта: **Inter** (UI/display) + **JetBrains Mono** (все
>   технические значения: id, sha, sec, метрики). Моноширинный шрифт
>   и делает интерфейс похожим на инструмент, а не на маркетинговую
>   страницу.
> - Один акцентный цвет: тёплый янтарь `#F2C94C`. Статусные цвета
>   (зелёный/жёлтый/красный/синий) — только для статусов, не для
>   декора.
> - Углы 6–8px, без теней, без градиентов.
> - Плотность Linear-уровня (строка таблицы 36–40 px).
> - Анимации: 120 мс hover, 180 мс entry, 240 мс modal. Без bouncy-springs.
> - Никаких shadcn-дефолтов, MUI-дефолтов, фиолетово-синих градиентов
>   и glassmorphism. Эти штуки сигналят «сгенерировано шаблоном».

## i18n

Простая собственная реализация без библиотек, всё в `frontend/src/i18n/`:

- `types.ts` — TypeScript-union всех ключей перевода. Если в `en.ts`
  или `ru.ts` пропущен ключ — TS падает на билде.
- `en.ts` / `ru.ts` — плоские словари.
- `index.tsx` — `LocaleProvider`, `useT()`, `<LanguageSwitcher />`.

Локаль хранится в `localStorage["cicd-ml.locale"]`. Первый визит
автоопределяется по `navigator.language` (всё, что начинается с `ru-`
— русский, остальное английский).

`<LanguageSwitcher compact />` смонтирован в:
1. шапке `AppShell` (доступен на любой странице кроме `/setup`),
2. `/setup` (правый верхний угол, чтобы выбрать язык до онбординга).

## Почему Go + Python (а не один язык)

- **Go** для всего I/O-bound: HTTP-сервер, Postgres pool, Redis pub/sub,
  ингест GitHub API, WebSocket fan-out. Дешёвые горутины,
  предсказуемая латентность. Здесь же scheduler и simulator —
  алгоритмически чистая логика, которой удобно жить в одной БД с
  ингестом.
- **Python** для ML: каждая нужная библиотека (XGBoost, LightGBM,
  scikit-learn, Optuna, matplotlib) здесь best-in-class. Ad-hoc анализ
  в `ml/notebooks/` использует то же окружение что и сервис.

Граница узкая: api-gateway зовёт ml-service через HTTP (JSON в обе
стороны). Никакого shared-кода — только схема БД и формат ошибок
выступают контрактом.
