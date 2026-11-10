# Route contributions, local data providers and related fixes — design

Status: design approved for implementation (iteration 4). Author: Claude, 2026-10-30.

## 1. Goals

1. Let a user add or update a route (micro, trufi or other transport with a fixed route) on the phone,
   keep it as a **local data provider**, use it (or not) for route calculation, export/import it, and
   send only the changed routes as a **contribution** for review.
2. Give reviewers time: the server accepts a **small number of contributions per month per device**.
3. Fixes: editable favourite places with icon + colour; a notification setting that really asks for the
   permission (or lets the user turn it off); the new feature in its **own tab**.

Non-goals: accounts, server-side merging of contributions into releases (reviewers do that with the
existing `piyo-import` pipeline), editing stops.

## 2. Concepts

| Term | Meaning |
|---|---|
| **Main provider** | The published dataset (seed + downloaded releases, today v15, named-routes source). Read-only. |
| **Local provider** | A named overlay the user owns: a set of **route overrides** (updated lines) and **new lines**, plus the transfer links computed for them. Stored only on the phone, exportable as one JSON file. |
| **Effective bundle** | Main bundle with the active provider's overlay applied: overridden lines replace the main line of the same `line_id`, new lines are appended, overlay transfers are added. This is what every screen and the planner see. |
| **Contribution** | A submission to the server: the overlay routes the user picked (not the whole provider), with the base dataset version and, for updates, the original geometry they started from, so reviewers can diff even when the main data has moved on. |

A provider is created automatically the first time the user saves a route while the main provider is
active; the save dialog asks for a name (default "Mis rutas"). Saving while a local provider is active
writes into that provider. Any number of providers may exist; exactly one is active (main by default).

## 3. Data

### 3.1 Provider file (phone, `filesDir/providers/<id>.json`, also the export format)

```json
{
  "format": "piyo-provider/1",
  "id": "p_7f3a…",                 // random, stable across export/import
  "name": "Mis rutas",
  "created_at": "2026-10-30T03:12:00Z",
  "updated_at": "…",
  "base_dataset_version": 15,       // main version when the provider was created
  "lines":    [ …package-format-2 line objects… ],      // overridden + new lines (full objects)
  "variants": [ …package-format-2 variant objects… ],   // their variants, encoded polylines
  "transfers": [[fromVariantId, fromIdx, toVariantId, toIdx, walkM], …],  // ids, not positions
  "edits": { "<line_id>": { "op": "update"|"add", "based_on_version": 15,
                            "before": { "<variant_id>": "<polyline>" }, "edited_at": "…",
                            "sent": { "id": "c_…", "at": "…", "status": "pending|accepted|rejected" } } }
}
```

Lines and variants reuse the dataset package shapes so `DatasetParser` decodes them and a new
`DatasetWriter` encodes them. New lines get `line_id = "u-<display>-<rand4>"`, variants
`"<line_id>:<direction>"`. Transfers are computed on save for the overlay variants against every other
variant of the effective bundle (brute force ≤ 150 m, `Dispatchers.Default`, cached in the file).

Import: the user picks a `.json` with `OpenDocument`; the file is validated, given its own copy, and
listed. Export: `ACTION_SEND` of the file through a `FileProvider` (`piyo-provider-<name>.json`).

### 3.2 Repository layer (app)

- `ProvidersRepository` — index in DataStore `piyo_providers` (active id + meta list), files on disk.
  API: `providers: Flow<List<ProviderMeta>>`, `active: StateFlow<LocalProvider?>`, `create(name)`,
  `rename`, `activate(id|null)`, `remove(id)`, `saveLine(providerId, line, before)`, `deleteLine`,
  `export(id): File`, `import(uri)`, `markSent(lineIds, contributionId)`.
- `LinesRepository` keeps the main bundle; a new `EffectiveLinesRepository` wraps it and the active
  provider and exposes the merged `bundle`. `LineBundle` gains `providerName: String?`; `Line` gains
  `local: LocalEdit?` (`ADDED` / `UPDATED`) so lists can show an "editada" badge.
- `ContributionsRepository` — `quota()`, `send(provider, lineIds, comment)`, `mine()` (statuses).

### 3.3 Server

New table `contributions` (memory / SQLite / PostgreSQL), same review life cycle as suggestions:

```
id, device_hash, provider_name, base_dataset_version, route_count, payload (jsonb), comment,
app_version, status (pending|accepted|rejected), review_note, created_at, reviewed_at
```

Payload = the routes sent (`lines`, `variants`, `edits` with `before`, `transfers` omitted). Limits:
≤ 10 routes, ≤ 600 points per variant, every point inside `METRO_BOUNDS`, body ≤ 512 KB on this
route only (the global 64 KB cap stays for everything else).

Endpoints (device ones require `X-Install-Id`, same hashing as reports):

| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/contributions/quota` | `{limit, remaining, resets_at (1st of next UTC month), blocked_until}` |
| POST | `/v1/contributions` | create → 201 `{id, remaining, resets_at}`; 403 blocked; 429 monthly quota |
| GET | `/v1/contributions/mine` | this device's contributions: id, status, review_note, created_at, route ids |
| GET | `/v1/admin/contributions?status=` | list (summary, no payload) |
| GET | `/v1/admin/contributions/{id}` | full payload for review |
| POST | `/v1/admin/contributions/{id}/accept` / `reject` | review with note; rejections count towards the shared auto-block |

Config: `MAX_CONTRIBUTIONS_PER_MONTH` (default **3**). Quota counts rows created since the first day of
the current UTC month.

## 4. App UI

### 4.1 New tab "Aportar" (4th tab, icon `EditRoad`)

Bottom bar becomes 4 equal-width items: Buscar · Líneas · Favoritos · Aportar.

Screen "Aportar" (`ContributeScreen`), scrollable:

1. **Datos en uso** — card list of providers: "Datos oficiales · v15 · 133 líneas" (main) and each local
   provider ("Mis rutas · 2 rutas · creado 30 oct"). Tap selects (radio). Local rows have a ⋯ menu:
   Renombrar, Exportar, Eliminar (confirmation dialog: "Se borran N rutas de este teléfono. Volvés a los
   datos oficiales."). Import button "Importar archivo".
2. **Mis rutas** (active local provider only) — one row per overlay line: chip, name, badge
   *Nueva*/*Actualizada*, status (*Sin enviar* / *Enviada* / *Aceptada* / *Rechazada* + note). Tap → editor.
   Swipe-free: trailing ⋯ → Editar, Quitar.
3. **Actions** — primary "Agregar ruta nueva", secondary "Actualizar una ruta" (opens the line picker:
   search by number; then choose ida/vuelta/branch) and "Enviar aportes" (sheet: tick routes not yet sent,
   comment, quota dots "2 de 3 este mes", send; offline → the send fails with a message and the routes stay on the phone, there is no queue for contributions).
4. Note: how review works, monthly limit, data stays on the phone unless sent.

### 4.2 Route editor (`RouteEditorScreen`, full screen map)

- Map with the current polyline (line colour, arrows), vertex dots, START/END pins, other lines faded
  off for context (toggle "Mostrar otras líneas").
- Tool bar: **Agregar** (tap appends a point), **Mover** (tap moves nearest vertex), **Insertar** (tap
  inserts on the nearest segment), **Borrar punto** (tap removes nearest vertex), Deshacer, Limpiar.
  Current length and point count shown ("4,2 km · 38 puntos").
- Bottom sheet "Datos de la ruta": número (display), nombre, tipo (Micro / Trufi / Otro), color (12
  toned swatches), sentido (Ida / Vuelta), desde (origin), hacia (headsign).
- Save: validates ≥ 2 points, display and headsign not blank; if main provider is active, asks for a
  provider name and creates it; recomputes transfers; activates the provider; returns to Aportar.
- Update flow pre-fills everything from the chosen variant and stores `before`.

### 4.3 Report form

Under the type chips: a card "¿Conocés bien el recorrido? Corregilo vos" → goes to the Aportar tab
(for `route`/`missing` it pre-selects update/new). Text warns: it takes a few minutes, the proposal is
reviewed together with the line data of the provider you have selected, so reviewers can take into
account the changes the official data may have had since.

### 4.4 Favourite places

`Favorites.places: List<FavoritePlace(id, label, icon, color, place)>` replaces `home`/`work` (both
migrate as fixed first entries that can be edited but keep their ids). Add/edit sheet: name, icon
(Casa, Trabajo, Estudio, Gimnasio, Salud, Compras, Comida, Familia, Iglesia, Parque), colour (8
toned swatches), address via the existing picker. Remove with confirmation. Home chips and the
Favoritos list render icon + colour.

### 4.5 Notifications setting

Row text: "Avisarme cuando se apliquen mis reportes y aportes". Turning it on requests
`POST_NOTIFICATIONS` (Android 13+); if denied the switch stays off and a note offers "Abrir ajustes".
Turning it off sets the preference and no notification is posted. When "Buscar actualización"
installs a newer dataset and the setting is on and granted, the app posts the existing "Piyo se
actualizó con tu reporte" notification (channel from strings) that opens `piyo://gracias`.

## 5. Privacy

Nothing new leaves the phone unless the user taps "Enviar aportes"; a contribution carries routes, the
provider's *name*, comment, app and dataset versions, and the install id header (hashed server-side,
as for reports). Provider files never contain the install id.

## 6. Files touched (plan)

Backend: `model/model.go` (+Contribution), `store/store.go` (+7 methods), `store/memory*.go`,
`store/sqlite*.go`, `store/postgres/postgres.go` + migration, `config/config_v2.go`
(`MAX_CONTRIBUTIONS_PER_MONTH`), `httpapi/handlers_contributions.go`, `server.go` (routes, per-route
body cap), tests, `backend/README.md`.

App: `domain/model/Models.kt`, `domain/model/Provider.kt`, `data/lines/DatasetWriter.kt`,
`data/providers/ProvidersRepository.kt`, `data/lines/EffectiveLinesRepository.kt`,
`data/feedback/ContributionsRepository.kt` (+Http), `data/favorites/FavoritesRepository.kt`,
`di/*`, `ui/components/BottomBar.kt` (4 tabs), `ui/screens/ContributeScreen.kt`,
`ui/screens/RouteEditorScreen.kt`, `ui/screens/PlaceEditorSheet.kt`, `ReportScreen.kt`,
`FavoritesScreen.kt`, `HomeScreen.kt`, `SearchScreen.kt` (purpose `place:<id>`), `DataScreen.kt`
(notifications), `nav/*`, `AndroidManifest.xml` (FileProvider), `res/xml/file_paths.xml`, strings,
unit tests (`DatasetWriterTest`, `ProviderMergeTest`, `TransfersTest`).

Docs: `DATA_MODEL.md` §9 (provider file + contributions), `PRIVACY.md`, `SETUP_AND_RUN.md`, `PROGRESS.md`,
`EVIDENCE.md` §12.

## Update 2026-10-30 (morning)

- The tab is called **Datos**, not Aportar: it now opens with the official data card and "Buscar actualización" (the sync with the backend, shared with *Datos y conexión* through `OfficialDataCard`), then the data in use, the provider's routes and the editing actions.
- **Invertir el sentido de los recorridos** (renamed 2026-10-31; it was a label swap first): in the route picker (after choosing a line) and in a provider route's ⋯ menu. Every variant of the line is reversed point by point (`Variant.reversed()`: points, streets, stops reversed, origin <-> destination; label and id unchanged), then the line is saved into the active provider as an `update` (or `add`), creating the provider when official data is active. The backend receives it as a normal contribution with `before` polylines. The editor has the same action for a single trace (**Invertir**).
- Editor **Mover** is a drag: press a vertex (hit radius ≈ 24 dp) and drag it; the map stops panning while dragging (`MapDrag` on `MapProvider.Map`). A tap far from any vertex does nothing in this mode.
- The "Otras líneas" overlay was removed: drawing all 294 variants on top of the editor strained low-end phones.

## Update 2026-11-04 — one row per route, withdrawal, releases from contributions

Supersedes the parts of §3.3, §4.1 and §4.3 it touches.

### One route per row

`POST /v1/contributions` still accepts up to 10 routes, but stores **one row per route**. The rows of a
request share `batch_id`; each has its own id, status, review note, notification and publish state, so a
reviewer can accept one route and reject another. Before, two routes sent together became one row with
`route_ids: [a, b]` and could only be reviewed together. The monthly quota counts **batches**
(`COUNT(DISTINCT batch_id)`; rows from before the change have `batch_id = ''` and count on their own), so
the split costs the user nothing. Response: `{"id": <first>, "batch_id", "contributions": [{"id","route_id"}], "remaining", "resets_at"}`;
`id` stays for older apps. Each stored payload is `{base_dataset_version, lines:[line], variants:[its variants], edits:{line: edit}}`.

New columns (PostgreSQL migration `004_contribution_batches.sql`, SQLite adds them on open):
`batch_id TEXT NOT NULL DEFAULT ''`, `published_version INTEGER NOT NULL DEFAULT 0`, `published_at`.

### Withdrawal ("Quitar de mis rutas")

`DELETE /v1/contributions/{id}` (device): 204 deletes a **pending** row of this device; 404 when it does not
exist or belongs to another device; 409 `not_pending` once reviewed. Reviewed rows stay: they are the
review history, the auto-block evidence and, when accepted, the source of a release. The per-IP rate
limit applies; a device can only delete its own rows, so the table cannot grow through this route.

App: "Quitar" became "Quitar de mis rutas" with a confirmation that says what happens on the phone (the
route disappears / the official route is shown again) and on the server, by state: pending → "Quitar y
retirar envío" (DELETE first; without connection nothing is removed, so phone and server never disagree);
accepted → it still arrives in the next update; published → official data does not change; rejected →
phone only. Deleting a provider withdraws its pending routes the same way. Routes sent before the split
that share a server id with another route are removed locally only (withdrawing would drop the other).

### Wrong routes are contributions, not reports

The report form no longer offers "Recorrido incorrecto": a report only carries a point and a comment, a
contribution carries the geometry a reviewer needs. The report screen opens with a card that goes straight
to the route editor for that line ("Corregir el recorrido"), or to a new route when the problem is a
missing line; the line detail has "Corregir recorrido" next to "Reportar otro problema". Reports keep
stop / gone / missing / other. The API still accepts `type: "route"` from older apps.

### Releases from contributions (route data versioning)

Releases are immutable files (`releases/vN`, `dataset.Write`) built offline and deployed with the API
(Cloud Run bakes `releases/` into the image), so a release is built locally and then published:

1. **Review** in the admin API (`GET /v1/admin/contributions?status=pending`, `…/{id}`, `…/{id}/accept|reject`).
2. **Build** `piyo-release` (`cmd/piyo-release`): reads the latest `releases/vN`, fetches accepted and
   unpublished contributions (`GET /v1/admin/contributions?status=accepted&published=false`, then each
   payload) or reads them from a file, merges them (`dataset.MergeContributions`), validates
   (`dataset.Validate`) and writes `releases/v(N+1)` with `manifest.json` → `changes`
   `{base_version, contribution_ids, lines_added, lines_updated, notes}`. It refuses to overwrite a version.
   - `add` creates a line; `update` replaces every variant of the line (the app always sends whole lines).
   - Conflicts are **skipped** and printed, unless `-force`: an update whose `before` geometry no longer
     matches the base (the line changed in a release after the user made the change), an add that reuses an
     existing line id, a variant id owned by another line, a second contribution to a line already changed
     in this release, a point outside `METRO_BOUNDS`. Lines are never deleted by a merge.
   - Transfers and bbox are recomputed; stops no longer used by any variant are dropped; a replaced line
     loses its upstream `source_url`.
3. **Deploy** the API with the new folder (Cloud Run: `deploy/cloudrun/deploy.sh`; VPS: copy to
   `deploy/data/releases`). `/v1/manifest` now points at v(N+1); apps pick it up with "Buscar actualización".
4. **Publish** `POST /v1/admin/datasets/{N+1}/publish` (admin): checks that this server serves that version
   and that it is the latest, marks the contributions in `changes.contribution_ids` as published
   (`published_version`, only accepted and not yet published ones, so a repeated call marks and notifies
   nobody again), sends one data-only push per device `{"type":"contribution_published","version","count"}`
   and announces `{"type":"dataset","version"}` to the `dataset` topic. Body (optional):
   `{"notify":true,"announce":true}`. The response lists `published_contributions`,
   `skipped_contributions`, `devices_notified`, `announced` and `pending_reports_on_changed_lines`
   (pending reports made against older data about lines this release changed: check whether it fixes them).

Across versions: a contribution keeps its `base_dataset_version` and per-line `before`, which is how a
later merge detects that the official line moved on; `GET /v1/contributions/mine` returns
`published_version`, and the app shows "En los datos oficiales · vN" (refreshed on resume and when an
update installs). Reports keep their `data_version`; publishing lists the pending ones on changed lines.
Release notes: the merged geometry is community data on top of the route data provider's; check the
licence terms in `docs/DATA_SOURCES.md` before publishing a merged release publicly.
