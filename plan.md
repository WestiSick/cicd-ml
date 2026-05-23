# План разработки: интеллектуальная система прогнозирования и планирования очередей CI/CD

## Контекст

Магистерская диссертация: «Разработка интеллектуальной системы прогнозирования времени выполнения и планирования очередей CI/CD на основе ML».

Нужно построить веб-приложение, которое:
1. **Прогнозирует** время выполнения CI/CD-задач (job/workflow) на основе исторических данных GitHub Actions.
2. **Планирует** очередь сборок, используя предсказания и сравнивая стратегии **FIFO / SJF / EDF / custom**.
3. **Демонстрирует** качество моделей и стратегий через интерактивные дашборды — материал для практической главы и защиты.
4. **«Из коробки» уже обучено**: при первом запуске система автоматически (без участия пользователя, без CLI-команд) подтягивает датасет из 5–10 публичных репозиториев и тренирует все модели в фоне. Пользователь сразу видит прогресс в браузере.
5. **Real-time реакция на push**: коммит в подключённую ветку моментально появляется в дашборде с прогнозом, а по мере выполнения сборки — обновляется фактическое время и дельта (prediction error) без перезагрузки страницы.
6. **Полностью UI-driven управление данными и моделями**: добавление репозиториев, запуск сбора данных, обучение моделей, просмотр метрик и распределений — всё через веб-интерфейс. Никаких ручных `make`-команд, `python` или `psql` для штатных сценариев. CLI остаётся только для разработки и редких задач (миграции, бэкапы).

Каталог пустой (`C:\Users\buzdi\GolandProjects\cicd-ml`) — greenfield.

Решённые ранее вопросы:
- Backend: **Go** (API + scheduler + collector), ML: **Python + FastAPI** (XGBoost/LightGBM, Random Forest, Linear Regression, MLP/LSTM).
- Источник данных: **GitHub Actions REST API** (публичные репозитории + опционально свой).
- Хранилище: **PostgreSQL + Redis**.
- Frontend: **React + TypeScript + Vite**.
- Режим: **гибрид — webhook-receiver принимает реальные события, исполнение в симуляторе** (без реального пула runners).
- UI: дашборд очереди, страница ML-экспериментов, симулятор стратегий, админка.

## Архитектура (целевая)

```
cicd-ml/
├── docker-compose.yml                  # БАЗА: postgres, redis, ml-service, api, frontend
├── docker-compose.dev.yml              # OVERRIDE для разработки: hot-reload, volumes на исходники, debug-порты
├── docker-compose.prod.yml             # OVERRIDE для прода: Traefik reverse-proxy, Let's Encrypt, без dev-сервера
├── deploy/
│   ├── traefik/                        # конфиг Traefik для прода (HTTPS, домен)
│   ├── nginx/                          # альтернативный reverse-proxy (опционально)
│   └── systemd/                        # unit-файлы для VPS (опционально)
├── .env.example                         # для разработки
├── .env.prod.example                    # для прода (домен, email для LE, секреты)
├── README.md                            # ПОЛНАЯ инструкция: установка, использование, прод-деплой
├── docs/
│   ├── usage.md                         # подробные сценарии работы с UI
│   ├── deployment.md                    # деплой на VPS пошагово
│   ├── architecture.md                  # архитектура для научного руководителя
│   └── thesis/                          # выгрузки графиков/таблиц для диссертации
│
├── services/
│   ├── api-gateway/        (Go)        # REST + WebSocket для фронта, webhook от GitHub, auth
│   ├── scheduler/          (Go)        # очередь, стратегии FIFO/SJF/EDF/custom, Redis
│   ├── collector/          (Go)        # тянет runs/jobs из GitHub Actions API → Postgres
│   ├── simulator/          (Go)        # replay исторических потоков через стратегии
│   └── ml-service/         (Python)    # FastAPI: /train, /predict, /models, /metrics
│
├── frontend/               (React+TS+Vite)
│
├── db/
│   ├── migrations/                     # goose миграции для Postgres
│   └── seed/                            # синтетические данные для отладки
│
├── ml/
│   ├── notebooks/                       # EDA, отбор фич, эксперименты (для главы 3)
│   ├── features/                        # пайплайн фич: pandas → parquet/Postgres
│   ├── models/                          # обёртки: linear, rf, xgb, lgbm, mlp, lstm
│   ├── training/                        # CLI: train.py --model xgb --dataset ...
│   └── registry/                        # локальный артефакт-стор моделей (joblib + meta)
│
└── docs/
    └── thesis/                          # выгрузки графиков/таблиц для диссертации
```

**Поток данных:**

1. `collector` → GitHub Actions API → Postgres (`workflow_runs`, `jobs`, `commits`, `repos`).
2. `ml/features` → инкрементально считает признаки → таблица `features`.
3. `ml-service` тренирует модели на `features` → сохраняет в `registry` + регистрирует в `models` (Postgres).
4. GitHub webhook → `api-gateway` → формирует фичи → `ml-service.predict` → задача в Redis-очередь с предсказанием.
5. `scheduler` выбирает порядок по активной стратегии → `simulator` имитирует исполнение → результат в `runs` (предсказание vs факт).
6. Фронт: WebSocket-стрим состояния очереди + REST для аналитики.

## Ключевые компоненты и решения

### 1. Сбор данных (GitHub Actions)

- **Эндпоинты**: `GET /repos/{owner}/{repo}/actions/runs`, `/runs/{id}/jobs`, `/commits/{sha}` (для diff stats).
- **Целевые репозитории для датасета**: 5–10 крупных публичных репо с активным CI (например, `kubernetes/kubernetes`, `prometheus/prometheus`, `vitejs/vite`, `microsoft/vscode`). Объём ~50–200K job-записей за разумное окно — достаточно для тезиса.
- **Rate limiting**: GitHub token + `If-Modified-Since`, выдержка между запросами; collector работает как фоновой воркер с чекпоинтами в Postgres.
- **Хранение сырых данных**: таблицы `repos`, `workflow_runs`, `jobs`, `commits`, `commit_files` — нормализованно.

### 2. Признаки (feature engineering)

Базовый набор для регрессии длительности job:
- **Repo**: язык, размер, средний `job_duration` last 30 дней, число воркфлоу.
- **Commit**: `files_changed`, `additions`, `deletions`, доля тестовых файлов, доля конфиг-файлов.
- **Branch**: main/release/feature (one-hot), длина имени.
- **Author**: историческое среднее, p50/p90 длительности, число коммитов автора.
- **Workflow**: имя job, runner type, matrix dimension, число шагов.
- **Time**: час, день недели, выходной (для эффекта загрузки runners GitHub).
- **Rolling**: скользящие медианы по job_name за 7/30 дней.

Реализация: Python + pandas, материализация в `features` (Parquet + Postgres).

### 3. ML-сервис (Python, FastAPI)

Модели (все за единым интерфейсом `BaseModel.fit/predict/save/load`):
- **Линейная регрессия** — baseline.
- **Random Forest** — нелинейный baseline.
- **XGBoost / LightGBM** — основная гипотеза, ожидаемо лучшая.
- **MLP** (PyTorch) — DL baseline.
- **LSTM** — на временных рядах rolling-фич (для главы «использование temporal-зависимостей»).

Метрики: **MAE, RMSE, MAPE, R²**, плюс отдельно — **точность ранжирования** (Spearman, NDCG@k) — это критично, потому что для SJF важна не абсолютная ошибка, а правильный порядок.

Эксперименты: time-based split (train < cutoff < test), исключая random shuffle; cross-validation с временным окном.

API:
- `POST /train` — запуск тренировки модели (фон, статус через `/jobs/{id}`).
- `POST /predict` — батч или одна задача.
- `GET /models` — список с метриками, активная модель.
- `POST /models/{id}/activate` — выбрать активную модель для scheduler.
- `GET /metrics` — текущие метрики на свежем тесте (для дашборда).

### 4. Планировщик (Go)

Стратегии за единым интерфейсом `Strategy.NextJob(queue) Job`:
- **FIFO** — порядок поступления (контроль).
- **SJF** — по возрастанию `predicted_duration`.
- **EDF** — учитываем `deadline` (для PR в main — короткий SLA, для feature-веток — длиннее).
- **Custom** — взвешенный приоритет: `score = w1·short_job + w2·deadline_proximity + w3·branch_importance`. Веса конфигурируемы.

Состояние очереди — в **Redis** (sorted set по score). Воркеры scheduler читают и отдают `simulator`.

### 5. Симулятор стратегий

Два режима:
- **Online**: реальный поток из webhook receiver, исполнение имитируется sleep’ом на `predicted_duration` (или из конфига — на `actual_duration` если известно).
- **Replay**: берёт исторические `runs` за окно (например, 7 дней), прогоняет через каждую стратегию, считает метрики:
  - `makespan` (общее время потока),
  - средний / p95 `wait_time`,
  - `throughput`,
  - доля SLA-нарушений (для EDF/custom).

Результаты экспорта в CSV + графики (matplotlib) → `docs/thesis/` для иллюстраций в диссертации.

### 6. API Gateway (Go)

- REST: `/api/runs`, `/api/queue`, `/api/models`, `/api/strategies`, `/api/simulator/run`, `/api/repos`, `/api/datasets`, `/api/bg-jobs`, `/api/training`.
- Управление репозиториями: `POST /api/repos` (по URL), `POST /api/repos/:id/sync`, `POST /api/repos/:id/pause`, `DELETE /api/repos/:id`.
- Обучение: `POST /api/training` (algo, dataset_filter, hyperparams) → возвращает `bg_job_id`. `GET /api/training/:id` — статус. `POST /api/models/:id/activate`.
- WebSocket каналы:
  - `/ws/queue` — push-обновления состояния очереди и live-feed пушей.
  - `/ws/bg-jobs` — прогресс всех фоновых задач (сбор, фичи, обучение, симуляция).
  - `/ws/training/:id` — стрим метрик и логов конкретной тренировки.
  - `/ws/bootstrap` — прогресс первичного онбординга.
- Webhook: `POST /webhooks/github` — приём `workflow_run` событий, валидация HMAC.
- Auth: простой JWT (один пользователь — автор диссертации); это не корпоративный продукт.
- HTTP-фреймворк: `chi` или `gin` (предпочитаю `chi` — минималистичный, стандартный).

### 7. Frontend (React + TS + Vite)

Страницы:

1. **`/dashboard`** — живая очередь (карточки job с предсказанным временем, прогресс-бары), KPI-блок (текущая модель + её MAE/MAPE, средний wait_time, активная стратегия), мини-график загрузки за 24ч, лента live-событий пушей.

2. **`/datasets`** — главная страница управления данными, полностью UI-driven:
   - **Карточки репозиториев** (предзагруженные seed + добавленные пользователем): owner/name, ветки в трекинге, статус сбора (idle / fetching / synced / error), количество собранных runs/jobs, период покрытия (oldest → newest), последнее обновление.
   - Кнопка **«Add repository»** — модальное окно: вставить URL `https://github.com/owner/repo` (парсинг и валидация), выбрать ветки, период (last 3/6/12 месяцев), запустить.
   - **Live-прогресс сбора**: для каждого активного fetch — прогресс-бар по страницам GitHub API, ETA, скорость (jobs/sec), счётчик rate-limit (`4982/5000 remaining, reset in 23m`), журнал последних событий.
   - **Действия по карточке**: Pause / Resume / Refresh / Re-sync from scratch / Remove.
   - **Глобальные действия**: «Compute features» (пересчёт фич для всех новых job), «Export dataset» (CSV/Parquet).
   - **Карта временного покрытия** — heatmap: репозиторий × дата, цвет = плотность job. Видно дыры в данных.

3. **`/datasets/:id`** — детальная страница одного репозитория:
   - **Статистика**: распределение длительностей (гистограмма), топ job_name по количеству, распределение по успех/провал, средние по веткам.
   - **Список workflows** с числом запусков и средним временем.
   - **Превью feature matrix** — первые 50 строк таблицы фич с фильтрами по job_name/branch.

4. **`/experiments`** — обучение и сравнение моделей, всё через UI:
   - **Таблица обученных моделей**: имя, алгоритм, гиперпараметры, MAE/RMSE/MAPE/R², Spearman, размер обучающей выборки, время обучения, дата, статус (`active`/`available`), действия (Activate / Compare / Delete / Download artifact).
   - Кнопка **«Train new model»** — мастер:
     1. Выбор алгоритма (Linear / RF / XGBoost / LightGBM / MLP / LSTM).
     2. Выбор датасета (фильтр по репозиториям и периоду).
     3. Гиперпараметры (форма + ползунки + опционально «Optuna search» с указанием бюджета trials).
     4. Train/test split: time-based с указанием cutoff (визуализирован на таймлайне).
     5. Кнопка **Start training**.
   - **Live-страница тренировки** (`/experiments/jobs/:trainingId`):
     - Прогресс-бар по эпохам/итерациям бустинга.
     - Live-график loss / validation MAE / RMSE по итерациям (WebSocket-стрим из ml-service).
     - Логи (последние 200 строк, авто-скролл).
     - Кнопка **Cancel**.
     - После завершения — таблица итоговых метрик, residuals plot, predicted vs actual, feature importance, ссылки «Compare with active model».
   - **Сравнение моделей**: выбрать 2–5, увидеть метрики бок-о-бок + overlaid predicted-vs-actual.

5. **`/simulator`** — what-if: выбрать окно истории, выбрать стратегии для сравнения, запустить, увидеть результаты makespan / wait p95 / SLA на одном графике (с экспортом CSV/PNG для диссертации).

6. **`/admin`** — настройки: GitHub PAT (хранится зашифрованным), вебхуки и их статус, конфиг весов custom-стратегии, выбор активной модели и стратегии планировщика, управление пользователями (один, но интерфейс готов), системные настройки (частота rolling-фич, лимиты).

### 7.1 Система обратной связи UI (важно — каждое действие даёт явный сигнал)

Любая кнопка/форма в системе даёт пользователю один из четырёх типов фидбека. Это сквозное требование, реализуется на уровне общего слоя.

**Технический слой:**
- `react-hot-toast` (или `sonner`) — единая система toast-уведомлений в правом-нижнем углу.
- `@tanstack/react-query` для всех мутаций — встроенные состояния `pending / success / error`, автоматический revalidate данных.
- **Глобальный `<ApiErrorBoundary>`** — ловит любые необработанные ошибки запросов, показывает баннер с возможностью повторить.
- **Скелетоны и спиннеры** во всех загружающихся блоках — отдельный пустой state с пояснением, если данных нет.

**Правила фидбека (применяются ко всем экранам):**

| Тип действия | Что видит пользователь |
|---|---|
| Старт асинхронной операции (тренировка, сбор) | Toast «Training started for XGBoost» + кнопка переключается в spinner-состояние с надписью «Starting…» |
| Успех быстрой операции (Activate model, Save settings) | Toast зелёный «Model activated» + кнопка короткое время показывает галочку |
| Ошибка валидации формы | Подсветка поля + текст под полем, форма не отправляется, без toast |
| Ошибка API (4xx) | Toast красный «Failed to add repo: repository not found» + текст ошибки рядом с кнопкой |
| Сетевая ошибка / 5xx | Toast красный «Connection lost. Retrying in 5s…» + автоматический retry, кнопка «Retry now» |
| Длительная операция в фоне | Карточка в `/datasets` или `/experiments` со статусом-чипом (queued / running / done / failed) + прогресс-бар + ссылка «View logs» |
| Подтверждение опасных действий (Delete repo, Delete model) | Modal с двойным подтверждением: вводом имени или explicit checkbox |

**Унифицированный формат ошибок от backend** (Go и Python отдают одно и то же):

```json
{
  "error": {
    "code": "github_rate_limited",
    "message": "GitHub API rate limit exceeded",
    "details": { "reset_at": "2026-05-14T19:42:00Z" },
    "user_action": "Wait until reset or add a GitHub token in /admin"
  }
}
```

Фронт показывает `user_action` пользователю напрямую — никаких stack trace и кодов в UI.

**Журнал действий пользователя** (`/admin → Activity log`) — последние 200 действий с результатом (success/fail), временем и кратким сообщением. Полезно, если пользователь не понял, что прошло, а что нет.

**Health-индикатор** в шапке: цветная точка (зелёный/жёлтый/красный) с тултипом — статус ml-service, scheduler, БД, Redis, последний heartbeat WebSocket. Клик ведёт на `/admin → System health`.

Стек UI:
- `@tanstack/react-query` для серверного состояния,
- `react-router-dom` для роутинга,
- **`Radix UI` primitives** (headless) + **vanilla-extract** или **CSS Modules** для стилей — даёт полностью кастомный визуал без «шаблонного» вида MUI/shadcn,
- **`visx`** для графиков вместо recharts — даёт editorial-вид графиков как у FT/NYT/Observable, без «дашбордного» шаблона. Recharts оставим только для одного-двух простых графиков.

### 7.2 Дизайн-система (не «AI-generated» вид)

Цель: интерфейс должен выглядеть как профессиональный инженерный инструмент (Linear, Vercel, Datadog, Grafana, Observable), а не как очередной landing page из shadcn-туториала.

**Чего избегать (типичные маркеры «сгенерированного ИИ» дизайна):**
- Фиолетово-синие градиенты на hero-блоках.
- Дефолтные палитры Tailwind (`slate-900`/`indigo-500` сочетания).
- «Glassmorphism» и blur-эффекты без причины.
- Все углы скруглены до `rounded-2xl`, всё с тенями.
- Эмодзи в заголовках разделов.
- Иконки рядом с каждым словом «для красоты».
- Бесконечные пустые отступы (`p-12` повсюду).
- Stock-иллюстрации, абстрактные «волны» в фоне.
- Дефолтные шрифты — Inter везде, без иерархии.

**Дизайн-направление: «scientific instrument»**

Референсы (буквально открыть и сверяться):
- **Linear.app** — типографика, плотность, состояния списков.
- **Vercel dashboard** — таблицы, графики, переходы.
- **Observable / observablehq.com** — графики, типографика для данных.
- **Tailscale admin** — формы и настройки.
- **Resend dashboard** — карточки и пустые состояния.
- **Datadog** — плотные технические таблицы.

**Конкретные технические решения:**

1. **Типографика** (две гарнитуры, точно):
   - **Display/UI**: `Geist` (sans, есть бесплатно от Vercel) или `Söhne` (если доступен) — отличается от затёртого Inter. Веса: 400, 500, 600.
   - **Mono**: `JetBrains Mono` или `Geist Mono` — для всех технических данных: id, sha, длительности, метрики, имена job. Это сразу даёт «инженерный» вид.
   - Размерная шкала: 12 / 13 / 14 / 16 / 20 / 28 / 40. Без промежуточных.
   - `letter-spacing` для caps-заголовков: -0.01em для display, +0.04em для small-caps лейблов.

2. **Палитра** (constrained, не Tailwind-default):
   ```
   bg-base:      #0E0F11   (dark mode primary)
   bg-elevated:  #16181C
   bg-overlay:   #1C1F24
   border-subtle:#23262C
   border-strong:#2E323A
   text-primary: #ECEDEE
   text-secondary:#9BA1A6
   text-tertiary:#6F757C
   accent:       #F2C94C   (тёплый янтарь — единственный акцент, для активных состояний)
   ok:           #4ADE80
   warn:         #FBBF24
   err:          #F87171
   info:         #60A5FA
   ```
   Светлая тема — инверсия с тем же янтарным акцентом. Никаких градиентов.

3. **Раскладка**:
   - Сетка 8px, но колонки контента — 1240px max-width, не «full-bleed».
   - Левый сайдбар 240px, фиксированный, без collapse-анимаций.
   - Плотность как в Linear: строка таблицы 36–40px, не 64px.
   - Разделители — `1px solid border-subtle`, не тени.

4. **Компоненты**:
   - Кнопки: квадратные углы `radius: 6px` (не 16px), без gradient-фонов, primary — фон `text-primary` + текст `bg-base` (инверсия), secondary — outline + `border-strong`.
   - Поля ввода: `radius: 6px`, фокус — `border-strong + 0 0 0 3px accent/20%`, не «glow».
   - Карточки: `radius: 8px`, `border 1px subtle`, без теней. Только при hover чуть подсветка границы.
   - Чипы статусов: pill (`radius: 999px`), но с моно-шрифтом и uppercase small-caps — «инструмент», не «соцсеть».
   - Таблицы: zebra нет, разделители строк только subtle border-bottom. Sticky header. Числа — моно-шрифт, выровнены по правому краю.
   - Графики: тонкие линии (1.5px), один цвет на серию из палитры, явные подписи осей моно-шрифтом, без 3D и градиентов.

5. **Микровзаимодействия**:
   - Длительности анимаций: 120ms (hover), 180ms (entry), 240ms (modal). Easing: `cubic-bezier(0.2, 0.0, 0.0, 1.0)`.
   - Никаких bouncy/spring анимаций.
   - Skeleton-плейсхолдеры в цвет `bg-elevated`, без shimmer-градиента.

6. **Иконография**:
   - **Один** набор: `Lucide` или `Phosphor (regular weight)`. Не миксовать.
   - Иконки только там, где они несут смысл (статус, действие), не как декорация рядом с заголовками.
   - Размер по умолчанию 14px, stroke 1.5.

7. **Пустые состояния и онбординг**:
   - Не «милые» иллюстрации — короткий текст + одна явная следующая кнопка-действие.
   - Пример: «No models trained yet. → [Train your first model]» — две строки, без картинки.

8. **Особые элементы для технического вида**:
   - **Хедер с monospace breadcrumb**: `datasets › vitejs/vite › jobs/build`.
   - **Command palette** (`Cmd+K`) — поиск по репо/моделям/действиям. Сразу даёт «инструмент Linear-уровня».
   - **Keyboard shortcuts** на всех частых действиях (`G D` — datasets, `G E` — experiments, `N` — new model). Подсказки в меню справа моно-шрифтом.
   - **Footer-bar** на странице training: моно-строка с метриками, обновляется live (`iter 142/1000 · val_mae 38.2s · 0.024s/iter · ETA 22s`).

**Шаблоны страниц** (обязательно)

Каждая страница построена из одних и тех же блоков:
```
┌─────────────────────────────────────────────┐
│ TopBar: monospace breadcrumb     · health · │
├──────┬──────────────────────────────────────┤
│      │ PageHeader: H1 + actions (right)     │
│ Side │ ──────────────────────────────────── │
│ bar  │ Tabs / Filters (small-caps)          │
│      │ ──────────────────────────────────── │
│      │ Content (table / cards / chart)      │
│      │                                       │
│      │ Pagination / Status bar (mono)       │
└──────┴──────────────────────────────────────┘
```

**Тёмная тема — основная** (как у Linear / Vercel / Datadog). Светлая — переключаемая, но дефолт = тёмная.

**Скриншоты для диссертации** — делать в тёмной теме, на разрешении 1440×900, без курсора, с реальными данными (не Lorem). Это критично для «солидного» вида в защите.

**Не используем** (явно):
- shadcn/ui (слишком узнаваемый «AI-look» в 2026).
- Любые «AI app» темплейты (Vercel AI templates, etc.).
- Anime.js, framer-motion для декоративных анимаций.
- Default MUI/Chakra/Mantine без глубокой кастомизации темы.

### 8. БД-схема (ключевые таблицы)

```sql
repos (id, owner, name, github_id, added_at, status, last_synced_at, oldest_run_at, newest_run_at, runs_count, jobs_count)
workflow_runs (id, repo_id, run_id, head_sha, event, status, conclusion, created_at, run_started_at, updated_at)
jobs (id, run_id, name, status, conclusion, started_at, completed_at, duration_sec, runner_name, runner_group)
commits (sha, repo_id, author, message, files_changed, additions, deletions)
features (job_id PK, feature_vector JSONB, computed_at)
models (id, name, algo, params JSONB, metrics JSONB, artifact_path, trained_at, active BOOL, training_job_id)
predictions (job_id, model_id, predicted_sec, made_at)
queue_state (job_id, strategy, score, enqueued_at, dequeued_at)
sim_runs (id, strategy, window_start, window_end, makespan, wait_p50, wait_p95, sla_violations, created_at)

-- для UI-управления фоновыми задачами:
bg_jobs (id, kind, payload JSONB, status, progress INT, total INT, message TEXT,
         logs_tail TEXT, error TEXT, created_at, started_at, finished_at)
-- kind: 'collect_history' | 'compute_features' | 'train_model' | 'simulate' | 'refresh'
training_metrics (training_job_id, iteration, train_loss, val_mae, val_rmse, val_mape, ts)
system_state (key PK, value JSONB)  -- хранит bootstrap_done, активную стратегию, веса custom
```

`bg_jobs` — единая таблица задач, на которую подписан фронт через WebSocket. Любая UI-операция (сбор, обучение, симуляция) создаёт строку и стримит прогресс.

Миграции: **goose** (Go-friendly, одна тулза для всех Go-сервисов).

## Bootstrap «из коробки» (важно)

Чтобы при первом запуске система была сразу готова к демонстрации — **без единой команды от пользователя**, всё в фоне и видимо в UI:

1. **Зафиксированный список репо** в `db/seed/seed_repos.yaml` (исходники, не для пользователя):
   ```yaml
   repos:
     - { owner: vitejs,     name: vite }
     - { owner: prometheus, name: prometheus }
     - { owner: gin-gonic,  name: gin }
     - { owner: fastapi,    name: fastapi }
     - { owner: pandas-dev, name: pandas }
     - { owner: golang,     name: go }
     - { owner: pallets,    name: flask }
   ```

2. **Автостарт `bootstrap-orchestrator`** — отдельный воркер в составе `api-gateway`, запускается при первом старте контейнера:
   - проверяет флаг `bootstrap_done` в Postgres → если нет:
   - регистрирует seed-репозитории в таблице `repos`,
   - ставит задачи в очередь `bg_jobs`: `collect_history`, `compute_features`, `train_all_models`, `simulate_baseline`,
   - воркеры подбирают и выполняют последовательно,
   - **весь прогресс публикуется в WebSocket-канал `/ws/bootstrap`**, фронт показывает экран «Initial setup: 3/7 repos collected, training XGBoost…»,
   - по завершении ставит флаг `bootstrap_done=true`.

3. **Фронт при первом заходе** (если `bootstrap_done=false`) показывает **онбординг-страницу `/setup`**:
   - Поле «GitHub PAT» (опционально, без него лимит 60 req/h — медленно, но работает).
   - Чекбоксы: какие из 7 seed-репозиториев включить (все по умолчанию).
   - Слайдер «Период истории»: 3 / 6 / 12 месяцев (по умолчанию 6).
   - Кнопка **«Start setup»**. Дальше — экран с live-прогрессом по этапам, каждая фаза с прогресс-баром:
     1. Создание GitHub webhook’ов (если PAT дан).
     2. Сбор данных по каждому репо (прогресс отдельно).
     3. Расчёт фич.
     4. Обучение моделей (Linear → RF → XGBoost → LightGBM → MLP; LSTM как опция-галочка).
     5. Базовая симуляция стратегий.
   - После завершения redirect на `/dashboard`.

4. **Кэш-снапшот для проверяющего диссертацию**: один раз администратор (разработчик) может выгрузить `db/seed/snapshot.sql.gz` — при наличии этого файла в монтированном volume `bootstrap-orchestrator` восстанавливает БД из дампа за 1–2 минуты вместо часов и сразу ставит `bootstrap_done=true`. Это разовая опция, не часть пользовательского сценария.

5. **Резервный план**: если GitHub API упрётся в rate limit, оркестратор сохраняет прогресс и продолжает после reset; пользователь видит таймер обратного отсчёта.

После первого старта: пользователь открывает `http://localhost:5173`, проходит онбординг **только в UI**, и через 30–120 минут (или 2 минуты со снапшотом) видит обученные модели на `/experiments` и реальную историю на `/datasets`.

## Real-time поток push → дашборд

Цепочка событий при пуше в подключённый репозиторий:

```
git push
  └─> GitHub webhook (workflow_run.requested)
       └─> POST /webhooks/github (api-gateway)
            ├─> ml-service.predict()           ← быстрый прогноз < 50ms
            ├─> INSERT predictions             ← запись в БД
            ├─> Redis LPUSH queue:<strategy>   ← постановка в очередь
            └─> WebSocket broadcast            ← фронт мгновенно обновляется
                  ├─ событие "job.enqueued"  → карточка появляется в очереди с predicted_sec
GitHub Actions исполняет job (реально)
  └─> webhook workflow_run.in_progress
       └─> WebSocket broadcast "job.started"  → таймер на карточке стартует
  └─> webhook workflow_run.completed
       └─> UPDATE jobs SET actual_sec = ...
       └─> WebSocket broadcast "job.completed" с фактическим временем
            └─ карточка показывает: predicted 4m12s | actual 3m58s | error -5.3%
```

UI-элементы для real-time:
- **`/dashboard` → "Live feed"** — список последних 20 событий, новые сверху, с подсветкой свежих 5 секунд.
- **Карточка job** содержит: репо, ветка, автора коммита, имя job, прогноз, факт (если есть), δ-ошибку, статус (queued/running/completed/failed).
- **WebSocket подключение** — `wss://.../ws/queue`, авто-reconnect, heartbeat каждые 30с.

Технически: api-gateway хранит набор активных подписчиков (`map[clientID]chan Event`), при любой записи в `predictions`/`jobs` через шину событий (внутренний `event-bus` поверх Redis Pub/Sub) рассылает всем.

## Docker Compose: dev и prod в одной системе

Принцип: один базовый `docker-compose.yml` + overrides для dev и prod. Это позволяет одной командой запустить локально, а на VPS — той же кодовой базой с домен/SSL.

### Базовый `docker-compose.yml` (общий)

Сервисы:
- `db` — Postgres 16, persistent volume, healthcheck.
- `redis` — Redis 7, persistent volume.
- `api` — Go api-gateway (включает scheduler и bootstrap-orchestrator).
- `collector` — Go-воркер сбора данных.
- `ml` — Python ml-service (FastAPI + uvicorn).
- `frontend` — собранный статический фронт (nginx, отдающий dist).

Все сервисы — в одной internal сети, наружу торчит только один порт (через reverse-proxy в проде, или напрямую в dev).

### `docker-compose.dev.yml` (override для разработки)

```yaml
services:
  api:
    build:
      target: dev          # Dockerfile.dev с air для hot-reload
    volumes:
      - ./services/api-gateway:/app
    ports: ["8080:8080"]   # прямой доступ для отладки
  ml:
    build:
      target: dev          # uvicorn --reload
    volumes: [./services/ml-service:/app]
    ports: ["8000:8000"]
  frontend:
    build:
      target: dev          # vite dev server
    volumes: [./frontend:/app, /app/node_modules]
    ports: ["5173:5173"]
  db:
    ports: ["5432:5432"]   # доступ pgAdmin/DBeaver с хоста
```

Запуск: `docker compose -f docker-compose.yml -f docker-compose.dev.yml up` или через `make dev`.

### `docker-compose.prod.yml` (override для прода с доменом и SSL)

Добавляет **Traefik** как reverse-proxy с автоматическим Let's Encrypt:

```yaml
services:
  traefik:
    image: traefik:v3
    command:
      - --providers.docker=true
      - --providers.docker.exposedbydefault=false
      - --entrypoints.web.address=:80
      - --entrypoints.websecure.address=:443
      - --entrypoints.web.http.redirections.entrypoint.to=websecure
      - --entrypoints.web.http.redirections.entrypoint.scheme=https
      - --certificatesresolvers.le.acme.email=${LE_EMAIL}
      - --certificatesresolvers.le.acme.storage=/letsencrypt/acme.json
      - --certificatesresolvers.le.acme.tlschallenge=true
    ports: ["80:80", "443:443"]
    volumes:
      - traefik-letsencrypt:/letsencrypt
      - /var/run/docker.sock:/var/run/docker.sock:ro

  api:
    build: { target: prod }
    labels:
      - traefik.enable=true
      - traefik.http.routers.api.rule=Host(`${DOMAIN}`) && PathPrefix(`/api`,`/ws`,`/webhooks`)
      - traefik.http.routers.api.entrypoints=websecure
      - traefik.http.routers.api.tls.certresolver=le
      - traefik.http.services.api.loadbalancer.server.port=8080

  frontend:
    build: { target: prod }   # nginx с собранным dist
    labels:
      - traefik.enable=true
      - traefik.http.routers.front.rule=Host(`${DOMAIN}`)
      - traefik.http.routers.front.entrypoints=websecure
      - traefik.http.routers.front.tls.certresolver=le
      - traefik.http.services.front.loadbalancer.server.port=80

  # db, redis, ml — без меток Traefik, недоступны снаружи

volumes:
  traefik-letsencrypt:
```

`.env.prod`:
```
DOMAIN=cicd-ml.example.com
LE_EMAIL=buzdin.vadim@gmail.com
POSTGRES_PASSWORD=<strong>
GITHUB_WEBHOOK_SECRET=<strong>
JWT_SECRET=<strong>
```

Деплой на VPS:
```bash
# на VPS, в каталоге проекта
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

DNS A-запись `cicd-ml.example.com → <VPS IP>` → через 30–60 секунд Let's Encrypt выпускает сертификат → HTTPS работает.

### Multi-stage Dockerfile-ы

Каждый сервис — multi-stage Dockerfile с двумя target’ами:
- `dev` — с hot-reload (air для Go, `uvicorn --reload` для Python, vite dev для фронта).
- `prod` — минимальный образ: `distroless` для Go, `python:3.12-slim` для ML, `nginx:alpine` для фронта со статикой.

Образы для прода — оптимизированы по размеру (Go ~30 МБ, ML ~800 МБ из-за torch, фронт ~50 МБ).

### Постоянные данные на VPS

Volumes:
- `pg-data` — БД (бэкапить!).
- `redis-data` — очередь.
- `model-artifacts` — обученные модели (`ml/registry/`).
- `traefik-letsencrypt` — сертификаты.
- `thesis-output` → mount в `/data/thesis` на хосте — графики и CSV для диссертации.

В `docs/deployment.md` — отдельный раздел про регулярные `pg_dump` (cron на хосте).

## Этапы разработки

Этапы согласованы с типичной структурой диссертации — каждый этап даёт материал для главы.

### Этап 1. Скелет инфраструктуры (1 неделя)
- `docker-compose.yml`: postgres, redis, ml-service-stub, api-gateway-stub, frontend-dev.
- Go workspace (`go.work`) с тремя модулями: api, scheduler, collector.
- Базовые `Makefile` цели: `make up`, `make migrate`, `make seed`.
- CI на самом репозитории (GitHub Actions): lint Go (golangci-lint), lint Python (ruff), build frontend.

### Этап 2. Collector + БД (1–2 недели)
- Goose-миграции по схеме выше.
- Collector с чекпоинтами: тянет 5–10 публичных репо, ~50K job-записей.
- Тесты: моки GitHub API (httpmock), e2e на pg-testcontainers.
- **Артефакт для диссертации**: первичная статистика датасета (распределение длительностей, корреляции) — `notebooks/01_eda.ipynb`.

### Этап 3. Feature engineering + первая модель (1 неделя)
- Python-пайплайн фич, материализация в Postgres + Parquet.
- Линейная регрессия + Random Forest на baseline-фичах.
- Time-based train/test split.
- **Артефакт**: график predicted vs actual, метрики в `docs/thesis/baseline_metrics.csv`.

### Этап 4. Продвинутые модели (1–2 недели)
- XGBoost, LightGBM (это два независимых эксперимента, не оба нужны).
- MLP на PyTorch.
- LSTM на временных rolling-фичах (если хватит времени — это самый рискованный кусок).
- Hyperparam search: Optuna, ограниченный бюджет (100 trials).
- **Артефакт**: сравнительная таблица моделей, feature importance.

### Этап 5. Scheduler + Simulator (1–2 недели)
- Очередь в Redis, интерфейс `Strategy`, реализации FIFO/SJF/EDF/custom.
- Симулятор replay: загружает окно `jobs` из БД, проигрывает через все стратегии.
- **Артефакт**: главный результат диссертации — сравнение стратегий на одних и тех же реальных данных. График makespan/wait p95 по стратегиям.

### Этап 6. API Gateway + Frontend (2 недели)
- REST endpoints + WebSocket для очереди.
- 4 страницы фронта (dashboard, experiments, simulator, admin).
- Скриншоты для диссертации в `docs/thesis/screenshots/`.

### Этап 7. Webhook receiver + online режим (1 неделя)
- HMAC-валидация, приём `workflow_run`, фичи в реальном времени, predict, enqueue.
- Live-демо для защиты.

### Этап 8. Доводка, эксперименты, тексты (2+ недели)
- Полный прогон экспериментов с финальными моделями и стратегиями.
- Выгрузка графиков и таблиц.
- Написание глав, синхронизация с кодом.

**Итого ~10–13 недель** активной работы.

## Критические файлы / точки расширения

- `services/scheduler/internal/strategy/strategy.go` — добавление новых стратегий.
- `services/ml-service/app/models/base.py` — интерфейс моделей.
- `services/ml-service/app/features/build.py` — фичи (главное место для итераций).
- `db/migrations/` — эволюция схемы.
- `frontend/src/pages/Simulator.tsx` — главная демо-страница.

## Используемые существующие компоненты (не писать своё)

- **chi** (`github.com/go-chi/chi/v5`) — HTTP router в Go.
- **goose** (`github.com/pressly/goose/v3`) — миграции.
- **sqlc** (`sqlc-dev/sqlc`) — типобезопасные query из SQL.
- **go-redis** — клиент Redis.
- **google/go-github** — клиент GitHub API.
- **FastAPI + uvicorn + pydantic v2** — ML-сервис.
- **scikit-learn, xgboost, lightgbm, pytorch, optuna** — стандарт.
- **@tanstack/react-query, recharts, MUI** — фронт.

## Верификация / план тестирования

1. **Юнит-тесты Go**: стратегии очереди — табличные тесты на синтетических job-наборах.
2. **Юнит-тесты Python**: модели — фитинг на seed-датасете, сериализация/десериализация, формат предсказаний.
3. **Интеграционные**: testcontainers (Postgres + Redis), e2e — webhook → predict → enqueue → simulate.
4. **ML-валидация**: time-based CV, отчёт MAE/RMSE/MAPE/R²/Spearman для всех моделей в одном CSV.
5. **Сценарий защиты** (ручной):
   - `make up` → открыть `/admin`, подключить тестовый репо.
   - Нажать «Sync» → собрать данные.
   - На `/experiments` обучить XGBoost → дождаться метрик.
   - Сделать commit в подключённый репо → увидеть его в `/dashboard` с предсказанием.
   - На `/simulator` сравнить FIFO/SJF/EDF на последних 7 днях → графики.
6. **Воспроизводимость**: `Makefile` цель `make thesis-figures` — пересобирает все графики из БД для главы 4.

## Инструкция использования (попадёт в README.md и docs/)

Инструкция структурирована в трёх документах:
- **`README.md`** — quick start + ссылки на остальное.
- **`docs/usage.md`** — детальные сценарии работы с UI (с скриншотами).
- **`docs/deployment.md`** — деплой на VPS с доменом и SSL.

Ниже — содержимое всех трёх.

### A. Локальная установка для разработки

#### Предварительные требования
- Docker Desktop (Windows/macOS/Linux), Docker Compose v2.
- ~6 ГБ свободного места.
- (Опционально) GitHub Personal Access Token с правом `public_repo` — без него API-лимит 60 req/h, сбор будет медленным. Создать: github.com/settings/tokens → Generate new token (classic) → отметить `public_repo`. **Токен вставляется в браузере на экране онбординга**, не в файле.

#### Запуск (одна команда)

```powershell
# Windows PowerShell
git clone <repo-url> cicd-ml
cd cicd-ml
copy .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

```bash
# Linux/macOS
git clone <repo-url> cicd-ml && cd cicd-ml
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
```

Открыть `http://localhost:5173`.

### B. Сценарии работы с UI

#### Первый вход — экран онбординга `/setup`

Появится автоматически, если БД пустая:

1. **Поле «GitHub Token»** — вставить PAT (или оставить пустым).
2. **Список seed-репозиториев** с чекбоксами (vite, prometheus, gin, fastapi, pandas, go, flask) — снимать ненужные, можно сразу добавить свои репо.
3. **Слайдер «История»**: 3 / 6 / 12 месяцев.
4. **Чекбоксы моделей для обучения**: Linear, RF, XGBoost (✓ по умолчанию), LightGBM (✓), MLP, LSTM.
5. Кнопка **«Start setup»**.

Дальше в реальном времени видны:
- прогресс-бары по каждому репозиторию (jobs собрано / всего),
- расчёт фич,
- кривые обучения моделей,
- финальные метрики и переход на `/dashboard`.

В любой момент можно закрыть вкладку — фон не прерывается, при возврате прогресс продолжается.

#### Сценарий 1: добавить свой репозиторий — всё в UI

1. `/datasets → Add repository`.
2. Вставить URL `https://github.com/myuser/myrepo`.
3. Выбрать ветки и период, нажать **Add**.
4. Карточка появится со статусом `fetching`, прогресс-бар обновляется в реальном времени.
5. Когда статус станет `synced`, кликнуть карточку — увидеть статистику (распределения длительностей, топ workflows, временной охват).

#### Сценарий 2: подключить real-time push → дашборд

1. `/admin → Webhooks → Setup tunnel` — система предлагает встроенный туннель (cloudflared) или показывает инструкцию для своего URL.
2. Для каждого репо в `/datasets` есть переключатель **«Live webhook»** — включает приём `workflow_run` событий от GitHub. Включение автоматически создаёт webhook в репо (если PAT даёт права; иначе показывает curl-инструкцию для ручного создания).
3. Сделать `git push` в подключённый репо.
4. На `/dashboard` через 1–2 секунды появится карточка с прогнозом. По мере выполнения CI — обновится фактическим временем и ошибкой прогноза. Всё **без обновления страницы**.

#### Сценарий 3: обучить свою модель — без CLI

1. `/experiments → Train new model`.
2. Мастер:
   - Алгоритм: XGBoost.
   - Датасет: выбрать репозитории и период.
   - Гиперпараметры: дефолт или ползунки. Или «Optuna search» с бюджетом 50 trials.
   - Train/test split: time-based, cutoff на таймлайне.
3. Кнопка **Start training**.
4. Открывается live-страница: график loss/val_MAE по итерациям, прогресс-бар, логи. Можно закрыть вкладку — обучение продолжится в фоне, статус видно на `/experiments`.
5. По окончании — таблица метрик, графики predicted vs actual и residuals, feature importance.
6. Кнопка **Activate** делает модель текущей для предсказаний.

#### Сценарий 4: сравнить стратегии планирования (материал для главы 4)

1. `/simulator → New run`.
2. Окно: `last 7 days`. Стратегии: FIFO, SJF, EDF, Custom.
3. **Run** → через ~30–60 сек появятся графики (wait p50/p95, makespan, SLA-нарушения).
4. Кнопка **Export** скачивает CSV и PNG, готовые для вставки в диссертацию.

#### Сценарий 5: дообучить модель на новых данных

1. `/datasets → Refresh all` (или на конкретной карточке) — собирает новые runs за период «с последней синхронизации».
2. После завершения сбора на `/experiments → Retrain active model` — кнопка переобучения с теми же параметрами на свежем датасете.

#### Где взять материалы для диссертации

Прямо в UI на `/experiments → Export thesis pack` и `/simulator → Export thesis pack`:
- `dataset_stats.md` — характеристики датасета,
- `model_comparison.csv` — метрики всех моделей,
- `strategy_comparison.csv` — сравнение стратегий,
- `figures/*.pdf` — графики для LaTeX.

Файлы сохраняются в `./docs/thesis/` хоста (mounted volume).

#### Команды только для разработки / редкого сопровождения

Эти команды НЕ нужны пользователю в штатном сценарии — только при разработке системы или восстановлении:

| Команда | Когда нужна |
|---|---|
| `docker compose up -d` | запуск (prod) |
| `docker compose -f docker-compose.yml -f docker-compose.dev.yml up` | запуск (dev) |
| `docker compose down` | остановка |
| `docker compose logs -f api` | отладка |
| `docker compose exec api goose -dir /migrations postgres up` | ручная миграция при обновлении кода |
| `docker compose exec db pg_dump ...` | резервная копия |

#### Если что-то пошло не так

Всё видно в UI — нет нужды лазить в логи:
- **GitHub rate limit** → в `/datasets` у карточки покажется «Rate limited, retrying in 23 min», прогресс возобновится автоматически.
- **Webhook не приходит** → `/admin → Webhooks` показывает последние 50 событий с временем и HMAC-валидацией.
- **Модель плохо предсказывает на новом репо** → на странице модели есть индикатор «coverage» (% job, для которых модель видела похожие в трейне); если низкий — добавить больше данных через `/datasets`.
- **Сервис не отвечает** → health-индикатор в шапке покажет, какой сервис упал; на `/admin → System health` — детали и кнопка «Restart service» (через Docker API).

### C. Деплой на VPS с доменом и SSL (`docs/deployment.md`)

#### Требования к VPS
- Ubuntu 22.04+ или Debian 12+.
- 4 vCPU, 8 ГБ RAM, 40 ГБ SSD (минимум; для крупных датасетов — больше).
- Открытые порты: 80, 443.
- Домен с DNS-записью `A` или `AAAA`, указывающей на IP VPS.
- Email для Let’s Encrypt.

#### Шаг 1 — установка Docker на VPS

```bash
ssh root@<VPS_IP>
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

#### Шаг 2 — деплой кода

```bash
# на VPS
git clone <repo-url> /opt/cicd-ml
cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod   # вписать DOMAIN, LE_EMAIL и сильные пароли
```

#### Шаг 3 — настройка DNS

В DNS-провайдере домена:
```
Type: A
Host: cicd-ml            (или @ для корня)
Value: <IP VPS>
TTL: 3600
```
Проверка: `dig cicd-ml.example.com +short` должен вернуть IP VPS.

#### Шаг 4 — запуск

```bash
cd /opt/cicd-ml
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
docker compose logs -f traefik   # следить за получением сертификата
```

Через 30–90 секунд Let’s Encrypt выпустит сертификат → открыть `https://cicd-ml.example.com`.

#### Шаг 5 — webhook URL для GitHub

В UI на `/admin → Webhooks` система покажет: `https://cicd-ml.example.com/webhooks/github`. Этот URL подставляется автоматически при подключении репо.

#### Бэкапы

Установить cron на VPS для ежедневного `pg_dump`:
```cron
0 3 * * * cd /opt/cicd-ml && docker compose exec -T db pg_dump -U postgres cicdml | gzip > /backup/cicdml-$(date +\%Y\%m\%d).sql.gz
```
Хранить 7 копий, старые удалять.

#### Обновление до новой версии

```bash
cd /opt/cicd-ml
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod build
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```
Миграции БД накатываются автоматически на старте `api`.

#### Безопасность прода

- Файл `.env.prod` — `chmod 600`, не коммитить.
- GitHub webhook HMAC включён по умолчанию (`GITHUB_WEBHOOK_SECRET`).
- JWT для frontend → backend, `Secure` + `HttpOnly` cookies на проде.
- Rate limiting на `/api/*` и `/webhooks/*` — через Traefik middleware.
- `fail2ban` на VPS для SSH.

## Что НЕ входит в скоуп (явно)

- Реальный пул self-hosted runners (только симуляция).
- Multi-tenant / multi-user — пользователь один.
- Платёжная биллинг-логика, прод-grade observability (Prometheus/Grafana — опционально, если останется время).
- Поддержка GitLab/Jenkins — только GitHub Actions в первой итерации (но интерфейс `collector` сделать pluggable, чтобы упомянуть в диссертации как «расширяемость»).
