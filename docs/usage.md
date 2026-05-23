# Руководство пользователя

Документ описывает все сценарии работы с системой. Все действия выполняются **в веб-интерфейсе** — CLI для штатной работы не нужен.

## Оглавление

1. [Переключение языка интерфейса](#переключение-языка)
2. [Первый запуск — экран `/setup`](#первый-запуск) (или snapshot auto-restore)
3. [Командная палитра Cmd+K](#командная-палитра-cmdk-и-shortcuts)
4. [Дашборд: live-очередь, KPI, 24h-нагрузка](#дашборд)
5. [Датасеты: heatmap, sync-прогресс, webhook auto-install](#датасеты)
6. [Добавление своего репозитория](#сценарий-1-добавить-свой-репозиторий)
7. [Real-time реакция на `git push`](#сценарий-2-real-time-push--дашборд)
8. [Обучение модели: быстрый запуск, Optuna, CV, wizard](#сценарий-3-обучение-модели)
9. [Сравнение моделей бок-о-бок](#сценарий-4-сравнение-моделей)
10. [Сравнение стратегий планирования](#сценарий-5-сравнение-стратегий)
11. [Экспорт CSV: датасет, симуляция, thesis pack](#сценарий-6-экспорт-данных)
12. [Admin: настройки, журнал, pause/resume воркеров](#админка)
13. [Диагностика](#диагностика)

---

## Переключение языка

В правом верхнем углу любого экрана видны две моно-кнопки **EN / RU**. Выбор сохраняется в `localStorage` и работает на всех страницах, включая онбординг `/setup`. Первый визит выбирает язык по `navigator.language`: всё, что начинается с `ru-` → русский, остальное → английский.

---

## Первый запуск

### Вариант A — со snapshot (1-2 минуты)

Если в `db/seed/snapshot.sql.gz` есть pre-baked дамп (его кладёт автор репозитория или CI), api-gateway на старте автоматически:

1. Проверяет `bootstrap_done` в `system_state`.
2. Если `false` и файл существует — gunzip + multi-statement Exec через pgx.
3. Ставит `bootstrap_done=true` в той же транзакции.

Лог показывает: `auto-restored snapshot — system is ready without /setup`. После этого открываете URL → попадаете сразу на `/dashboard` с предзаполненными данными.

### Вариант B — через /setup

Если БД пустая (нет snapshot или флаг сброшен), любой URL перенаправляется на `/setup`. Форма из четырёх блоков:

| # | Блок | Назначение |
|---|---|---|
| 01 | GitHub-токен | Необязателен. Без него лимит GitHub API — 60 запросов/час. |
| 02 | Стартовые репозитории | 7 публичных проектов с богатой историей CI. Снимите чекбоксы с лишних. |
| 03 | Окно истории | 3 / 6 / 12 месяцев. Больше — точнее модель, но больше API-вызовов. |
| 04 | Модели для предобучения | Linear, RF, XGBoost (✓), LightGBM (✓), MLP, LSTM. |

Кнопка **«Начать настройку»** запускает три фазы:

1. **Сбор данных** — по каждому выбранному репозиторию (collector-контейнер, 1 воркер).
2. **Извлечение фич** — после полного завершения фазы 1.
3. **Обучение моделей** — параллельно (compute-пул, 3 воркера), после фазы 2.

Каждая фаза тикает на экране прогресса. Вкладку можно закрыть — фон не прерывается; при возврате прогресс восстанавливается через `useActiveBootstrap`.

При первом старте webhook на seed-репо ставятся автоматически только если PAT даёт admin-доступ; для публичных upstream-репо (vitejs, prometheus и т.д.) их не поставить — пилюля webhook на карточке будет серой «Webhook: no admin access». Это нормально.

---

## Командная палитра (Cmd+K) и shortcuts

- **Cmd/Ctrl + K** — открывает палитру; начинайте печатать имя страницы или действия.
- **Alt + D** — Dashboard
- **Alt + S** — Datasets
- **Alt + E** — Experiments
- **Alt + I** — Simulator
- **Alt + A** — Admin

В палитре также прямые прыжки к якорям: `Admin → Settings`, `Admin → Activity log`, `Admin → Webhooks`, и т.д.

---

## Дашборд

`/dashboard` показывает четыре KPI и две секции:

**KPI-блок:**
1. **Active model** — алгоритм + MAE (если модель активирована).
2. **Strategy** — текущая активная стратегия планирования.
3. **Live feed** — статус /ws/queue подключения.
4. **Mean duration** — среднее время по последним 24 часам + sparkline-график почасовой нагрузки.

**Active queue** — карточки активных job'ов (in-flight + только что завершённые в течение 30 сек). На каждой:
- repo / workflow / branch / SHA
- predicted_sec (прогноз модели)
- elapsed (live-таймер для running) / actual (для completed)
- δ% — signed prediction error: зелёный ≤10%, жёлтый ≤30%, красный >30%
- progress-bar elapsed/predicted

**Live feed** — лента событий /ws/queue (последние 20). Полезно для диагностики webhook'ов.

---

## Датасеты

`/datasets` показывает:

1. **Heatmap покрытия** — репозиторий × день за последние 90 дней. Цвет ячейки = log-saturated число job'ов в этот день. Тёмные ячейки = пробелы в данных.
2. **Карточки репо** с:
   - status chip (idle/fetching/synced/error/paused);
   - **WebhookBadge** — статус автоустановки webhook (live/no-access/unreachable/etc) + кнопка Install/Remove;
   - кнопки Sync / Pause / Resume / Resync / Remove;
   - **SyncProgressStrip** — live прогресс-бар с ETA, jobs/sec, rate-counter и countdown до GitHub-reset.

Каждая карточка — ссылка на `/datasets/{id}` (per-repo детали).

### Per-repo details

`/datasets/{id}` показывает:
- статус + временное окно + tracked branches
- **Duration distribution** — log-binned гистограмма (8 buckets от <10s до 30m+)
- **Top workflows** — таблица с runs/p50/p95
- **Top jobs** — таблица с runs/mean/p50
- **Branches** — таблица run-counts/mean duration
- **Conclusions** — bar chart success/failure/cancelled
- **Feature matrix preview** — первые 50 строк фич с фильтром по job_name
- кнопка **Export CSV** — скачивает все job'ы репо как CSV для pandas/Excel

---

## Сценарий 1: добавить свой репозиторий

`/datasets` → **Add repository** → вставить URL `https://github.com/owner/repo` → выбрать period → **Add & start sync**.

Что произойдёт:
1. Репо добавится в `repos` со статусом `idle`.
2. Автоматически энкьюится `collect_history` bg_job — collector-контейнер тут же подхватит.
3. Параллельно фоном поставится webhook через GitHub API (если PAT даёт права).
4. На карточке появится `SyncProgressStrip` с живым прогрессом.

Через несколько минут (или часов для крупных репо) — счётчики runs/jobs обновятся, статус станет `synced`.

---

## Сценарий 2: Real-time push → дашборд

После того как webhook установлен (зелёная пилюля **Webhook live**), любой `git push` в подключённый репо запускает цепочку:

```
git push
  → GitHub workflow_run.requested
    → POST /webhooks/github → ml-service /predict/from-payload
      → broadcast /ws/queue с predicted_sec
        → на /dashboard появляется карточка с прогнозом (1-2 сек)

GitHub Actions выполняет job
  → workflow_run.in_progress → broadcast → таймер на карточке стартует
  → workflow_run.completed → api вычисляет actual_sec = updated_at - run_started_at,
      ищет прогноз в кэше, считает delta_pct
        → broadcast → карточка показывает predicted / actual / δ% с цветной подсветкой
```

Δ% зелёный ≤10%, жёлтый ≤30%, красный >30% — мгновенная оценка качества модели.

Если webhook не установлен — события не придут. На локалке используйте cloudflared tunnel (см. `docs/deployment.md`).

---

## Сценарий 3: обучение модели

`/experiments` предлагает **три варианта** запуска:

### A. Быстрый запуск (top-bar)

Пилюли алгоритмов (linear/rf/xgb/lgbm/mlp) + чекбокс «Activate on finish» + кнопка **Train xgboost**. Использует дефолтные гиперпараметры на полном датасете.

### B. Walk-forward CV (без сохранения модели)

Рядом блок **Walk-forward CV** с пилюлями 3/5/8 фолдов. Кнопка **Cross-validate** запускает синхронный CV через `/api/training/cv`. Результат — таблица per-fold + mean ± std для MAE/RMSE/MAPE/R²/Spearman/NDCG. Модель НЕ сохраняется — это оценка, а не обучение.

### C. Optuna search

Пилюли «10/30/50/100 trials». При >2 запускает Optuna TPE search → берёт лучшие параметры → обучает финальную модель. Метрики попадают в `models`.

### D. Wizard (полный 4-шаговый мастер)

Кнопка **New training (wizard)** в actions заголовка. Четыре шага:

1. **Algorithm** — пилюли всех 6 алгоритмов.
2. **Dataset** — чекбоксы репозиториев (можно сузить до одного для apples-to-apples модели) + slider 3/6/12 мес.
3. **Hyperparameters** — слайдеры со sensible-диапазонами для выбранного алгоритма (например для xgb: n_estimators / max_depth / learning_rate / subsample). Дефолты совпадают с `BaseModel._build_estimator`.
4. **Train/test split** — interactive cutoff timeline: bar-chart дневных run counts с кликабельной cutoff-линией. Слева amber = train, справа серый = test. Кнопка **Start training** + чекбокс **Activate on finish**.

После запуска фоновая задача `train_model` стримит per-iteration метрики. Кликните на ID модели в таблице — переход на `/experiments/jobs/{id}` с:
- live loss-curve / val RMSE / val MAE
- Cancel-кнопка (cooperative cancellation через ctx)
- Logs tail (последние 200 строк bg_job.logs_tail)
- по завершении: scatter predicted vs actual, **residuals plot** (linear-scale, центрирован вокруг нуля), feature importance

---

## Сценарий 4: сравнение моделей

В таблице `/experiments` поставьте чекбоксы у 2-5 моделей → кнопка **Compare selected (N)** в actions → `/experiments/compare?ids=1,2,3`.

Показывает:
- **Metrics table** — все метрики колонками с подсветкой лучшего значения per row.
- **Overlaid scatter** predicted vs actual — точки разных моделей разными цветами, диагональная reference.
- **Top features** — overlaid bar chart топ-10 фич с per-модель значениями.

Один скриншот этой страницы покрывает половину Главы 4 диссертации.

---

## Сценарий 5: сравнение стратегий

`/simulator` → выбрать **Window** (last 7/30/90 days / all) + **Strategies** (чекбоксы FIFO/SJF/EDF/Custom) + **Runners** + опционально SLA budget'ы → **Run simulation**.

Результаты:
- **Bar charts** по каждой метрике (makespan / wait mean / wait p95 / SLA violations) — стратегии сравниваются бок-о-бок.
- **Recent runs** таблица всех прошлых симуляций; рядом с каждой строкой ссылка **Export CSV** для вставки в pgfplotstable.

Веса Custom-стратегии задаются на `/admin → Settings`.

---

## Сценарий 6: экспорт данных

| Что | Где | Формат |
|---|---|---|
| Полный thesis pack (5 CSV + matplotlib фигуры) | `/experiments → Export thesis pack` | Файлы в `docs/thesis/` (mounted volume) |
| Один датасет (все job'ы репо) | `/datasets/{id} → Export CSV` | CSV: job_id, name, duration, runner, steps, status, workflow, branch, SHA, commit diff stats |
| Один sim_run (одна стратегия) | `/simulator → таблица → Export CSV` | CSV: strategy, window, jobs_count, makespan, wait_p50/p95, throughput, SLA violations |
| EDA фигуры (5 PNG для Главы 3) | `make eda-figures` или Jupyter `ml/notebooks/01_eda.ipynb` | PNG в `docs/thesis/figures/` |

Для воспроизводимости диссертации:

```bash
make snapshot               # сохранили текущее состояние БД
rm -rf docs/thesis/figures
make eda-figures            # сгенерировали фигуры
make thesis-pack            # выгрузили метрики + matplotlib фигуры
```

---

## Админка

`/admin` объединяет:

### Settings

- **Active strategy** — dropdown FIFO/SJF/EDF/Custom. Применяется к симулятору и сохраняется в `system_state`.
- **Custom strategy weights** — три ползунка (short_job / deadline_proximity / branch_importance) для weighted-score формулы Custom.
- **GitHub token** — обновить персистентный PAT, который collector/webhook installer используют когда per-call токен пуст.

### Activity log

Последние 50 пользовательских действий (add repo, train model, activate, delete, pause workers, install webhook, и т.д.). Колонки: время / actor / action / target / result chip.

### System health

Per-component статус (postgres / api-gateway / ml-service / redis / bg-jobs runner) с pingом каждые 10 сек. Цветной overall-чип в шапке.

**Pause workers / Resume workers** — справа в строке статуса. Останавливает claim-цикл bg-jobs воркеров (in-flight job'ы продолжают доходить до конца). Полезно когда хочется остановить долгую тренировку / сбор без рестарта контейнеров. При паузе появляется жёлтый chip `paused` в строке `bg-jobs runner`.

### Webhooks

Журнал последних 50 GitHub-deliveries с HMAC-результатом. Полезно для диагностики «почему дашборд не моргнул».

---

## Диагностика

### «Дашборд показывает webhook'и, но без прогноза»

- Нет активной модели. Зайдите на `/experiments`, обучите хотя бы одну модель и нажмите **Activate**.
- ml-service недоступен — проверьте `/admin → System health`.

### «Webhook на моём репо серый»

Откройте tooltip пилюли — он расскажет почему:
- **no admin access** — у PAT нет admin:repo_hook scope или вы не owner/maintainer. Это нормально для upstream OSS-репо.
- **public URL not set** — `PUBLIC_API_BASE` указывает на localhost. Нужен туннель (см. `docs/deployment.md`) или прод-домен.
- **install failed** — сетевая ошибка / неожиданный ответ. Жмите Install webhook вручную чтобы retry.

### «Сбор данных висит на `fetching`»

`/admin → Webhooks` показывает GitHub rate-limit. Если remaining=0, ждите до reset. Дополнительный PAT в `/admin → Settings` поднимает лимит с 60 до 5000 req/h.

### «bg_jobs ничего не делает»

`/admin → System health` → если строка `bg-jobs runner` показывает chip `paused` — нажмите **Resume workers** справа от строки overall-статуса.

Если воркер не paused, но новые job'ы стоят queued — проверьте логи `collector` и `simulator` контейнеров. Если кто-то из них упал, gateway не подберёт их job'ы автоматически (kind-filter через `ENABLED_BG_KINDS`). Запустите их обратно либо временно уберите `ENABLED_BG_KINDS` у api в compose.

### «Модель плохо предсказывает на новом репо»

На странице модели смотрите feature importance — если высокие веса у `repo=X` категориальной фичи, модель «привязана» к старым репо и не обобщается. Решение: переобучить модель с включением новых репо в датасет (через wizard, шаг Dataset).

### «Symbol-чип состояния странный»

Все цвета chip'ов: `idle/queued/cancelled` — нейтральный серый, `fetching/running` — синий, `done/synced` — зелёный, `failed/error` — красный, `paused` — жёлтый/оранжевый. Если что-то неожиданное — проверьте в БД `bg_jobs.status` / `repos.status` напрямую через `make psql`.
