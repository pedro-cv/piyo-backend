# Dependencies

All libraries are free, open source and actively maintained (checked by reputation on 2026-10-28; versions pinned from memory and **not resolved against Maven tonight**, see PROGRESS.md).

## Android app (`app/gradle/libs.versions.toml`)

| Library | Version | Licence | Why |
|---|---|---|---|
| Android Gradle Plugin | 8.7.3 | Apache 2.0 | Build system; 8.7 matches Android Studio Ladybug+. |
| Kotlin + Compose compiler plugin | 2.1.0 | Apache 2.0 | Language; the Compose compiler ships with Kotlin since 2.0. |
| kotlinx.serialization plugin + json | 2.1.0 / 1.7.3 | Apache 2.0 | Parse the line bundle, manifest, API bodies and type-safe nav routes. Lighter than Moshi/Gson, no reflection. |
| androidx.core:core-ktx | 1.15.0 | Apache 2.0 | Permission check helpers. |
| androidx.activity:activity-compose | 1.9.3 | Apache 2.0 | `setContent`, edge-to-edge. |
| Compose BOM | 2024.12.01 | Apache 2.0 | Pins ui, foundation, material3, icons to compatible versions. |
| Compose Material 3 | (BOM) | Apache 2.0 | `ModalBottomSheet`, `Switch`, `LinearProgressIndicator`; everything else is custom to match the canvas exactly. |
| material-icons-extended | (BOM) | Apache 2.0 | Material **Rounded** icons named in docs/02 (search, home, work, star, directions_bus, directions_walk, map, my_location, place, arrow_back, swap_vert, tune, wifi_off, cloud_download, more_vert, language, info, flag, add, check_circle, layers…). |
| ui-text-google-fonts | 1.7.6 | Apache 2.0 | Loads **Sora** through downloadable fonts (OFL), with system-sans fallback. Avoids bundling font files tonight. |
| navigation-compose | 2.8.5 | Apache 2.0 | Type-safe routes with `@Serializable` destinations. |
| lifecycle-runtime-compose / viewmodel-compose | 2.8.7 | Apache 2.0 | `collectAsStateWithLifecycle`. |
| datastore-preferences | 1.1.1 | Apache 2.0 | Favorites, settings, install id, offline outbox. No SQLite needed yet (docs/03 mentions SQLite; DataStore is enough for the first iteration; the bundle itself is a JSON file in `filesDir`). |
| kotlinx-coroutines-android | 1.9.0 | Apache 2.0 | Async I/O. |
| OkHttp | 4.12.0 | Apache 2.0 | HTTP client for the backend, manifest download and Photon. Retrofit was skipped: four endpoints do not justify it. |
| Firebase BoM + `firebase-messaging` | 33.7.0 (BoM) | Apache 2.0 | Push notifications only (review results, new data release), chosen by the maintainer on 2026-11-01 as the one exception to "no SDK that phones home" (PRIVACY.md §Push). Initialised from `keys.properties`, auto-init off, no Analytics/Crashlytics/Installations use beyond what FCM needs; blank keys = inert. Pulls Play services base/tasks/cloud-messaging (~1–2 MB before R8). BoM pinned to Dec 2024 to match AGP 8.7 / Kotlin 2.1; newer BoMs (35.x) need a toolchain bump. |
| MapLibre Native Android SDK (`org.maplibre.gl:android-sdk`) | 11.8.0 | BSD-2 | Open-source vector map renderer (the only map; no mock map since iteration 3); renders the city map bundled in the APK from a PMTiles file (`pmtiles://file://`, supported since 11.7/11.8; MAP_HOSTING.md §0). |
| Bundled city map (`app/src/main/assets/map/`, built by `app/scripts/bundle-map.py`) | built 2026-10-28 | data ODbL 1.0 (© OpenStreetMap contributors); styles BSD-3 (Protomaps basemaps); fonts OFL 1.1 (Noto Sans); see `assets/map/LICENSES.txt` | Offline map of the metro area in light and dark with the same layers: 11 MB PMTiles + ~2 MB fonts/sprites (Latin glyph ranges only). Adds about 12 MB to the APK (release APK 46.6 → 58.5 MB measured). No runtime dependency, no network. |

Not used on purpose: Google Play Services APIs beyond what FCM pulls in (location uses the framework `LocationManager`), Firebase SDKs other than Messaging, Hilt/Koin (one hand-written container is smaller), Room (not needed yet), Coil (no images), any analytics or crash SDK (docs/03 §5).

## Backend (`backend/go.mod`)

| Module | Version | Licence | Why |
|---|---|---|---|
| Go standard library | 1.25 | BSD-3 | `net/http` with method/path patterns, `log/slog`, `embed`, `crypto/sha256`, `encoding/csv`. No web framework needed. |
| modernc.org/sqlite | 1.39.0 | BSD-3 | Pure-Go SQLite (no cgo) for `STORE=sqlite` (single-binary deployments without PostgreSQL). |
| github.com/jackc/pgx/v5 (+ pgxpool) | 5.11.0 | MIT | PostgreSQL driver for `STORE=postgres`; native pool, JSONB and arrays map directly. |

## Deploy stack (`deploy/`)

| Component | Version | Licence | Why |
|---|---|---|---|
| postgis/postgis Docker image | 16-3.5 | PostgreSQL / GPL-2 (PostGIS) | Database with spatial types and GIST indexes (DATABASE_CHOICE.md). |
| ghcr.io/maplibre/martin | v0.15.0 | MIT / Apache-2.0 | Tile server for the PMTiles file (MAP_HOSTING.md). |
| caddy | 2.9 | Apache-2.0 | Reverse proxy, static files, automatic HTTPS. |
| protomaps/go-pmtiles | latest (CLI, one-shot) | BSD-3 | `pmtiles extract` of the city from the Protomaps daily build. |
| @protomaps/basemaps (npm) | ^5 | BSD-3 | Generates the MapLibre style layers (`deploy/tiles/make-style.mjs`). |
| protomaps/basemaps-assets | main | OFL / BSD-3 | Glyph PBFs and sprites served as static files. |
| gcr.io/distroless/static-debian12 | nonroot | Apache-2.0 | Minimal runtime image for the Go binaries (`backend/Dockerfile`). |

## Tools

| Tool | Why |
|---|---|
| `backend/tools/check_trips.py` (Python 3, stdlib only) | Independent re-implementation of the planner rules used to cross-check the app's results (EVIDENCE.md). |

## Services (no SDK, no key)

| Service | Licence / terms | Why |
|---|---|---|
| OpenFreeMap tiles + "liberty" style | OSM data ODbL; service free, no key, no published limits | Optional online map when `MAP_STYLE_URL` points to it; the default is the self-hosted style served by the API. |
| Photon (komoot) | OSM data ODbL; public instance fair use | Default text search with autocomplete. |

## Tooling

| Tool | Why |
|---|---|
| Gradle wrapper 8.11.1 (`app/gradlew`) | Reproducible builds; jar and scripts fetched from the Gradle GitHub tag v8.11.1. |
| gofmt, go vet | Formatting and static checks; both pass on `backend/`. |
