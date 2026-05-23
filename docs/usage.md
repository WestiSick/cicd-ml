# Руководство пользователя

Документ описывает все сценарии работы с системой. Все действия
выполняются **в веб-интерфейсе** — CLI для штатной работы не нужен.

## Оглавление

1. [Переключение языка интерфейса](#переключение-языка)
2. [Первый запуск — экран `/setup`](#первый-запуск)
3. [Добавление своего репозитория](#сценарий-1-добавить-свой-репозиторий)
4. [Real-time реакция на `git push`](#сценарий-2-real-time-push--дашборд)
5. [Обучение модели](#сценарий-3-обучение-модели)
6. [Подбор гиперпараметров через Optuna](#сценарий-4-подбор-гиперпараметров)
7. [Сравнение стратегий планирования](#сценарий-5-сравнение-стратегий)
8. [Экспорт пакета для диссертации](#сценарий-6-экспорт-пакета-для-диссертации)
9. [Диагностика](#диагностика)

---

## Переключение языка

В правом верхнем углу любого экрана видны две моно-кнопки **EN / RU**.
Выбор сохраняется в `localStorage` и работает на всех страницах, включая
онбординг `/setup`. Первый визит выбирает язык по `navigator.language`
браузера: всё, что начинается с `ru-` → русский, остальное → английский.

## Первый запуск

Если БД пустая (флаг `bootstrap_done = false` в `system_state`), любой
URL перенаправляется на `/setup`. Форма состоит из четырёх блоков:

| # | Блок | Назначение |
|---|---|---|
| 01 | GitHub-токен | Необязателен. Без него лимит GitHub API — 60 запросов/час. |
| 02 | Стартовые репозитории | 7 публичных проектов с богатой историей CI. Снимите чекбоксы с тех, что не нужны. |
| 03 | Окно истории | 3 / 6 / 12 месяцев. Больше — точнее модель, но больше API-вызовов. |
| 04 | Модели для предобучения | Linear, RF, XGBoost (✓), LightGBM (✓), MLP, LSTM. |

Кнопка **«Начать настройку»** запускает в фоне три фазы:

1. **Сбор данных** — по каждому выбранному репозиторию (io-пул, 1 воркер).
2. **Извлечение фич** — только после полного завершения фазы 1.
3. **Обучение моделей** — параллельно (compute-пул, 3 воркера), только
   после завершения фазы 2.

Каждая фаза тикает в реальном времени на экране прогресса. Вкладку
можно закрыть — фон не прерывается. На странице видны три уровня:
плашка фазы → плашка bg_job → progress-бар.

> **Важно.** Раньше bootstrap-orchestrator складывал в очередь все
> bg_jobs одновременно — train_model падал с InsufficientDataError
> до окончания сбора. Сейчас orchestrator ждёт каждую фазу через
> опрос `bg_jobs.status`.

## Сценарий 1: добавить свой репозиторий

1. `/datasets → Add repository`.
2. Вставьте URL: `https://github.com/<owner>/<repo>` (поддерживаются
   также `git@github.com:owner/repo.git` и `owner/repo`).
3. Выберите окно истории (3/6/12 мес.) и опционально GitHub-токен.
4. Нажмите **«Add & start sync»**.

Карточка появится со статусом `idle` → быстро переключится в `fetching`
(сбор стартует автоматически — bg_job `collect_history` ставится при
успешном POST `/api/repos`). Когда сбор завершится — статус `synced`,
counters runs/jobs обновятся.

На карточках с любым статусом, кроме `fetching`, есть кнопка **Sync /
Start sync** — поставить в очередь свежий проход для обновления данных.

## Сценарий 2: real-time push → дашборд

1. `/admin → Webhooks` — система покажет URL, на который GitHub должен
   слать события: `https://<domain>/webhooks/github` (для локальной
   разработки используйте туннель типа `cloudflared tunnel`).
2. В настройках GitHub-репозитория создайте Webhook:
   `Settings → Webhooks → Add webhook` → Content-type `application/json`,
   Secret из `GITHUB_WEBHOOK_SECRET`, события `workflow_run`.
3. Сделайте `git push` в репозиторий.
4. На `/dashboard` карточка с прогнозом появится через 1–2 секунды.
   Поле «Live feed» KPI должно показывать `online`.
5. Когда GitHub Actions реально завершит job — карточка обновится
   фактическим временем и ошибкой прогноза. Без перезагрузки страницы.

## Сценарий 3: обучение модели

1. `/experiments` → выберите алгоритм (pill в форме «New training run»):
   Linear / Random Forest / XGBoost / LightGBM / MLP.
2. Отметьте **«Activate on finish»** если хотите сразу использовать
   модель в симуляторе.
3. **Не нужно** включать Optuna для первого захода — дефолтные параметры
   уже разумные.
4. Нажмите **«Train <algo>»**.
5. Toast: «Training queued». Под формой появится строка в «Recent
   training runs» — кликабельна, ведёт на `/experiments/jobs/<id>`.

На странице `/experiments/jobs/:id` видно:

- Статус-чип + progress bar.
- **Per-iteration metrics** — train loss и val RMSE по итерациям
  (XGBoost/LightGBM выдают полную кривую, Linear/RF/MLP — финальную
  точку).
- **Predicted vs actual** — log-log scatter на тестовой выборке.
   Идеальная модель — все точки на пунктирной диагонали `y = x`.
- **Top features** — горизонтальная гистограмма топ-20 фич по важности
  (Gini для деревьев, |coef| для Linear).

Обучение на ~500 job'ах занимает 0.3 секунды для Linear и 1–5 секунд
для XGBoost.

## Сценарий 4: Подбор гиперпараметров

1. `/experiments`, в форме «New training run» снизу есть блок
   **«Hyperparameter search (Optuna)»**.
2. Pill-выбор: `off / 10 / 30 / 50 / 100 trials`. По умолчанию off
   (используются дефолтные гиперпараметры).
3. Выберите 30 trials, **Activate on finish**, нажмите **Train xgboost**.
4. Optuna прогонит 30 итераций TPE-сэмплера на той же time-based
   train/test разбивке, что и обычный train. Поиск минимизирует MAE
   на тестовой выборке.
5. После завершения в `/api/models` появится модель с лучшими
   найденными гиперпараметрами. На странице training detail видна
   `train_curves` финального fit'а с этими параметрами.

На датасете в 500–1000 job'ов 30 итераций занимают ~10–20 секунд.

## Сценарий 5: сравнение стратегий

1. `/simulator`.
2. Выберите окно: «Last 7 days» / «Last 30 days» / «Last 90 days» / «All
   data».
3. Отметьте стратегии: FIFO / SJF / EDF / Custom.
4. **Runners** — число параллельных runner'ов (по умолчанию 2).
5. **SLA main** и **SLA feature** — дедлайны в секундах для main-ветки
   и feature-веток соответственно (используются EDF и Custom).
6. **«Run simulation»**.

Через 30–60 секунд (на 500 job'ах — < 1 секунды) появятся 4 гистограммы:
makespan, wait mean, wait p95, SLA violations. Под ними — таблица всех
метрик по каждой стратегии. Внизу — история «Recent runs»: каждый
запуск сохранён в `sim_runs`, можно сравнивать конфигурации между
собой.

Симулятор использует prediction'ы активной модели как `PredictedSec`.
Если активной модели нет — fallback'ом служит фактическое время
`actual_duration_sec` (oracle-режим), что даёт «потолок» SJF.

## Сценарий 6: экспорт пакета для диссертации

Кнопка **«Export thesis pack»** в правом верхнем углу `/experiments`.

Toast: «Thesis pack exported». В `/var/lib/cicdml/thesis/<timestamp>/`
появятся:

```
models.csv                — все обученные модели + метрики
dataset_stats.csv         — каждый репозиторий: runs/jobs/период покрытия
strategy_comparison.csv   — все симуляции
predicted_actual.csv      — пары (actual, predicted) активной модели
feature_importance.csv    — важность фич активной модели

figures/
├── model_comparison.{png,pdf}
├── strategy_comparison.{png,pdf}
├── predicted_vs_actual_model_<id>.{png,pdf}
├── feature_importance_model_<id>.{png,pdf}
└── training_curves_run_<id>.{png,pdf}
```

CSV LaTeX-дружелюбные: запятые, точка как десятичный разделитель,
без лишних кавычек — `pandas.read_csv` без аргументов.
PDF 300 DPI, тонкие линии, моно-шрифты — готовы под `\includegraphics{}`.

Путь — это монтированный Docker volume `thesis-output`. Достать файлы
с хоста:

```bash
# каждый файл по отдельности
docker compose cp api:/var/lib/cicdml/thesis/20260514-193804 ./thesis-pack/

# либо смотреть прямо в томе (Windows path к Docker Desktop volume)
\\wsl$\docker-desktop\mnt\docker-desktop-disk\data\docker\volumes\cicd-ml_thesis-output\_data\
```

## Диагностика

Всё видно в UI — лазить в логи обычно не нужно.

| Симптом | Где смотреть | Что делать |
|---|---|---|
| GitHub rate limit во время сбора | Карточка в `/datasets` показывает «Rate limited, retrying in 23 min» | Подождать или добавить токен в форме Add repository / Sync |
| Webhook не приходит | `/admin → Webhook deliveries` — таблица последних 50 событий с результатом HMAC | Пересоздать webhook с правильным `GITHUB_WEBHOOK_SECRET` |
| Модель плохо обобщает на новый репо | Подключите больше истории через `/datasets`, переобучите модель | Запустите Optuna 50 trials |
| Сервис не отвечает | Точка статуса в шапке → `/admin → System health` | Перезапустить контейнер из CLI: `docker compose restart <service>` |
| Long-running bg_job не двигается | `/admin` ничего не показывает напрямую, но `GET /api/bg-jobs?status=running` через REST даст полную картину | Подождать или отменить запись через DB (`UPDATE bg_jobs SET status='cancelled' WHERE id=…`) |

---

Альтернатива UI для разработчиков — прямые REST-вызовы. Список
актуальных эндпоинтов в [`docs/architecture.md`](architecture.md).
