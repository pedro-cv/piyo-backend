# Piyo API — guide for AI assistants

Go 1.25 service for the Piyo transit app: publishes immutable dataset releases, serves the self-hosted
map style, receives line reports, NPS and route contributions, and exposes admin review endpoints.
Standard library HTTP; stores: in-memory (dev), SQLite (`modernc.org/sqlite`), PostgreSQL + PostGIS (`pgx`).
`deploy/` holds the Docker Compose stack (PostGIS, API, Martin tiles, Caddy).

If this folder is inside the workspace repo, the root `CLAUDE.md` and `.claude/rules/` also apply.

## Map of the code

| Path | Role |
|---|---|
| `cmd/piyo-api/main.go` | Flags/env → config, store selection (`STORE=memory|sqlite|postgres`), server start |
| `cmd/piyo-import/main.go` | Importer: upstream `lines.json` → dataset release `vN` (+ optional PostGIS load) |
| `internal/config/` | `Config` from env, `Validate()` refuses prod without `ADMIN_TOKEN`/`HASH_SALT` |
| `internal/httpapi/server.go` | Router (`routes()`), middleware: recoverer, access log, CORS, rate limit, body limit, `admin()` |
| `internal/httpapi/handlers_*.go` | One file per area: public, dataset, reports, nps, suggestions, contributions, admin |
| `internal/store/store.go` | `Store` interface — the contract every store implements |
| `internal/store/memory*.go`, `sqlite*.go`, `postgres/` | The three implementations; SQL migrations in `postgres/migrations/` |
| `internal/dataset/` | Release model, builder for both source layouts (`osm`, `named-routes`), polyline compaction, manifest + checksums; `sources.go` maps upstream text → attribution |
| `internal/model/`, `internal/ratelimit/`, `internal/seed/`, `internal/export/` | Shared types, per-IP token bucket, demo data, issue filer |
| `static/` | Legacy embedded bundle (`lines-v12.json`) for `/v1/data/{file}` |
| `tools/check_trips.py` | Cross-checks planner results against the dataset |
| `deploy/` | `docker-compose.yml` (local), `docker-compose.prod.yml`, Caddyfiles, `martin/`, `tiles/build-tiles.sh`, `.env.example` |

API contract (endpoints, headers, bodies, limits): `README.md`. Data model: `docs/implementation/DATA_MODEL.md`.

## Commands

```sh
go test ./... && go vet ./...                 # must be green; no database needed
go run ./cmd/piyo-api                         # DEV_MODE=true → memory store + demo data on :8080
curl -s localhost:8080/v1/health
go run ./cmd/piyo-import -source <lines.json> -out ../deploy/data/releases -version N [-db postgres://…]

cd deploy && cp .env.example .env             # then edit; never commit .env
docker compose up -d --build && docker compose ps
```
Admin calls use `X-Admin-Token`; device calls use `X-Install-Id` (8–64 chars `[A-Za-z0-9-]`).

## Rules that matter most here

1. Secrets only via env / `deploy/.env`; examples carry placeholders. `HASH_SALT` is never rotated casually.
2. No personal data in logs or storage: only `sha256(HASH_SALT + install_id)`; IPs stay in memory.
3. New endpoint = handler + validation + quota/rate limit + body cap + store method in **all three**
   stores + migration + `httptest` test + README row. Use `/new-endpoint`.
4. Releases are immutable; publish `vN+1`, never rewrite `vN`.
5. Provider brands only in `internal/dataset/sources.go` (and `docs/DATA_SOURCES.md`); code speaks of
   source layouts (`osm`, `named-routes`).
6. Images pinned, API non-root, TLS at Caddy. Compose uses `${VAR:-dev-default}` only.
7. `gofmt`/`go vet` clean, tests green, `/ship` before a PR. `main` is PR-only.

## Skills

`/new-endpoint`, `/new-migration`, `/dataset-release`, `/verify-backend` live in `.claude/skills/`.
Root-level `/ship` and `/public-release-check` apply as well.
