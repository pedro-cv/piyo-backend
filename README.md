# Piyo API

Backend for Piyo, the free public-transport app for Santa Cruz de la Sierra.
Go 1.25, standard library plus `modernc.org/sqlite` (pure Go).

## Run (dev)

```sh
cd backend
go run ./cmd/piyo-api        # or: make run
```

`DEV_MODE=true` is the default: in-memory store seeded with demo data,
admin token `dev-admin-token`, hash salt `dev-salt`, CORS `*`.
Demo install ids: `demo-device-0001`..`0004`, and `blocked-demo-device`
(blocked for 30 days).

Production: `DEV_MODE=false ADMIN_TOKEN=... HASH_SALT=... go run ./cmd/piyo-api`
uses SQLite at `SQLITE_PATH`. All variables are documented in `.env.example`.
Flags `-port`, `-dev`, `-sqlite` override the environment.

## Conventions

- All endpoints are JSON under `/v1`; errors use
  `{"error":{"code":"...","message":"..."}}` plus optional top-level fields
  (`blocked_until`, `resets_at`).
- Device endpoints need `X-Install-Id` (8-64 chars `[A-Za-z0-9-]`). The
  server only stores `sha256(HASH_SALT + install_id)`.
- Admin endpoints need `X-Admin-Token` equal to `ADMIN_TOKEN` (401 otherwise).
- All times are UTC RFC 3339. The daily report quota is per **UTC calendar
  day**; `resets_at` is the next 00:00 UTC.
- Bodies are limited to 64 KB; requests are rate limited per IP
  (`IP_RATE_PER_MINUTE`, 429 `rate_limited`). IPs are never stored or logged.
- Blocked devices cannot send reports (403 `blocked`) but **can** send NPS:
  NPS is only aggregated and is not abuse-prone, so the signal is kept.

## Endpoints

| Method | Path | Auth | Result |
|---|---|---|---|
| GET | `/v1/health` | - | `{"status":"ok","mode":"dev","time":...}` |
| GET | `/v1/manifest` | - | data bundle manifest (version, url, bytes, sha256, min_app_version, fare_bs) |
| GET | `/v1/data/{file}` | - | embedded bundle, cached immutable |
| GET | `/v1/reports/quota` | install id | `{"limit":3,"remaining":n,"resets_at":...,"blocked_until":null}` |
| POST | `/v1/reports` | install id | 201 `{"id":"r_...","remaining":n}`; 400 validation, 403 blocked, 429 quota |
| POST | `/v1/nps` | install id | 201 `{"id":"n_..."}` |
| GET | `/v1/admin/reports?status=pending&limit=100` | admin | `{"count":n,"reports":[...]}` (status: pending default, accepted, rejected, all) |
| POST | `/v1/admin/reports/{id}/accept` | admin | 200 report |
| POST | `/v1/admin/reports/{id}/reject` | admin | 200 report, plus `blocked_until` when the auto-block triggers |
| GET | `/v1/admin/blocks` | admin | `{"count":n,"blocks":[...]}` |
| POST | `/v1/admin/blocks` | admin | 201 block; body `{"install_id":"..."}` or `{"device_hash":"..."}`, optional `days`, `reason` |
| DELETE | `/v1/admin/blocks/{device_hash}` | admin | 204 |
| GET | `/v1/contributions/quota` | install id | `{"limit":3,"remaining":n,"resets_at":<1st of next UTC month>,"blocked_until":null,"max_routes":10}` |
| POST | `/v1/contributions` | install id | 201 `{"id":"c_...","batch_id":"b_...","contributions":[{"id":"c_...","route_id":"76"}],"remaining":n,"resets_at":...}`: **one row per route**, the request is one batch for the quota; 400 validation, 403 blocked, 413 too large, 429 monthly quota. Body cap 512 KB on this route only |
| DELETE | `/v1/contributions/{id}` | install id | 204 withdraws this device's **pending** route; 404 unknown / another device's; 409 `not_pending` once reviewed |
| GET | `/v1/contributions/mine` | install id | this device's routes: id, batch_id, status, review_note, route_ids, published_version (no payloads) |
| PUT | `/v1/push/token` | install id | 204; body `{"token":"<FCM registration token>"}`; one row per device (upsert), so no quota beyond the per-IP limit; 400 validation |
| DELETE | `/v1/push/token` | install id | 204 (notifications turned off) |
| GET | `/v1/admin/contributions?status=pending&limit=100[&published=true\|false]` | admin | summaries (no payload); `status=accepted&published=false` = what the next release can include |
| GET | `/v1/admin/contributions/{id}` | admin | full contribution with payload (`lines`, `variants`, `edits.<line_id>.before` = original polylines) |
| POST | `/v1/admin/contributions/{id}/accept` / `reject` | admin | 200 summary; body `{"note":"..."}`; rejections count towards the auto-block |
| GET | `/v1/admin/datasets` | admin | releases on disk with their manifests (`changes` on releases built from contributions) |
| POST | `/v1/admin/datasets/{version}/publish` | admin | 200 `{"version":N,"published_contributions":[...],"skipped_contributions":[...],"devices_notified":n,"announced":bool,"push_enabled":bool,"pending_reports_on_changed_lines":[...]}`; marks the release's contributions published, pushes `contribution_published` once per device, announces to the `dataset` topic; body `{"notify":true,"announce":true}` optional; idempotent; 404 not deployed here, 409 `not_latest` |
| POST | `/v1/admin/push/dataset` | admin | 200 `{"sent":true,"topic":"dataset","version":N}`; body `{"version":N}` optional (default latest release); 503 `push_disabled` without `FCM_PROJECT_ID`, 502 `push_failed` |
| GET | `/v1/admin/nps.csv` | admin | CSV export |
| GET | `/v1/admin/reports.json` | admin | JSON export |

Report body: `{"type":"stop|route|gone|missing|other","lat":..,"lng":..,"line_id"?:"76","comment"?:"<=500","app_version":"1","data_version":12}`.
Lat/lng must be inside `METRO_BOUNDS`.

NPS body: `{"score":0..10,"reasons":["faltan_lineas","recorridos_mal","el_mapa","es_lento","me_sirvio","otra_cosa"],"comment"?:"<=140","app_version":"1","data_version":12,"device_class"?:"android-low"}`.

Contribution body (routes from a local data provider, docs/implementation/CONTRIBUTIONS_DESIGN.md):
`{"provider_name":"Mis rutas","base_dataset_version":15,"app_version":"1","comment"?:"<=500","lines":[{line_id,display,name,mode,color?}],"variants":[{variant_id,line_id,direction,headsign,origin?,polyline,streets?}],"edits":{"<line_id>":{"op":"add|update","based_on_version":15,"before":{"<variant_id>":"<polyline>"}}}}`.
Limits: ≤ 10 lines, ≤ 600 points per variant, every point inside `METRO_BOUNDS`, `MAX_CONTRIBUTIONS_PER_MONTH` (default 3) per device per UTC month.

Push (optional, `FCM_PROJECT_ID`): when an admin accepts or rejects a contribution, or accepts a report, the
device's registered token gets a data-only FCM message `{"type":"contribution_accepted|contribution_rejected|report_accepted","id":"..."}`;
`/v1/admin/push/dataset` sends `{"type":"dataset","version":"N"}` to the `dataset` topic. Messages carry no
text (the app owns the copy). Unregistered tokens are deleted on the first failed send, tokens not refreshed
for 60 days are purged daily. Credentials: the Cloud Run service account (role *Firebase Cloud Messaging API
Admin*) via the metadata server, or `FCM_CREDENTIALS_FILE` (service-account JSON, VPS only, never in git).

## New dataset release from accepted contributions

```sh
# 1. build v(N+1) from the latest release + accepted, unpublished contributions (nothing changes on the server)
export ADMIN_TOKEN=...                        # never paste it in a PR or log
go run ./cmd/piyo-release -api https://<api> -releases ./releases -dry-run   # report only
go run ./cmd/piyo-release -api https://<api> -releases ./releases -notes "aportes de noviembre"
# 2. deploy the API with releases/v(N+1) (Cloud Run: bash deploy/cloudrun/deploy.sh <project>)
curl -s https://<api>/v1/manifest            # dataset_version = N+1
# 3. mark the contributions published and notify their senders
curl -X POST -H "X-Admin-Token: $ADMIN_TOKEN" https://<api>/v1/admin/datasets/<N+1>/publish
```

Conflicting contributions (the line changed in a later release, a reused line id, …) are skipped and listed;
`-force` applies them, `-ids c_a,c_b` limits the merge, `-contributions file.json` works offline. Details:
`docs/implementation/CONTRIBUTIONS_DESIGN.md` → "Releases from contributions".

## curl

```sh
H='X-Install-Id: demo-device-0001'
A='X-Admin-Token: dev-admin-token'
B=http://localhost:8080/v1

curl $B/health
curl $B/manifest
curl -o lines-v12.json $B/data/lines-v12.json
curl -H "$H" $B/reports/quota
curl -H "$H" -H 'Content-Type: application/json' -d '{"type":"stop","lat":-17.7833,"lng":-63.1821,"line_id":"76","comment":"Parada movida","app_version":"1","data_version":12}' $B/reports
curl -H "$H" -H 'Content-Type: application/json' -d '{"score":9,"reasons":["me_sirvio"],"comment":"Gracias","app_version":"1","data_version":12,"device_class":"android-mid"}' $B/nps
curl -H "$A" "$B/admin/reports?status=pending&limit=50"
curl -X POST -H "$A" $B/admin/reports/r_seed00000001/accept
curl -X POST -H "$A" $B/admin/reports/r_seed00000002/reject
curl -H "$A" $B/admin/blocks
curl -H "$A" -H 'Content-Type: application/json' -d '{"install_id":"demo-device-0002","days":7,"reason":"spam"}' $B/admin/blocks
curl -X DELETE -H "$A" $B/admin/blocks/<device_hash>
curl -H "$H" $B/contributions/quota
curl -H "$A" "$B/admin/contributions?status=pending"
curl -H "$A" $B/admin/contributions/c_xxxxxxxxxxxx
curl -X POST -H "$A" -H 'Content-Type: application/json' -d '{"note":"verificado"}' $B/admin/contributions/c_xxxxxxxxxxxx/accept
curl -H "$A" $B/admin/nps.csv
curl -H "$A" $B/admin/reports.json
```

## Smoke test with Bruno

`bruno/` is a [Bruno](https://www.usebruno.com) collection with every endpoint above, ordered as a smoke
test (`1-public`, `2-device`, `3-admin`). Open the folder in Bruno, pick an environment and run the
collection runner; or from a terminal:

```sh
cd backend/bruno
npx @usebruno/cli run --env dev -r                       # against DEV_MODE (admin token dev-admin-token)
npx @usebruno/cli run --env local-prod -r --env-var adminToken=$ADMIN_TOKEN   # DEV_MODE=false on your PC
npx @usebruno/cli run --env server -r --env-var baseUrl=https://api.<domain> --env-var adminToken=$ADMIN_TOKEN
```

The environments `local-prod` and `server` declare `adminToken` as a secret variable: Bruno keeps its value
in your local profile, never in the repo. Quotas are real: the report request passes 3 times per UTC day per
`installId`, the contribution 3 times per month; change `installId` or restart the DEV_MODE server (memory
store) to reset them.

## Issue filing

Accepted reports are passed to an `export.IssueFiler`. Today only
`LogIssueFiler` exists (it logs the issue it would create); a
`GitHubIssueFiler` that opens public issues is a next iteration.
