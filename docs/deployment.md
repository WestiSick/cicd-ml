# Deployment guide

How to run the system on a VPS with a domain and automatic HTTPS.

## Requirements

- A VPS (Ubuntu 22.04+ or Debian 12+ recommended).
- 4 vCPU, 8 GB RAM, 40 GB SSD (minimum; larger datasets need more disk).
- Open TCP ports **80** and **443**.
- A domain name with an `A` (or `AAAA`) record pointing to your VPS.
- An email address for Let's Encrypt notifications.

## Step 1 — install Docker on the VPS

```bash
ssh root@<vps-ip>
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
```

## Step 2 — clone the repository and configure

```bash
git clone <repo-url> /opt/cicd-ml
cd /opt/cicd-ml
cp .env.prod.example .env.prod
nano .env.prod
```

Fill in:

```env
DOMAIN=cicd-ml.example.com
LE_EMAIL=you@example.com
POSTGRES_PASSWORD=<generate a strong one>
JWT_SECRET=<generate a strong one>
GITHUB_WEBHOOK_SECRET=<generate a strong one>
```

Lock the file down:

```bash
chmod 600 .env.prod
```

## Step 3 — configure DNS

In your DNS provider's panel, create:

```
Type:  A
Host:  cicd-ml     (or @ for the apex)
Value: <vps-ip>
TTL:   3600
```

Verify with:

```bash
dig cicd-ml.example.com +short
```

## Step 4 — launch the stack

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
docker compose logs -f traefik
```

Traefik will obtain a Let's Encrypt certificate within 30–90 seconds. Then
open `https://cicd-ml.example.com`.

## Step 5 — webhook URL for GitHub

The UI (`/admin → Webhooks`) automatically displays the public webhook URL:
`https://cicd-ml.example.com/webhooks/github`. When you toggle **Live
webhook** on a repository, the system uses this URL when registering the
webhook in GitHub.

## Backups

Set up a daily Postgres dump on the host:

```cron
0 3 * * * cd /opt/cicd-ml && docker compose exec -T db pg_dump -U cicdml cicdml | gzip > /backup/cicdml-$(date +\%Y\%m\%d).sql.gz
```

Rotate to keep ~7 copies (a simple `find -mtime +7 -delete` is enough for
this scale).

## Updates

```bash
cd /opt/cicd-ml
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod build
docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.prod up -d
```

Database migrations run automatically on `api` startup.

## Security checklist

- [ ] `.env.prod` is `chmod 600` and not committed.
- [ ] `JWT_SECRET` and `GITHUB_WEBHOOK_SECRET` are strong, random values.
- [ ] Traefik forces HTTPS (already configured in `docker-compose.prod.yml`).
- [ ] `fail2ban` is installed on the VPS for SSH.
- [ ] Daily DB backups are tested by performing a restore at least once.
- [ ] Postgres/Redis are **not** exposed to the public network (verify with
      `ss -tlnp` — they should only listen on the Docker bridge).

## Troubleshooting

- **Certificate not issued.** Check `docker compose logs traefik`. Common
  reasons: DNS not yet propagated, port 80 blocked by the provider firewall,
  or `LE_EMAIL` malformed.
- **Webhook fails HMAC verification.** Ensure `GITHUB_WEBHOOK_SECRET` matches
  the secret you set on the GitHub webhook itself.
- **OOM during model training.** Set explicit memory limits in
  `docker-compose.prod.yml` under the `ml` service and switch to a larger
  VPS tier if hitting them.
