# Transit data model

Written 2026-10-28 for iteration 2; §8 added the same evening for iteration 3 (Microcruz source, package format 2). Source: `santa-cruz-transit-data/data/lines.json` (62 lines, 72 OSM relations as variants, 24,977 polyline points, 379 inferred stop entries that dedupe to 114 unique OSM nodes, 2.1 MB pretty JSON). Decisions here follow docs/03-infra-and-tech.md §1 (one versioned bundle, encoded polylines, precomputed transfers) and §3 (immutable files named by version, manifest with checksums).

## 1. What the source looks like and what is wrong with using it as-is

| Source field | Observation | Consequence |
|---|---|---|
| `lines[].variants[]` one per OSM relation | 1 to 3 variants per line; `direction` is a free string "A → B" or null | Normalise to `ida` / `vuelta` / `variante` plus `origin` and `headsign` |
| `variants[].polyline` as `[[lat,lng],…]` | 22 bytes per point, 24,977 points | Encode (Google polyline, 1e-5) → ≈ 5.5 bytes per point |
| `variants[].stops[]` repeated per variant with `osm_node` | The same physical stop appears in several variants | One `stops` table keyed by `n<osm_node>`; variants reference ids |
| `mode` = `bus`, `share_taxi`, `bus+share_taxi` | OSM vocabulary | App vocabulary: `micro`, `trufi`, `other` |
| No street names along the route | The UI shows "main streets" | Use the named stops of the variant in order as the street list (fallback: origin + headsign) |
| No transfer information | On-device routing needs transfer edges (docs/03 §4) | Precompute walking links ≤ 150 m between variants of different lines |
| No colour | UI needs a stable colour per line | Deterministic colour from the number (same formula as the app); overridable per line later |

## 2. Logical model

```
Line 1 ── n Variant 1 ── n VariantStop n ── 1 Stop
                 │
                 └── n Transfer (variant A idx → variant B idx, walk_m)
Dataset (version) = immutable snapshot of all of the above + manifest
Suggestion ── references Line / Variant / Dataset version (user proposals)
```

| Entity | Key | Fields | Notes |
|---|---|---|---|
| Line | `line_id` ("10", "trufi-montero") | `display` (number or short name), `name`, `mode`, `color`, `fare_bs` (empty = global tariff), `variant_ids[]`, `source_url` | ~62 now, ~120 target |
| Variant | `variant_id` ("osm-6945575") | `line_id`, `direction`, `headsign`, `origin`, `polyline` (encoded), `points`, `length_m`, `stop_ids[]`, `streets[]`, `gaps`, `source_url` | one per OSM relation; the editing tool will create its own ids (`v-…`) |
| Stop | `stop_id` ("n5175143938") | `name`, `lat`, `lng` | shared; stops are request stops, approximate |
| Transfer | (`from`, `from_idx`, `to`, `to_idx`) | `walk_m` | directed; ≤ 8 per variant pair, local minima ≥ 12 indices apart |
| Dataset | `version` (int, monotonic) | `generated_at`, `fare_bs`, `license`, `source`, `bbox`, counts, files + checksums | immutable once published |

Why variants carry the geometry and lines do not: a line is what the user searches ("el 10"); a variant is what the router walks along. Keeping geometry per variant avoids duplicating line metadata and lets a report or suggestion point at the exact direction.

## 3. Published files (what the API serves)

```
/v1/manifest                      → manifest of the latest release (absolute URLs)
/v1/datasets                      → {"latest": 13, "versions": [13, 12]}
/v1/datasets/13/manifest.json     → immutable
/v1/datasets/13/dataset-v13.json.gz   the compact package (everything, gzip)   ~134 KB for 62 lines
/v1/datasets/13/lines.json        → route metadata                               11 KB
/v1/datasets/13/stops.json        → stops                                        9 KB
/v1/datasets/13/variants.json     → geometry + stop sequences                   93 KB
/v1/datasets/13/transfers.json    → transfer links                               1 MB raw (≈ 60 KB gz)
/v1/data/{file}                   → legacy alias into the latest release
```

Manifest shape (`backend/internal/dataset/dataset.go`):

```json
{
  "dataset_version": 13, "data_version": 13, "generated_at": "2026-10-28T…", "min_app_version": "1",
  "fare_bs": "2.50", "license": "ODbL 1.0 — © OpenStreetMap contributors",
  "bbox": [-17.855, -63.250, -17.602, -63.086],
  "counts": {"lines": 62, "variants": 72, "stops": 114, "transfers": 12927},
  "package": {"name": "dataset-v13.json.gz", "bytes": 133906, "sha256": "9e02…", "url": "https://…/v1/datasets/13/dataset-v13.json.gz"},
  "files": [{"name": "lines.json", "bytes": 11141, "sha256": "9777…", "url": "…"}, …],
  "url": "…dataset-v13.json.gz", "bytes": 133906, "sha256": "9e02…"
}
```

The last three keys mirror `package` so the iteration-1 app (which only knew `data_version`/`url`/`sha256`) keeps working.

## 4. Versioning and the update protocol

- **Version** = integer, incremented by `piyo-import` (next after the newest release on disk or in the database). Files live in `releases/vN/`; `releases/latest.json` is the only mutable pointer and is replaced atomically.
- **Immutability**: a published version is never edited. Cache headers: files `max-age=31536000, immutable`; `/v1/manifest` `max-age=300`.
- **Releases from contributions** (2026-11-04): `piyo-release` builds vN+1 from vN plus accepted route contributions; its `manifest.json` carries `changes` `{base_version, contribution_ids, lines_added, lines_updated, notes}` (absent on upstream imports). Apps ignore it; `POST /v1/admin/datasets/{v}/publish` reads it to mark those contributions `published_version = v` and notify their senders. See CONTRIBUTIONS_DESIGN.md.
- **Integrity**: every file has `bytes` + `sha256`; the app verifies the package hash before swapping (`HttpManifestRepository.download`).
- **App protocol**: on start with network, `GET /v1/manifest`; if `dataset_version` > installed, download `package.url` (one request, ~134 KB gz), verify, write `lines-current.json`, swap. Keep the previous bundle until the new one parses.
- **Partial updates** (designed, not used by the app yet): compare `files[].sha256` with the installed ones and fetch only the changed files; the four files are independent (`lines` without geometry, `variants` without metadata). For the current size the single package is cheaper than two round-trips, so the app uses the package; partial updates become useful when the geometry grows past ~1 MB or when a release only changes metadata.
- **Compatibility**: `min_app_version` lets a release refuse apps that cannot parse it; the app shows "actualizá la app" instead of crashing.
- **Seed**: the APK ships the latest package as `assets/dataset-seed.json` (the same JSON, uncompressed; the APK compresses assets). First run works offline.

## 5. JSON files vs a database for the master data

Criteria from the brief: a future desktop tool to place points and edit lines; many users sending corrections; the app downloads versioned snapshots.

| | Versioned JSON only (git as the database) | PostgreSQL + PostGIS as working store, JSON as the published format | Document DB (MongoDB) as working store |
|---|---|---|---|
| Editing tool | Edits whole files; two editors conflict at file level; no spatial queries ("variants within 50 m of this point") without loading everything | Row-level edits, transactions, `ST_DWithin`, `ST_ClosestPoint`, snapping, length — the tool is a thin client over SQL | Document-level edits; geospatial indexes exist (2dsphere) but no linestring editing functions |
| Corrections at scale | Reports/suggestions need their own store anyway | Same database: suggestions join lines/variants, reviewer queries are one SQL statement | Possible, but joins are manual |
| Snapshots for the app | Natural: the files are the snapshots | `piyo-import`/release step writes the files from the tables; the `datasets` table records each release | Same as PostgreSQL, with an exporter |
| History / rollback | git history for free | `datasets` rows + files on disk; optional row history later | Needs custom versioning |
| Operations | none | one container, backups with `pg_dump` | one container |
| Fit with existing choice | — | Already chosen for reports (DATABASE_CHOICE.md) | A second database just for geometry |

**Decision**: the working (editable) data lives in PostgreSQL + PostGIS; the **published** data is the set of immutable JSON files above, which is what the app downloads. This matches the brief's assumption. The source repo (`santa-cruz-transit-data`) stays the ingestion input until the editing tool exists; `piyo-import` is the bridge (source → tables → release). When the tool exists, the release step reads the tables instead of the source file (the `dataset.Package` type is the contract either way).

## 6. Suggestions and reports in the model

- `reports`: free-text observations anchored at a point (iteration 1).
- `suggestions`: structured proposals with `kind` ∈ {polyline, stop_add, stop_move, stop_remove, line_meta, missing_line, other}, a JSON `payload` (≤ 16 KB, ≤ 400 points, points inside the metro box), `line_id`/`variant_id`, the `dataset_version` the user saw, `status` pending/accepted/rejected, `review_note`, the salted device hash. Accepting a suggestion is a reviewer action in the tool; the API only stores and lists them.
- Both share the device block list; 3 rejections (reports + suggestions) in 90 days block the device for 30 days.

## 7. Known limits of the current data (carried into the release as-is)

- 9 variants have polyline jumps > 1 km and 6 have no nearby stops (source README). The release does not repair them; the importer reports `gaps` per variant so the tool can prioritise.
- Stops are inferred bus-stop nodes within 25 m, not official stops. Micros stop on request anyway.
- `direction` is null for most variants, so `ida`/`vuelta` are assigned by order, not by geography.
- ~58 city lines are absent; `missing_line` suggestions are how users ask for them.

## 8. Iteration 3: the Microcruz source and package format 2 (2026-10-28)

The sibling repo gained a second dataset, `santa-cruz-transit-data/data-microcruz/lines.json` (133 lines, 147 named routes, 65,396 points, built by `src/microcruz/build.js` from the Microcruz route API plus the colour list `data/lines-and descriptions-with-color.json`). It keeps the OSM file's structure and adds attributes. `piyo-import` accepts both files; the layout is detected per variant.

| New source attribute | Where it lands in the package | Rule |
|---|---|---|
| `mode: "microcruz"` | `lines[].mode = "micro"` | same vocabulary as before |
| `color` (line and variant) + `color_source` (`color-file`, `name-color-word`, `gray`) | `lines[].color`, `lines[].color_source`; `variants[].color`/`color_source` only when a variant differs from its line | a real colour is kept (upper-case hex); a `gray` placeholder is replaced by the derived colour and marked `derived`, so lines without an official colour stay distinguishable |
| `ida` / `vuelta` (split halves of the loop) | two variants per named route: `<variant_id>-ida` and `<variant_id>-vuelta`, `direction` set accordingly | a route without halves falls back to the complete `polyline` as one variant |
| variant `name` ("linea41 Rojo") | `variants[].name` and `variants[].branch` ("Rojo") | the branch is what tells two routes of one number apart; `""` for the main route and for OSM data |
| colour-file `line-detail` ("Urb. La Pascana - Mercado Plan 3000") | `origin` / `headsign` of ida, swapped for vuelta; `streets` = [origin, headsign] | matched by number, by colour when a number has several rows; a detail without " - " is the headsign only; no row → headsign "Línea N Branch" |
| file `license` / `source` | `license`, `source`, new `attribution` ("Recorridos © Microcruz") | the app shows `attribution` on the line screen and `license` in Datos / Acerca de |
| line `scraped_at` | `lines[].source_updated_at` | RFC 3339 |

**Compact transfers.** With 294 variants almost every pair of routes meets within 150 m, so there are ~104k links instead of 13k. As objects with variant ids (format 1) that was 11 MB raw; format 2 stores each link as `[from_variant, from_idx, to_variant, to_idx, walk_m]` with positions in the `variants` array: 1.9 MB raw, 2.1 MB for the whole package uncompressed, 658 KB gzip. `package_format: 2` is written in the package and the manifest. The app parser and `backend/tools/check_trips.py` read both forms.

**Release v14** (`deploy/data/releases/v14`): 133 lines, 294 variants, 0 stops (Microcruz has none), 103,889 links, bbox −18.16..−17.34 / −63.51..−62.99 (Montero, Warnes and Viru Viru extensions). The APK seed is this package uncompressed (2,118,551 bytes).

**Database.** `lines.color_source`, `lines.source_updated_at`, `variants.name`, `variants.branch`, `variants.color`, `variants.color_source` were added with idempotent `ALTER TABLE … ADD COLUMN IF NOT EXISTS` in `001_init.sql`; the importer upserts them.

**Known data limits carried into v14.** The colour file assigns the same colour to both branches of 41, 44, 60, 61, 64 (the builder in the data repo picks one row per number), so branch chips share a colour and 44 Azul gets 44 Rojo's endpoints; 74 of 294 variants have no origin because their number has no row or an empty detail (1, 104B, 116, 124–126, 129, 130, 136, 140–142, 151). Stops and street names are gone with the OSM source; the line screen lists origin and destination only.

### 8.1 Orientation of ida/vuelta (dataset v15, 2026-10-29)

The colour file's `line-detail` ("A - B") does not say which half of the loop is the "ida". Checking 110 routes against geocoded endpoints (Photon, bbox-filtered, cached in `data/endpoints-geocode.json` next to the colour file) found 53 reversed and 45 consistent, so the order is not usable as a direction. The importer now computes `score = (d(last, A) − d(last, B)) + (d(first, B) − d(first, A))` on the ida half; `score < −800 m` swaps the names, `> 800 m` keeps them, in between (or when a name is not geocoded) the "A - B" order is kept and the variant is marked `direction_source: "assumed"`. Variants whose names were oriented carry `direction_source: "geocoded"`. The geocode file is a plain `{"name": {"lat", "lng", "matched"} | null}` map and can be edited by hand when Photon picked the wrong place.

## 9. Iteration 4: local data providers and route contributions (2026-10-30)

Design: `CONTRIBUTIONS_DESIGN.md`. Summary of the shapes that matter for data work:

- **Provider file** (`filesDir/providers/<id>.json`, also the export/import format, `format: "piyo-provider/1"`):
  `id`, `name`, `created_at`, `updated_at`, `base_dataset_version`, `lines[]` and `variants[]` in the **package format 2
  shapes** (so `DatasetParser`/`DatasetWriter` and the backend read them unchanged), `transfers[]` as
  `{from, from_idx, to, to_idx, walk_m}` objects with variant ids, and `edits{line_id → {op, based_on_version,
  before{variant_id → polyline}, edited_at, sent{id, at, status, note}}}`.
- **Effective bundle** (`EffectiveLinesRepository.merge`): official lines with overridden ids replaced (`Line.local = UPDATED`),
  new lines appended (`ADDED`), official transfer links touching a replaced variant dropped, provider links added,
  `LineBundle.providerName` set. The planner and every screen read this bundle; `AppContainer.officialLines` keeps the official one.
- **Transfer links for local routes** (`TransferBuilder`): for each local variant against every other variant whose bounding
  box is within 150 m, up to 3 links per ordered pair spread along the local route (nearest-vertex, equirectangular),
  both directions. Same semantics as the importer's links (§8).
- **Contribution** (`POST /v1/contributions`): `provider_name`, `base_dataset_version`, `app_version`, `comment`, the chosen
  `lines[]`/`variants[]` and `edits{}` with `before` polylines. Stored verbatim in `contributions.payload` (JSONB in
  PostgreSQL, `migrations/002_contributions.sql`; TEXT in SQLite). `route_ids` is denormalised for listing. Monthly quota per
  device (`MAX_CONTRIBUTIONS_PER_MONTH`, default 3), ≤ 10 lines, ≤ 600 points per variant, every point inside `METRO_BOUNDS`.
- New line ids from the app: `u-<display>-<4 hex>`; variant ids `<line_id>:<direction>`. Updated official lines keep their ids,
  so favourites, history and transfer links stay valid.

Push tokens (2026-11-01, `migrations/003_push_tokens.sql`; same table in SQLite `sqlite.go`): `push_tokens(device_hash
PK, token, updated_at)`. One FCM token per device, keyed by the salted device hash the other tables use. Written by
`PUT /v1/push/token`, removed by `DELETE`, by an `UNREGISTERED` answer from FCM, and by the daily purge of rows older
than 60 days. Read when an admin reviews that device's contribution or report (PRIVACY.md §Push).

Reviewer workflow: `GET /v1/admin/contributions?status=pending` → `GET /v1/admin/contributions/{id}` (payload with `before`
so the diff is visible even if the official data moved on) → verify → `accept`/`reject` with a note. Accepted routes are merged
into the data repository by hand and shipped in the next `piyo-import` release; the device sees the status through
`GET /v1/contributions/mine` on the Aportar tab.
