# Deploying Piyo on a VPS

Written 2026-10-28. The stack is exactly what runs locally with `deploy/docker-compose.yml` (PostgreSQL/PostGIS, Go API, Martin tile server, Caddy); production adds `docker-compose.prod.yml` (HTTPS, no published database port). Prices and provider comparison are in HOSTING_PRICES.md (read 2026-10-28; verify before buying). Nothing here was executed on a real server; the local simulation in EVIDENCE.md is the test.

## 1. Sizing and cost

Traffic model (editable assumptions): per active user per month ≈ 20 map sessions × 30 tiles × 40 KB ≈ 24 MB, plus one 134 KB dataset download and ~10 API calls → ≈ 26 GB/month at 1k MAU, 260 GB at 10k, 2.6 TB at 100k. CPU is never the limit: Martin serves tiles from the page cache, the API does ~1 ms of work per request, PostgreSQL holds a few MB.

| Setup | 1k MAU | 10k MAU | 100k MAU | Notes |
|---|---|---|---|---|
| **A. One VPS** (2 vCPU / 4 GB, Docker Compose) | Hetzner CX23 €5.99 · Contabo €5.50 · OVH $4.54 · DigitalOcean $24 | same (all within included traffic) | same for Hetzner (20 TB), Contabo, OVH; DO $24 + overage ≈ $0 (4 TB pooled) | Backups add ~20 % |
| **B. VPS + Cloudflare R2 for tiles and dataset** | VPS as above + R2 ≈ $0 (free 10 GB storage, zero egress) | + ≈ $0–2 (Class B ops) | + ≈ $0–18 worst case (60 M tile reads without cache hits) | Best latency for Bolivia (Cloudflare has São Paulo/Lima/Santiago PoPs); keeps the VPS small |
| **C. Managed cloud** | Oracle Always Free $0 · Fly.io ≈ $28 · Railway ≈ $81 | Oracle $0 · Fly ≈ $37 · Railway ≈ $93 | Oracle $0 (10 TB egress) · Fly ≈ $131 · Railway ≈ $210 | Oracle free tier can be reclaimed; Fly/Railway bill egress |

Recommendation (the decision is yours): **B** — the cheapest 2 vCPU/4 GB VPS you trust (Hetzner if EU latency is acceptable, Contabo/OVH US-East otherwise; none has a Bolivian PoP, Miami/São Paulo give ~50–120 ms) plus Cloudflare R2 with a custom domain for `santa-cruz.pmtiles`, the dataset files and, via a Cloudflare Worker or a second Martin behind Cloudflare, the tiles. Start with **A** (one VPS, everything in Compose, ≈ €6/month) and add R2 when tile egress shows up in the bill. Oracle Always Free is the $0 option if you accept the risk of instance reclaim and set up the backup/restore below from day one.

Sizing per scale: 1k–10k MAU → 2 vCPU / 4 GB / 40 GB SSD; 100k MAU → 4 vCPU / 8 GB or setup B.

## 2. OS setup (Ubuntu 24.04 LTS)

```sh
# as root on a fresh VPS
adduser piyo && usermod -aG sudo piyo
rsync --archive --chown=piyo:piyo ~/.ssh /home/piyo     # copy your key
# as piyo
sudo apt update && sudo apt -y upgrade
sudo apt -y install ca-certificates curl git ufw unattended-upgrades fail2ban
curl -fsSL https://get.docker.com | sudo sh && sudo usermod -aG docker $USER && newgrp docker
sudo timedatectl set-timezone America/La_Paz      # logs in local time; the API stores UTC
sudo dpkg-reconfigure -plow unattended-upgrades   # security updates automatically
```

SSH hardening: `/etc/ssh/sshd_config` → `PasswordAuthentication no`, `PermitRootLogin no`, then `sudo systemctl restart ssh`. Keep one terminal open while testing.

## 3. Firewall

```sh
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw allow 443/udp   # HTTP/3
sudo ufw enable && sudo ufw status
```

Docker publishes only ports 80/443 (Caddy) in prod; the database port is not published (`docker-compose.prod.yml` sets `ports: []`). Note that Docker bypasses ufw for published ports, so never publish 5432 or 8080 on a public server.

## 4. Deploy with Docker Compose

```sh
git clone <your repo> ~/piyo && cd ~/piyo/deploy
cp .env.example .env && nano .env     # see §6
bash tiles/build-tiles.sh             # ~1 min: PMTiles + fonts + sprites + styles (needs node: sudo apt -y install nodejs npm)
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
docker compose ps
curl -s https://$DOMAIN/v1/health
```

Then publish the first dataset (§9). Until then the API serves the embedded 3-line sample and `/v1/health` says `"dataset_source":"embedded"`.

## 5. Domain and HTTPS

1. Buy a domain (HOSTING_PRICES.md: .com ≈ $10/yr; .bo ≈ Bs 980/yr, .com.bo ≈ Bs 280/yr). Create an `A` record `api.<domain>` → VPS IPv4 (and `AAAA` for IPv6 if the VPS has one). Optional `www` CNAME.
2. In `.env`: `DOMAIN=api.<domain>`, `ACME_EMAIL=<you>`. Caddy obtains and renews the Let's Encrypt certificate automatically on first start (ports 80 and 443 must be reachable; DNS must already resolve).
3. Check: `curl -I https://api.<domain>/v1/health` → `HTTP/2 200`, `strict-transport-security` header present.
4. Put `https://api.<domain>` in `app/keys.properties` (`PIYO_API_BASE_URL`) and set `manifestPlaceholders["usesCleartextTraffic"] = "false"` in `app/app/build.gradle.kts` before the release build.

## 6. Environment variables (`deploy/.env`)

| Variable | Production value | Why |
|---|---|---|
| `POSTGRES_PASSWORD` | `openssl rand -hex 24` | used only inside the Compose network |
| `DEV_MODE` | `false` | real store, JSON logs |
| `ADMIN_TOKEN` | `openssl rand -hex 32` | `X-Admin-Token` for review endpoints |
| `HASH_SALT` | `openssl rand -hex 32`, **never rotated** | device hashes; rotating orphans quotas/blocks |
| `DOMAIN`, `ACME_EMAIL` | your values | Caddy HTTPS |
| `CORS_ALLOWED_ORIGINS` | empty | native app needs no CORS; set your web origin if you build a web client |
| `PUBLIC_BASE_URL` | set by the prod override to `https://$DOMAIN` | absolute URLs in manifests and styles |
| quotas | defaults (3 reports, 5 suggestions per device per UTC day, 60 req/min per IP) | tune after launch |

Keep `.env` out of git (it is ignored). Store a copy in your password manager.

## 7. Logs and monitoring

- `docker compose logs -f api` (JSON: request_id, method, path, status, duration_ms; no IPs). `docker compose logs caddy` (JSON access log in `data/caddy/access.log`, rotated at 50 MB × 5).
- Health: `GET /v1/health` returns store kind and dataset version; use a free external pinger (UptimeRobot, Better Stack free tier) every 5 minutes on `https://api.<domain>/v1/health`.
- Disk: `df -h` and `docker system df` monthly; `docker image prune -f` after upgrades.
- Resource view: `docker stats --no-stream`. Expect api ≈ 20 MB, martin ≈ 30 MB, postgres ≈ 100 MB, caddy ≈ 20 MB.
- Optional later: Netdata (free, one container) for CPU/RAM/disk graphs, Grafana Cloud free tier for logs.

## 8. Database backups and restore

Daily dump kept 14 days, plus the release files and the PMTiles (they are plain files):

```sh
# /home/piyo/backup.sh
set -e; cd ~/piyo/deploy
mkdir -p ~/backups && D=$(date +%F)
docker compose exec -T db pg_dump -U piyo -Fc piyo > ~/backups/piyo-$D.dump
tar -czf ~/backups/files-$D.tgz data/releases data/tiles/santa-cruz.pmtiles .env
find ~/backups -mtime +14 -delete
# crontab -e:  15 3 * * * /home/piyo/backup.sh >> /home/piyo/backup.log 2>&1
```

Copy `~/backups` off the server (rclone to R2/B2, or `scp` from your PC weekly). Restore on a new server:

```sh
docker compose up -d db && sleep 10
docker compose exec -T db pg_restore -U piyo -d piyo --clean --if-exists < piyo-YYYY-MM-DD.dump
tar -xzf files-YYYY-MM-DD.tgz && docker compose up -d
```

Test the restore once on a throwaway VPS before you need it.

## 9. Dataset release process

```sh
# on your PC (or the VPS): build the release from the source repo and load it into the database
cd backend
go run ./cmd/piyo-import -source ../../santa-cruz-transit-data/data/lines.json \
   -out ../deploy/data/releases -db "postgres://piyo:<pw>@localhost:55432/piyo?sslmode=disable" -notes "October survey"
# -version is optional: the next number is chosen. Output: releases/vN/{manifest.json, dataset-vN.json.gz, ...} + latest.json
```

On the VPS, copy the new `releases/vN` folder (`rsync -av deploy/data/releases/ piyo@vps:~/piyo/deploy/data/releases/`) **before** `latest.json`; the API reads `latest.json` on each manifest request, so the switch is atomic and needs no restart. Apps see the new version within 5 minutes (manifest cache). Old versions stay on disk so a phone mid-download is never broken; delete versions older than a year.

When the database becomes the master (editing tool): the same command with `-source` replaced by a `-from-db` flag (next iteration) exports the tables; the file layout does not change.

Retention job (monthly, see DATABASE_CHOICE.md §3):

```sh
docker compose exec -T db psql -U piyo -d piyo -c "DELETE FROM reports WHERE status='rejected' AND reviewed_at < now() - interval '120 days'; DELETE FROM suggestions WHERE status='rejected' AND reviewed_at < now() - interval '120 days'; DELETE FROM blocks WHERE until_at < now();"
```

## 10. Map data update

```sh
cd ~/piyo/deploy && bash tiles/build-tiles.sh && docker compose restart martin
```

Twice a year is enough; the script fetches only the metro bbox (≈ 11 MB) from the latest Protomaps daily build. Phones with the offline file (future) re-download when `GET /v1/map` reports a newer `pmtiles_updated_at`.

## 11. Updating the API without downtime

Rolling update for a single-host Compose: build the new image first, then recreate the container; the gap is the API start time (< 1 s, migrations are idempotent and fast).

```sh
cd ~/piyo && git pull
cd deploy && docker compose -f docker-compose.yml -f docker-compose.prod.yml build api
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --no-deps api
docker compose logs --tail=20 api && curl -s https://$DOMAIN/v1/health
```

For a true zero-second switch: `docker compose up -d --scale api=2 --no-recreate` then recreate one at a time; Caddy's `reverse_proxy api:8080` resolves both replicas via Docker DNS. Rollback = `git checkout <previous tag>` and the same two commands. Database migrations are additive (`CREATE … IF NOT EXISTS`), so old and new API versions can run side by side.

App updates: the Android app is updated through Google Play; the API keeps serving old clients (`min_app_version` in the manifest tells an obsolete app to update instead of failing).

## 12. Checklist before going live

- [ ] `.env` secrets generated, backed up
- [ ] DNS resolves, `curl -I https://api.<domain>/v1/health` is 200
- [ ] `bash tiles/build-tiles.sh` ran; `curl https://api.<domain>/tiles/santa-cruz/13/2658/4507` returns a tile
- [ ] First dataset released; `/v1/health` says `releases`
- [ ] `backup.sh` in cron and one restore tested
- [ ] Uptime check configured
- [ ] App built with the HTTPS base URL and `usesCleartextTraffic=false`
