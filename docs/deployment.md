# Деплой `cicd-ml`

Два сценария: **прод на VPS с публичным доменом** и **локальная разработка с туннелем для webhook'ов**. Оба используют один `docker-compose.yml`; различаются override-файлами и env'ом.

---

## 1. Деплой на VPS (prod)

### Требования к VPS

- Ubuntu 22.04+ / Debian 12+.
- 4 vCPU, 8 ГБ RAM, 40 ГБ SSD (минимум; для больших датасетов нужно больше).
- Открытые TCP-порты **80** и **443**.
- Доменное имя с DNS-записью `A` (или `AAAA`), указывающей на ваш VPS.
- E-mail для уведомлений Let's Encrypt.

### Шаг 1 — установка Docker

```bash
ssh root@<vps-ip>
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

### Шаг 2 — клонирование репозитория и конфигурация

```bash
git clone <repo-url> /opt/cicd-ml
cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod
chmod 600 .env.prod
```

Минимум что задать:

```env
DOMAIN=cicd-ml.example.com
LE_EMAIL=you@example.com
POSTGRES_PASSWORD=<сильный пароль>
JWT_SECRET=<сильный секрет>
GITHUB_WEBHOOK_SECRET=<сильный секрет, обязателен для HMAC>
PUBLIC_API_BASE=https://cicd-ml.example.com
PUBLIC_WS_BASE=wss://cicd-ml.example.com
```

### Шаг 3 — настройка DNS

```
Type:  A
Host:  cicd-ml     (или @ для apex)
Value: <vps-ip>
TTL:   3600
```

Проверка: `dig cicd-ml.example.com +short` → IP VPS.

### Шаг 4 — запуск стека

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
docker compose logs -f traefik
```

В течение 30–90 секунд Traefik получит сертификат Let's Encrypt. Откройте `https://cicd-ml.example.com` — должен появиться экран `/setup` (или сразу `/dashboard`, если применён snapshot, см. ниже).

### Архитектура контейнеров в проде

```
                       ┌────────────┐
                       │  Traefik   │── 80/443 ── публичный домен
                       └─────┬──────┘
                             │ HTTPS + Let's Encrypt
            ┌────────────────┼─────────────────┐
            │                │                 │
       ┌────▼────┐      ┌────▼─────┐     ┌─────▼─────┐
       │   api   │      │ frontend │     │ /webhooks │
       │ :8080   │      │  :80     │     │  /github  │
       └────┬────┘      └──────────┘     └───────────┘
            │ docker-сеть, недоступна извне
   ┌────────┼─────────────┬─────────────┐
   │        │             │             │
┌──▼──┐  ┌──▼──┐    ┌─────▼─────┐ ┌─────▼─────┐
│ ml  │  │ db  │    │ collector │ │ simulator │
└─────┘  └─────┘    └───────────┘ └───────────┘
         Postgres   bg_jobs:        bg_jobs:
                    collect_history simulate
                    + refresh
```

- **api-gateway** — HTTP/WS + bootstrap orchestrator + bg-jobs runner для `bootstrap`/`compute_features`/`train_model`.
- **collector** — отдельный воркер для `collect_history`/`refresh` (длинные GitHub-пуллы не блокируют API).
- **simulator** — отдельный воркер для `simulate` (CPU-burst).
- **ml** — Python FastAPI для тренировок/предсказаний.
- **db** — Postgres 16; **redis** — Redis 7 (зарезервирован под очередь, сейчас в health-check).
- **frontend** — собранный React-bundle через nginx.

Воркеры пушат WS-broadcast обратно в gateway через `POST /api/internal/broadcast` — этот путь закрыт от внешнего доступа Traefik-правилами.

### Шаг 5 — webhook URL для GitHub

После запуска UI показывает webhook URL в `/admin → Webhooks` и автоматически устанавливает webhook через GitHub API при добавлении репо на `/datasets` (если PAT даёт права admin:repo_hook).

Если хочется вручную:

- **Payload URL:** `https://cicd-ml.example.com/webhooks/github`
- **Content type:** `application/json`
- **Secret:** значение `GITHUB_WEBHOOK_SECRET` из `.env.prod`
- **Events:** `Workflow runs` (минимум) + `Pushes` (для будущих commit features)

### Snapshot для быстрого старта

Один раз снимаем дамп на работающей системе:

```bash
make snapshot                              # → db/seed/snapshot.sql.gz
```

Файл коммитится в репозиторий (или передаётся отдельно). На свежем VPS:

```bash
git clone <repo-url> /opt/cicd-ml
cd /opt/cicd-ml
cp .env.prod.example .env.prod && nano .env.prod
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
# 1-2 минуты — api-gateway зальёт snapshot и установит bootstrap_done=true.
# Открыть https://... — всё уже работает.
```

Без snapshot — попадаем на `/setup`, надо вручную запускать сбор данных (часы для нескольких репо).

### Бэкапы

```cron
0 3 * * * cd /opt/cicd-ml && docker compose exec -T db \
    pg_dump --inserts --no-owner --no-acl -U cicdml cicdml \
  | gzip > /backup/cicdml-$(date +\%Y\%m\%d).sql.gz

30 3 * * 0 find /backup -name 'cicdml-*.sql.gz' -mtime +7 -delete
```

Восстановление: `gunzip -c backup.sql.gz | docker compose exec -T db psql -U cicdml -d cicdml`.

### Обновления

```bash
cd /opt/cicd-ml
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod build
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Миграции БД накатываются автоматически на старте api (встроены через `go:embed`).

### Проверка безопасности

- [ ] `.env.prod` — `chmod 600`, не закоммичен.
- [ ] `JWT_SECRET` и `GITHUB_WEBHOOK_SECRET` — длинные случайные строки.
- [ ] Traefik принудительно редиректит на HTTPS (настроено в `docker-compose.prod.yml`).
- [ ] `fail2ban` для SSH.
- [ ] Postgres/Redis **не выставлены** в публичную сеть (`ss -tlnp` показывает только Docker bridge).
- [ ] `/api/internal/*` не проходит через Traefik (по умолчанию правила пропускают только `/api`, `/ws`, `/webhooks`, `/` — `/api/internal/broadcast` доступен только collector/simulator из docker-сети).
- [ ] Бэкапы хотя бы раз восстанавливались в тестовое окружение.

---

## 2. Локальная разработка + туннель для webhook'ов

Локально (без публичного домена) GitHub не может дотянуться до `http://localhost:8080`. Чтобы тестировать цепочку `git push → webhook → dashboard`, нужен туннель.

### Вариант A — Cloudflare Tunnel (рекомендуется, бесплатно, без регистрации)

[Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) пробрасывает random-URL `https://<random>.trycloudflare.com` на ваш localhost:8080.

**Установка:**

```bash
# macOS
brew install cloudflared

# Ubuntu / Debian
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cf.deb
sudo dpkg -i /tmp/cf.deb

# Windows
winget install --id Cloudflare.cloudflared
```

**Запуск (в отдельном терминале):**

```bash
cloudflared tunnel --url http://localhost:8080
```

В выводе появится:

```
Your quick Tunnel has been created! Visit it at:
https://abcd-1234-efgh-5678.trycloudflare.com
```

Скопируйте URL. Дальше:

1. В `.env` поставьте:

   ```env
   PUBLIC_API_BASE=https://abcd-1234-efgh-5678.trycloudflare.com
   PUBLIC_WS_BASE=wss://abcd-1234-efgh-5678.trycloudflare.com
   ```

   и перезапустите: `docker compose restart api`.

2. На `/datasets` добавьте репозиторий, где у вас admin-доступ. Webhook установится автоматически — пилюля рядом с карточкой станет зелёной **Webhook live**.

3. Сделайте `git push` в этот репо → GitHub шлёт `workflow_run` → cloudflared → api → ml-service → broadcast в `/ws/queue` → видим карточку на `/dashboard` за 1-2 сек.

**Минусы Quick Tunnel:** URL меняется при каждом запуске cloudflared. Для стабильного URL — зарегистрируйте named tunnel (тоже бесплатно): https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/

### Вариант B — ngrok

```bash
brew install ngrok        # или https://ngrok.com/download
ngrok http 8080
```

URL `https://1234-5678.ngrok-free.app`. Минус — баннер-предупреждение при первом обращении в браузере (GitHub его игнорирует).

### Вариант C — Tailscale Funnel

Если у вас уже Tailscale: `tailscale funnel 8080` → стабильный URL без регистрации.

### Проверка туннеля

```bash
curl https://your-tunnel-url/healthz
# {"status":"ok",...}
```

404 — туннель смотрит не на тот порт. Timeout — туннель не запущен / firewall.

### Что важно при тестировании webhook'ов

- GitHub шлёт retries при 5xx — событие придёт повторно через минуты, если api временно лежал.
- `/admin → Webhooks` показывает последние 50 доставок с HMAC-результатом — удобно дебажить «почему дашборд не моргнул».
- При смене `GITHUB_WEBHOOK_SECRET` ОБЯЗАТЕЛЬНО обновите его и в GitHub (Settings → Webhooks → Edit), иначе HMAC не сойдётся → 401.

---

## 3. Диагностика проблем

### «Контейнер не стартует»

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod logs <service> --tail=200
```

Типичное:
- **`api` упал из-за миграции** — посмотреть `db` логи + проверить `.env` (DSN).
- **`ml` не запускается** — PyTorch wheel не докачался. `docker compose build ml --no-cache`.
- **`traefik` не даёт сертификат** — DNS не настроен или 80/443 закрыты firewall'ом VPS.

### «bg_jobs ничего не делает»

Откройте `/admin → System health`. Если строка `bg-jobs runner` показывает `paused` — кто-то нажал «Pause workers». Кнопка `Resume workers` справа возвращает.

Также проверьте логи `collector` / `simulator` — они забирают свои kinds. Если контейнер упал, gateway не подхватит их job'ы автоматически (ENABLED_BG_KINDS ограничивает).

**Срочное переключение в single-binary mode** (gateway берёт все kinds):

```bash
docker compose stop collector simulator
# В docker-compose.yml уберите ENABLED_BG_KINDS у сервиса api (или поставьте пустое)
docker compose restart api
```

### «GitHub перестал слать webhook'и»

`/admin → Webhooks` покажет когда был последний. Если ничего за час:
1. Проверьте, что URL правильный (`PUBLIC_API_BASE` совпадает с тем, что в репо).
2. GitHub → Settings → Webhooks → ваш webhook → Recent Deliveries показывает попытки и ответы api.
3. HMAC-mismatch → секрет разошёлся, обновите в одном из мест.

### «OOM во время тренировки»

Поставьте лимиты в `docker-compose.prod.yml`:

```yaml
ml:
  mem_limit: 4g
```

или перейдите на VPS с большим RAM. LSTM тренировка особенно прожорлива.

### «`/setup` показывается заново после обновления»

Volume `pg-data` пересоздался — БД пуста. Если есть бэкап → восстановите. Если флаг `bootstrap_done` потерян, но данные на месте:

```bash
make psql
# в psql:
UPDATE system_state SET value='true' WHERE key='bootstrap_done';
```

---

## 4. Откат и снос

### Полный wipe (волюмы тоже)

```bash
docker compose down -v
# /var/lib/docker/volumes/cicd-ml_* удалены — БД, модели, всё пропало
```

### Откат на предыдущую версию

```bash
git log --oneline | head -10
git checkout <commit-sha>
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod build
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Если новая миграция добавила колонку — старый api отработает с расширенной схемой нормально. Если миграция удаляла колонку — старый api упадёт; нужен restore из бэкапа.

---

## 5. Куда подключаться внутри контейнеров

Все сервисы доступны друг другу по DNS-именам (compose-сеть `cicdml-net`):

| Сервис | Порт | Назначение |
|---|---|---|
| `api` | 8080 | Go api-gateway. Снаружи через Traefik: `/api/*`, `/ws/*`, `/webhooks/*`, `/healthz`. `/api/internal/*` — только во внутренней сети. |
| `ml` | 8000 | Python ml-service. Снаружи **не доступен**, вызывается только из api по `http://ml:8000`. |
| `frontend` | 80 (prod) / 5173 (dev) | Статика через nginx (prod) или vite dev-server. Снаружи через Traefik на `/`. |
| `db` | 5432 | Postgres, только внутренний. |
| `redis` | 6379 | Redis, только внутренний. |
| `collector` | — | Не слушает порты; пушит broadcast в `http://api:8080/api/internal/broadcast`. |
| `simulator` | — | Аналогично collector. |
