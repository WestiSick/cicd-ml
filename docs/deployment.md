# Деплой на VPS

Как запустить систему на боевом сервере с доменом и автоматическим
HTTPS через Let's Encrypt.

## Требования к VPS

- Ubuntu 22.04+ / Debian 12+.
- 4 vCPU, 8 ГБ RAM, 40 ГБ SSD (минимум; для больших датасетов нужно
  больше).
- Открытые TCP-порты **80** и **443**.
- Доменное имя с DNS-записью `A` (или `AAAA`), указывающей на ваш VPS.
- E-mail для уведомлений Let's Encrypt.

## Шаг 1 — установка Docker

```bash
ssh root@<vps-ip>
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

## Шаг 2 — клонирование репозитория и конфигурация

```bash
git clone <repo-url> /opt/cicd-ml
cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod
```

Заполните:

```env
DOMAIN=cicd-ml.example.com
LE_EMAIL=you@example.com
POSTGRES_PASSWORD=<сильный пароль>
JWT_SECRET=<сильный секрет>
GITHUB_WEBHOOK_SECRET=<сильный секрет>
```

Защитите файл:

```bash
chmod 600 .env.prod
```

## Шаг 3 — настройка DNS

В панели вашего DNS-провайдера:

```
Type:  A
Host:  cicd-ml     (или @ для apex)
Value: <vps-ip>
TTL:   3600
```

Проверка:

```bash
dig cicd-ml.example.com +short
```

## Шаг 4 — запуск стека

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
docker compose logs -f traefik
```

В течение 30–90 секунд Traefik получит сертификат Let's Encrypt.
Откройте `https://cicd-ml.example.com` — должен сразу появиться экран
онбординга `/setup`.

## Шаг 5 — webhook URL для GitHub

В `/admin → Webhooks` система автоматически показывает публичный URL:
`https://cicd-ml.example.com/webhooks/github`. Этот URL — то, что
вставляется в настройки GitHub-репозитория:

```
Settings → Webhooks → Add webhook
  Payload URL:    https://cicd-ml.example.com/webhooks/github
  Content type:   application/json
  Secret:         <значение GITHUB_WEBHOOK_SECRET из .env.prod>
  Events:         workflow_run (Let me select individual events)
```

## Бэкапы

Cron на хосте для ежедневного `pg_dump`:

```cron
0 3 * * * cd /opt/cicd-ml && docker compose exec -T db pg_dump -U cicdml cicdml | gzip > /backup/cicdml-$(date +\%Y\%m\%d).sql.gz
```

Ротация — раз в неделю удалять старые:

```cron
30 3 * * 0 find /backup -name 'cicdml-*.sql.gz' -mtime +7 -delete
```

Восстановление: `gunzip -c backup.sql.gz | docker compose exec -T db psql -U cicdml -d cicdml`.

## Обновления

```bash
cd /opt/cicd-ml
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod build
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Миграции БД накатываются автоматически на старте контейнера `api`
(встроены в бинарь через `go:embed`).

## Проверка безопасности

- [ ] `.env.prod` имеет `chmod 600` и не закоммичен.
- [ ] `JWT_SECRET` и `GITHUB_WEBHOOK_SECRET` — длинные случайные строки.
- [ ] Traefik принудительно редиректит на HTTPS (уже настроено в
      `docker-compose.prod.yml`).
- [ ] `fail2ban` установлен и активен для SSH.
- [ ] Postgres/Redis **не выставлены** в публичную сеть (проверка:
      `ss -tlnp` должна показать их только на Docker bridge).
- [ ] Ежедневные бэкапы проверены — хотя бы раз восстановите дамп
      в тестовое окружение.

## Куда подключаться внутри контейнеров

В compose все сервисы доступны друг другу по DNS-именам:

- `api` (порт 8080) — Go api-gateway. Снаружи через Traefik: `/api/*`,
  `/ws/*`, `/webhooks/*`, `/healthz`.
- `ml` (порт 8000) — Python ml-service. Снаружи **не доступен**, вызывается
  только из api по `http://ml:8000`.
- `frontend` (порт 80 в проде) — статика через nginx. Снаружи через
  Traefik на `/`.
- `db` (порт 5432) — Postgres, только внутренний.
- `redis` (порт 6379) — Redis, только внутренний.

## Диагностика

- **Сертификат не выпущен.** `docker compose logs traefik`. Частые
  причины: DNS ещё не распространился, провайдер блокирует порт 80,
  `LE_EMAIL` невалидный.
- **Webhook не проходит HMAC.** Убедитесь, что `GITHUB_WEBHOOK_SECRET`
  в `.env.prod` совпадает с тем, что введён на странице webhook'а
  в GitHub.
- **OOM во время обучения модели.** Поставьте лимиты памяти в
  `docker-compose.prod.yml` (`mem_limit: 4g` для `ml`) и/или
  перейдите на VPS с большим объёмом RAM.
- **`/setup` показывается заново после обновления.** Проверьте, что
  PostgreSQL volume `pg-data` не пересоздался. Если `bootstrap_done`
  потерян — `UPDATE system_state SET value='true' WHERE key='bootstrap_done';`
  через `make psql`.
