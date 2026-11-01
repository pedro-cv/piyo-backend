# Database choice

Written 2026-10-28. Load profile: many concurrent users sending small reports and suggestions (≈1 KB JSON each; even 100k monthly users produce well under 10 writes per second at peak), a few thousand reviewer reads per month, geographic data (route linestrings, stop points, report points), and the editable master transit data behind a future desktop tool.

## 1. Options compared

| Criterion | PostgreSQL 16 + PostGIS 3.5 | MongoDB 7 (document) | SQLite (iteration 1) |
|---|---|---|---|
| Concurrent writes from many clients | MVCC, row locks; thousands of inserts/s on a 2 vCPU VPS | Good; document-level locking | One writer at a time; fine up to ~hundreds of writes/s but no network server |
| Geographic data | `GEOGRAPHY(LINESTRING)` / `POINT`, GIST indexes, `ST_DWithin`, `ST_ClosestPoint`, `ST_LineLocatePoint`, `ST_Length` — everything an editing tool needs | 2dsphere index, `$near`, `$geoWithin`; no linestring editing or snapping functions | SpatiaLite exists but is not in the pure-Go driver |
| Relational integrity | Foreign keys line → variant → stop, transactions across tables | Manual references | Yes |
| Suggestion payloads (schemaless) | `JSONB` with GIN index if needed | Native | TEXT |
| Operations | One container, `pg_dump`/`pg_restore`, mature backups, 100 MB RAM at rest | One container, `mongodump` | A file |
| Hosting cost | Free on the VPS; managed from ~$15/month | Free on the VPS; Atlas free tier 512 MB | Free |
| Team familiarity / tooling | psql, QGIS connects directly to PostGIS (useful for the editing tool) | Compass | DB Browser |
| Fit | **Best**: one store for user input and the editable map data | Adequate for user input, weak for editing geometry | Dev/single-binary fallback |

**Decision: PostgreSQL + PostGIS** as the only server database. The Go API supports `STORE=memory` (dev), `STORE=sqlite` (single binary, no spatial queries) and `STORE=postgres`; Docker Compose uses postgres.

## 2. Schema

`backend/internal/store/postgres/migrations/001_init.sql`, applied idempotently at every start.

| Table | Purpose | Key columns |
|---|---|---|
| `lines` | editable line metadata | `line_id` PK, `display`, `name`, `mode` CHECK, `color`, `fare_bs`, `source_url`, `updated_at` |
| `variants` | one direction of a line | `variant_id` PK, `line_id` FK (cascade), `seq`, `direction`, `headsign`, `origin`, `geom GEOGRAPHY(LINESTRING,4326)`, `length_m`, `gaps`, `streets JSONB`, `source_url` |
| `stops` | shared request stops | `stop_id` PK, `name`, `geom GEOGRAPHY(POINT,4326)` |
| `variant_stops` | ordered stop sequence | PK (`variant_id`, `seq`), `stop_id` FK |
| `datasets` | published releases | `version` PK, `generated_at`, `published_at`, `manifest JSONB`, `notes` |
| `reports` | user observations | `id`, `device_hash`, `line_id`, `type`, `geom POINT`, `comment`, `app_version`, `data_version`, `status` CHECK, `created_at`, `reviewed_at` |
| `suggestions` | structured proposals | `id`, `device_hash`, `kind`, `line_id`, `variant_id`, `dataset_version`, `geom POINT`, `payload JSONB`, `comment`, `app_version`, `status` CHECK, `review_note`, `created_at`, `reviewed_at` |
| `nps` | feedback | `id`, `device_hash`, `score` CHECK 0..10, `reasons JSONB`, `comment`, versions, `device_class`, `created_at` |
| `blocks` | temporary device blocks | `device_hash` PK, `until_at`, `reason`, `created_at` |

### Indexes and why

| Index | Serves |
|---|---|
| `reports(device_hash, created_at)`, `suggestions(device_hash, created_at)` | the per-device daily quota count on every POST (hot path) |
| `reports(status, created_at DESC)`, `suggestions(status, created_at DESC)` | reviewer lists "pending, newest first" |
| `suggestions(line_id)` | reviewer filter by line |
| GIST on `reports.geom`, `suggestions.geom` | "what was reported near this point" in the editing tool |
| GIST on `variants.geom`, `stops.geom` | snapping, nearest variant, bbox queries in the tool |
| `variants(line_id)`, `variant_stops(stop_id)` | joins |
| `blocks` PK | block check on every POST |

Expected sizes: 100k users × 3 reports/day worst case is 300k rows/day; realistic volumes are two orders lower. A year of realistic traffic is well under 1 GB.

## 3. Privacy and retention of reports and suggestions

- Stored identity is `device_hash = sha256(HASH_SALT + install_id)` only; IPs never reach the database (in-memory rate limiter); no user agent.
- **Retention policy** (to be run as a monthly job; not automated yet):
  - `rejected` reports/suggestions: delete after 120 days (they only matter for the 90-day auto-block window).
  - `accepted` ones: keep the row but null the `device_hash` after the data release that applied them has shipped plus 90 days (the FCM thank-you window of docs/03 §8). After that nothing links a correction to a device.
  - `pending` older than 365 days: delete (stale against the current data).
  - `nps`: keep aggregated exports; delete raw rows older than 24 months, and strip `device_hash` from the published CSV.
  - `blocks`: rows expire by `until_at`; delete expired rows weekly.
- SQL for the job is intentionally simple, e.g. `DELETE FROM reports WHERE status='rejected' AND reviewed_at < now() - interval '120 days';` (see DEPLOY_VPS.md §9).

## 4. Backups

`pg_dump -Fc` daily from the host via `docker compose exec db` (DEPLOY_VPS.md §8). The releases directory and the PMTiles file are plain files; back them up with the dump. Restore is `pg_restore` into an empty database created by the container.

## 5. What is not in the database on purpose

- Raw install ids, IPs, user agents, FCM tokens (tokens, when implemented, live in a separate short-lived table keyed by report id and purged per release).
- Published dataset files: on disk under `RELEASES_DIR`, immutable; the database only records the manifest. Serving a 134 KB file from disk through Caddy is cheaper than from a table and lets a CDN cache it.
- Map tiles: PMTiles file served by Martin, not by PostGIS (no need for dynamic tiles; the basemap changes twice a year).
