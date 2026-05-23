# cicd-ml

Веб-приложение для **прогнозирования времени выполнения и планирования
очередей CI/CD на основе ML**. Практический артефакт магистерской
диссертации.

Что внутри:

1. **Прогнозирование длительности job'ов CI/CD** по историческим данным
   GitHub Actions — реализованы 5 алгоритмов: Linear (Ridge),
   Random Forest, XGBoost, LightGBM, MLP. Дополнительно — подбор
   гиперпараметров через Optuna.
2. **Симулятор очередей** со стратегиями **FIFO / SJF / EDF / Custom**.
   На одном и том же потоке исторических job'ов сравниваются makespan,
   wait p50/p95, throughput, SLA-нарушения.
3. **Реальная реакция на `git push`** через GitHub webhook → дашборд
   видит коммит за 1–2 секунды без перезагрузки.
4. **Полностью UI-driven**: добавление репозиториев, запуск сбора
   данных, обучение моделей, поиск гиперпараметров, симуляции,
   экспорт пакета для диссертации — всё в браузере.
5. **Готовый Docker Compose** для локальной разработки и боевого
   деплоя на VPS с доменом и автоматическим SSL (Traefik + Let's Encrypt).
6. **i18n**: интерфейс на русском и английском, переключатель в
   правом верхнем углу.

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

Откройте **http://localhost:5173** и пройдите онбординг.
Система соберёт историю и предобучит все выбранные модели в фоне;
вкладку можно закрыть, прогресс возобновится при возврате.

> **Совет.** В форме онбординга есть поле «GitHub Token» — без него
> лимит GitHub API 60 запросов/час (сбор займёт часы),
> с токеном `public_repo` — 5000 запросов/час (минуты). Создать:
> [github.com/settings/tokens](https://github.com/settings/tokens).

## Деплой на боевой сервер

```bash
# на VPS
git clone <repo-url> /opt/cicd-ml && cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod         # задать DOMAIN, LE_EMAIL и пароли
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Поставить DNS-запись `A` на ваш IP, через 30–90 секунд Traefik получит
сертификат Let's Encrypt — откройте `https://<ваш-домен>`.

Подробный гайд: [`docs/deployment.md`](docs/deployment.md).

## Документация

| Файл | О чём |
|---|---|
| [`docs/usage.md`](docs/usage.md)       | Пошаговые сценарии работы в UI |
| [`docs/deployment.md`](docs/deployment.md) | Деплой на VPS, бэкапы, обновления, безопасность |
| [`docs/architecture.md`](docs/architecture.md) | Архитектура, поток данных, дизайн-система |

## Структура репозитория

```
cicd-ml/
├── docker-compose.yml           # базовый стек
├── docker-compose.dev.yml       # dev-override (hot-reload, открытые порты)
├── docker-compose.prod.yml      # prod-override (Traefik + Let's Encrypt)
├── services/
│   ├── api-gateway/   (Go)      # REST + WebSocket + webhook + scheduler + bg-jobs
│   ├── collector/     (Go)      # отдельный воркер сбора (заготовка; collect живёт в api)
│   ├── simulator/     (Go)      # отдельный CLI симулятор (заготовка; sim живёт в api)
│   └── ml-service/    (Python)  # FastAPI: /train, /predict, /models, /features, /export
├── frontend/          (React + TS + Vite)
├── db/migrations/                # goose-миграции, встроены в api-gateway
├── ml/                           # ноутбуки + локальный registry артефактов
└── docs/                         # документация + thesis-артефакты
```

## Стек технологий

- **Backend (Go 1.23):** chi, pgx, go-redis, gorilla/websocket, zerolog
- **ML-сервис (Python 3.12):** FastAPI, scikit-learn, xgboost-cpu,
  lightgbm, optuna, matplotlib
- **Хранилище:** PostgreSQL 16, Redis 7
- **Frontend:** React 18 + TypeScript + Vite, Radix UI, visx, sonner,
  TanStack Query, собственный i18n (en/ru)
- **Деплой:** Docker Compose, Traefik v3, Let's Encrypt

## Реализованные модели и стратегии

**Модели (`/experiments`)**: Linear (Ridge), Random Forest, XGBoost,
LightGBM, MLP. Каждая отдаёт MAE/RMSE/MAPE/R² + ранг по Спирмену
(критично для SJF). Optuna search доступен на 10 / 30 / 50 / 100
итераций с TPE-сэмплером.

**Стратегии (`/simulator`)**: FIFO, SJF, EDF (с per-branch SLA),
Custom (взвешенный score). Симулятор event-driven, на 500+ job'ах
работает < 1 секунды.

**Экспорт для диссертации**: одна кнопка на `/experiments` →
`/var/lib/cicdml/thesis/<timestamp>/` появляются 5 CSV и 10 файлов
графиков (PNG + PDF, готовые для `\includegraphics{}`):
- `model_comparison.{png,pdf}` — сравнение MAE/RMSE/R²/Spearman
- `strategy_comparison.{png,pdf}` — сравнение FIFO/SJF/EDF/Custom
- `predicted_vs_actual_model_*.{png,pdf}` — scatter активной модели
- `feature_importance_model_*.{png,pdf}` — top-20 фич
- `training_curves_run_*.{png,pdf}` — кривые loss/val_RMSE

## Лицензия

Академическое использование в рамках магистерской диссертации.
Переиспользование с указанием авторства разрешено.
