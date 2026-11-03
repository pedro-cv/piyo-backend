# Going live: backend push, smoke test, server and app configuration

Written 2026-11-01. Order of work: (1) push the backend and smoke-test it on your PC without Docker,
(2) put the server up and publish the dataset, (3) build the app against it. Nothing here needs Firebase,
Google services or any paid provider: the app has no analytics, push or accounts, and the map, geocoder and
data are served by your own API plus two free public services (see `SETUP_AND_RUN.md` §5).

## 1. Push the backend (public repo `piyo-backend`)

The public clone lives next to the workspace (`../piyo-backend`, remote `origin` = GitHub, branch `main`,
protected: PR only, CI + secret scan required). Changes flow from the workspace through the export script.

```sh
cd piyo
bash scripts/check-secrets.sh                                   # must print OK
bash scripts/export-public-repo.sh backend /tmp/piyo-backend-export
cd ../piyo-backend
git switch -c chore/smoke-test-collection
rsync -a --delete --exclude .git /tmp/piyo-backend-export/ ./   # Git Bash without rsync: robocopy /MIR, keep .git
go test ./... && go vet ./... && bash scripts/check-secrets.sh
git add -A && git commit -m "Add Bruno smoke-test collection and branding notes"
git push -u origin chore/smoke-test-collection
gh pr create --fill && gh pr merge --squash --auto --delete-branch
```

CI (`Backend tests`, `Secret scan`) must be green before the auto-merge lands. Tag the result when the
smoke test below passes: `git tag v0.1.0 && git push origin v0.1.0`.

## 2. Smoke test on your PC: no Docker, real store, app in debug

### 2.1 Build a dataset release (once)

The API serves whatever `RELEASES_DIR` contains; without it, it falls back to an embedded 3-line sample
(`dataset_source: "embedded"` in `/v1/health`) and the app would "update" to that sample. Build v15 from
the current source first:

```sh
cd piyo/backend
go run ./cmd/piyo-import -source ../../santa-cruz-transit-data/data-microcruz/lines.json \
  -out ./releases -version 15 -dry-run          # read the summary: lines, variants, bbox, attribution
go run ./cmd/piyo-import -source ../../santa-cruz-transit-data/data-microcruz/lines.json \
  -out ./releases -version 15                   # writes releases/v15/{manifest.json, package}, latest.json
```

`backend/releases/` is git-ignored. v15 matches the seed bundled in the app, so the app will report
"datos al día"; build v16 later to see the update path.

### 2.2 Run the API like production, SQLite instead of PostgreSQL

```sh
cd piyo/backend
export DEV_MODE=false STORE=sqlite SQLITE_PATH=./piyo.db RELEASES_DIR=./releases
export ADMIN_TOKEN=$(openssl rand -hex 32) HASH_SALT=$(openssl rand -hex 32)
export PUBLIC_BASE_URL=http://<PC-IPv4>:8080        # the phone must be able to open the manifest url
go run ./cmd/piyo-api
curl -s localhost:8080/v1/health   # {"mode":"prod","store":"sqlite","dataset_version":15,"dataset_source":"releases"}
```

PowerShell: `$env:DEV_MODE="false"` and so on. Keep the two generated values in your password manager if
you intend to reuse this database; `HASH_SALT` must never change once real devices have quotas or blocks.
Allow TCP 8080 inbound in Windows Firewall for the phone. The map endpoints return 404 until
`deploy/tiles/build-tiles.sh` has produced the style; for this test set `MAP_STYLE_URL` in the app (below).

### 2.3 Bruno: every endpoint

`backend/bruno/` is the collection (`backend/README.md` §Smoke test with Bruno). Open it in Bruno, choose
the environment `local-prod`, set the secret `adminToken` to the value you exported, and run the collection
runner on the three folders in order. Or from a terminal:

```sh
cd piyo/backend/bruno
npx @usebruno/cli run --env local-prod -r --env-var adminToken=$ADMIN_TOKEN
```

Expected: every request green. The two map requests and the legacy data alias accept 404. Quotas are real
(3 reports per device per UTC day, 5 suggestions, 3 contributions per month): a second full run the same
day needs another `installId`.

What is not sensitive in this test: install ids are made-up strings, the server stores only their salted
hash, coordinates are the city centre, comments say "Smoke test".

### 2.4 App in debug against that API

```properties
# app/keys.properties (git-ignored)
PIYO_API_BASE_URL=http://<PC-IPv4>:8080
MAP_STYLE_URL=https://tiles.openfreemap.org/styles/liberty   # until your own tiles exist
GEOCODER_URL=https://photon.komoot.io/api/
```

`usesCleartextTraffic` stays `true` for http. Then `./gradlew :app:installDebug` and walk the device
checklist in `/verify-app`: first run, a trip, a report (appears in Bruno `3-admin/02-reports-list`), the NPS
sheet from Datos y conexión, a drawn route sent as contribution (`3-admin/08`), Datos → "Buscar
actualización" (manifest v15, up to date).

## 3. Server: what to configure so it provides data

Two options. **$0 to start**: `FREE_TIER.md` (Neon + Cloud Run or Render, OpenFreeMap tiles, releases baked
into the image). **Own VPS** when traffic justifies it: `DEPLOY_VPS.md`, summarised below. Summary of what you must create or set:

| Item | Where | Value |
|---|---|---|
| VPS 2 vCPU / 4 GB, Ubuntu 24.04, Docker | provider of your choice | §1–2 of DEPLOY_VPS |
| DNS `A api.<domain>` → VPS | registrar | before first start (Let's Encrypt) |
| `deploy/.env` | server only, never in git | `DEV_MODE=false`, `POSTGRES_PASSWORD`, `ADMIN_TOKEN`, `HASH_SALT` (random, the salt is permanent), `DOMAIN`, `ACME_EMAIL`, quotas at defaults |
| Stack | `docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build` | Caddy 80/443, API, PostGIS, Martin |
| Map tiles and style | `bash deploy/tiles/build-tiles.sh` on the server | fills `/v1/map/style.json`; then clear `MAP_STYLE_URL` in the app |
| Dataset release | `go run ./cmd/piyo-import -source … -out deploy/data/releases -version 15 -db "$DATABASE_URL"` | `/v1/manifest` points at v15; `/dataset-release` skill has the checklist |
| Backups | cron `pg_dump` + the releases folder | §8 of DEPLOY_VPS |
| Smoke test | Bruno environment `server`, `baseUrl=https://api.<domain>` | same collection |

## 4. App: configuration to build for production

| Setting | Where | Production value |
|---|---|---|
| `PIYO_API_BASE_URL` | `app/keys.properties` | `https://api.<domain>` |
| `MAP_STYLE_URL` | `app/keys.properties` | empty (style from the API) once tiles exist |
| `GEOCODER_URL` | `app/keys.properties` | `https://photon.komoot.io/api/` (self-host Photon past ~10k MAU) |
| Cleartext | `app/app/build.gradle.kts` `manifestPlaceholders["usesCleartextTraffic"]` | `false` |
| Signing | release keystore created with `keytool -genkeypair`, kept outside the repo; paths and passwords in `keys.properties` | required for Play; today release signs with the debug key |
| Version | `versionCode` / `versionName` in `build.gradle.kts` | bump per release; `MIN_APP_VERSION` on the server gates old builds |
| Play Console | $25 one-time; data-safety form says "no data collected" except the anonymous hashed id for abuse control; privacy policy URL = the landing page section or a static page | — |
| Landing | `landing/src/data/site.json` → `playStore.url` | set when the listing exists |

Firebase: only Cloud Messaging, optional, for review-result and new-release notifications (`FREE_TIER.md`
§2b, `PRIVACY.md` §Push). No Crashlytics, no Analytics, no google-services.json.
Google Maps: not used; the map is MapLibre on your own tiles. No `google-services.json`, no API keys to
create. The only external services at runtime are OpenFreeMap (fallback style, optional) and Photon.

## 5. Before the first public release

- `docs/DATA_SOURCES.md`: confirm the named-routes provider allows redistribution of the geometry in the
  public repo and APK; otherwise ship without the seed.
- Rotate `ADMIN_TOKEN` if it was ever pasted anywhere; keep `HASH_SALT` forever.
- `bash scripts/check-secrets.sh` and `/public-release-check` in both public repos.
