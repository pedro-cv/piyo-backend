# Piyo · Setup and run guide

Written 2026-10-28, updated the same day for iteration 2 (full stack) and iteration 3 (one build, no flavors). What is verified is listed in `PROGRESS.md`; follow this guide in order and the first section that fails tells you what to fix.

Since iteration 3 there is **one build** of the app. There are no `dev`/`prod` flavors and no mock data: every build loads the bundled line dataset, checks the API for newer releases, renders the real map (MapLibre, style served by the API) and searches places with Photon. Which server it talks to is configuration in `app/keys.properties` (section 2).

Contents
1. Prerequisites
2. Build and run the app (one build; configure the API/map URL)
2b. The whole stack from zero: PostgreSQL + API + map server + Caddy with Docker Compose, real data import
2c. Fast iteration loop: Android Studio Live Edit (hot reload) and `app\dev.ps1`
3. The backend alone in DEV_MODE (no Docker)
4. The app against the local backend
5. Provider configuration (self-hosted map, Photon, Firebase, Google Maps if chosen, GitHub)
6. Cost warnings: which steps can create charges
7. Troubleshooting
8. Configuration reference (keys.properties, BuildConfig)

---

## 1. Prerequisites and versions

| Tool | Version to use | Where it is on this PC (checked 2026-10-28) |
|---|---|---|
| JDK | 17 (any 17.x LTS) | `java -version` → 17.0.20.1. Android Studio also ships one at `C:\Program Files\Android\Android Studio\jbr`. |
| Android Studio | Ladybug 2024.2 or newer (any version bundling AGP 8.7+) | installed |
| Android SDK | compileSdk 35 (`platforms;android-35`), build-tools 35.0.0, platform-tools | `%LOCALAPPDATA%\Android\Sdk` has build-tools 35/36/37 and platforms android-36, android-36.1. **android-35 is missing**: either install it (SDK Manager → SDK Platforms → Android 15 (API 35)) or bump `compileSdk`/`targetSdk` to 36 in `app/app/build.gradle.kts`. |
| Gradle | 8.11.1 via the wrapper (already in `app/gradle/wrapper`) | downloaded on first `gradlew` run (~130 MB) |
| Kotlin | 2.1.0, AGP 8.7.3 (pinned in `app/gradle/libs.versions.toml`) | downloaded by Gradle |
| Go | 1.25.x | `go version` → 1.25.6 |
| Device or emulator | Android 7.0+ (API 24). Emulator: Pixel 6, API 34 or 35, x86_64, Google APIs image (needed for downloadable fonts; the app works without it). Real device: the Xiaomi from the audit works with USB debugging. | ADB in `%LOCALAPPDATA%\Android\Sdk\platform-tools` |
| curl | any | Windows 10+ ships one |

No accounts of any kind are needed for sections 2 to 4.

## 2. Build and run the app

One build for every environment. The APK bundles the real line dataset v14 as `assets/dataset-seed.json` (133 lines, 294 ida/vuelta variants), so lines and routing work offline from the first launch; the map needs the API (or another style URL) and a network. JVM unit tests (parser on the seed, planner cross-check): `gradlew :app:testDebugUnitTest`.

1. Configure where the app looks for the API and the map: copy `app/keys.properties.example` to `app/keys.properties` (git-ignored).
   - Emulator with the stack of section 2b on this PC: `PIYO_API_BASE_URL=http://10.0.2.2` (Caddy on port 80) or `http://10.0.2.2:8080` for the bare backend of section 3.
   - Phone on the same Wi-Fi: `PIYO_API_BASE_URL=http://<PC-IPv4>` (`ipconfig`).
   - Leave `MAP_STYLE_URL=` empty to use the style served by the API (`/v1/map/style.json`); set it to `https://tiles.openfreemap.org/styles/liberty` to use OpenFreeMap without the stack.
2. Open Android Studio → **Open** → select the folder `piyo\app` (the one containing `settings.gradle.kts`), not `piyo\app\app`.
3. Wait for the Gradle sync. First sync downloads Gradle 8.11.1 and ~400 MB of dependencies.
   - If sync complains about `gradle-wrapper.jar`, see Troubleshooting (T1).
   - If it complains about `android-35`, see Troubleshooting (T2).
4. Start an emulator (Device Manager → ▶) or plug in the phone, then press **Run ▶** (the only variant is `debug`/`release`).

Command line alternative, from `piyo\app`:

```powershell
.\gradlew.bat :app:installDebug
adb shell am start -n bo.piyo.app/bo.piyo.app.MainActivity
```

What you should see: the splash (≤ 1.5 s), the first-load screen ("62 líneas incluidas · versión 13", "Empezar a usar Piyo"), then Inicio with the "Pasan cerca" chips around C. Florida 461, and the search. Type "ventura" → pick Ventura Mall → Resultados shows three itineraries (51→45 39 min, 58→45 39 min, 60→45 41 min; cross-checked in EVIDENCE.md §4) → tap one → Ruta with the drawn map → **Empezar** → **Terminar viaje** → the NPS sheet appears (in dev it always appears, so you can test it).

## 2c. Fast iteration loop (hot reload)

What is installed on this PC and verified on 2026-10-28: Android Studio 2025.3.2 (`C:\Program Files\Android\Android Studio`), JDK 17, the Android SDK with platform 35, the Pixel 9 AVD, and the Xiaomi (Android 16, API 36) over USB. Nothing else is needed.

**Hot reload = Android Studio Live Edit** (Compose only; it patches composables in the running app without reinstalling):

1. Open the folder `piyopp` in Android Studio and let Gradle sync.
2. Settings → Editor → Live Edit: tick **Enable Live Edit**, choose **Push automatically** (changes apply as you type; "Push manually" uses Ctrl+'). Live Edit needs a *debug* build started with **Run ▶ (not Debug)** on a device with API 30+; both the phone and the Pixel 9 qualify.
3. Edit any `@Composable` body (text, colours, layout, lambdas inside composables) and watch the phone update in a second or two.
4. Changes outside composables (new functions, data classes, repositories, `AppModule`, resources, Gradle files) are not hot-reloadable: use **Apply Code Changes** (Ctrl+F10) for method bodies or **Run ▶** again for the rest. A full incremental install takes about 10 s with the Gradle settings below.

**Command line loop** (what Claude uses; no hot reload, but one command):

```powershell
cd piyopp
.\dev.ps1                      # incremental build → install → launch → app logcat   (≈ 8–12 s after the first run)
.\dev.ps1 -Test               # run the JVM unit tests first
.\dev.ps1 -Serial emulator-5554
.\dev.ps1 -Clean              # after editing build.gradle.kts / gradle.properties
```

**Judge performance on a release build.** The debug build runs Compose with its debug checks and without R8; on the Xiaomi a fling through the Líneas list renders at ~40 ms per frame in debug and ~12 ms in release (`.\dev.ps1 -Release`, which installs `:app:installRelease` signed with the debug keystore; same application id, so it replaces the debug install). Smoothness complaints should be checked on the release build before touching code.

Measured on this PC after the `gradle.properties` tuning (daemon, parallel, build cache, configuration cache, 4 GB heap): cold build with warm caches 21 s, no-change install 8 s, one Kotlin file changed 12 s.

## 2b. The whole stack from zero (Docker Compose, the "local VPS")

Needs Docker Desktop (Compose v2), Go 1.25, Node 18+ (only to generate the map style) and Git Bash. Ports: 80 (Caddy) and 55432 (PostgreSQL, localhost only). Verified on this PC on 2026-10-28 (EVIDENCE.md).

```sh
cd piyo/deploy
cp .env.example .env            # defaults are fine locally
bash tiles/build-tiles.sh       # ~1 min: Santa Cruz vector tiles (11 MB from Protomaps), fonts, sprites, 2 styles
docker compose up -d --build    # db (PostGIS), api (Go, built from ../backend), martin (tiles), caddy (:80)
docker compose ps               # all four "Up"; db "healthy"
curl http://localhost/v1/health # {"status":"ok","store":"postgres","dataset_source":"embedded",...}
```

Import the real transit data and publish a release (writes `deploy/data/releases/vN` and loads PostGIS). The source is the named-routes provider file (docs/DATA_SOURCES.md); the older OSM file `data/lines.json` still works (DATA_MODEL.md §8):

```sh
cd ../backend
go test ./...                     # dataset builder tests (both source layouts, compact transfers, release writer)
go run ./cmd/piyo-import -source ../../santa-cruz-transit-data/data-<provider>/lines.json -out ../deploy/data/releases -db "postgres://piyo:<POSTGRES_PASSWORD from .env>@localhost:55432/piyo?sslmode=disable"
docker compose -f ../deploy/docker-compose.yml up -d --build api   # only after changing Go code: the container has its own binary
curl http://localhost/v1/health   # now "dataset_source":"releases","dataset_version":14
curl http://localhost/v1/manifest # package_format 2; counts: 133 lines, 294 variants, 0 stops, 103889 transfers
python tools/check_trips.py ../deploy/data/releases/v14/dataset-v14.json.gz   # independent planner check
```

To refresh the APK seed after a new release: gunzip `deploy/data/releases/vN/dataset-vN.json.gz` to `app/app/src/main/assets/dataset-seed.json`.

Useful URLs (all through Caddy on port 80): `/v1/manifest`, `/v1/datasets`, `/v1/datasets/14/dataset-v14.json.gz`, `/v1/map`, `/v1/map/style.json?theme=dark`, `/tiles/santa-cruz` (TileJSON), `/tiles/santa-cruz/13/2658/4507` (a tile), `/tiles/santa-cruz.pmtiles` (offline file), `/assets/sprites/v4/light.json`. Suggestions: `GET /v1/suggestions/quota`, `POST /v1/suggestions` (header `X-Install-Id`), admin `GET /v1/admin/suggestions?status=pending`, `POST /v1/admin/suggestions/{id}/accept|reject` (header `X-Admin-Token`, body `{"note":"..."}`), `GET /v1/admin/datasets`. Full curl transcript in EVIDENCE.md §2.

App against this stack, on a phone in the same Wi-Fi: find the PC's IPv4 (`ipconfig`), write `app/keys.properties` with `PIYO_API_BASE_URL=http://<PC-IP>` (no port: Caddy is on 80) and leave `MAP_STYLE_URL=` empty so the app uses the self-hosted style from `/v1/map/style.json`; then Run. On the emulator use `http://10.0.2.2` instead. Verified: first load shows the bundled version, the manifest is checked against the API, the map screens render the self-hosted dark style with Spanish labels.

Stop everything: `docker compose down` (data stays in `deploy/data`); wipe: `docker compose down -v` and delete `deploy/data/postgres`.

## 3. Run the backend locally in DEV_MODE

DEV_MODE uses an in-memory store with seed data. No database, no external service.

```powershell
cd piyo\backend
copy .env.example .env          # optional: all variables have dev defaults
go run .\cmd\piyo-api           # listens on http://localhost:8080
```

Environment variables (all in `backend/.env.example` with comments): `PORT`, `DEV_MODE`, `ADMIN_TOKEN` (dev default `dev-admin-token`), `HASH_SALT`, `SQLITE_PATH`, `MAX_REPORTS_PER_DAY=3`, `REJECTS_TO_BLOCK=3`, `REJECT_WINDOW_DAYS=90`, `BLOCK_DAYS=30`, `METRO_BOUNDS`, `IP_RATE_PER_MINUTE=60`, `CORS_ALLOWED_ORIGINS`, `MANIFEST_DATA_URL`, `MANIFEST_DATA_VERSION`, `FARE_BS`, `MIN_APP_VERSION`. Go does not read `.env` by itself; on PowerShell set them with `$env:PORT="8081"` before `go run`, or use the defaults.

One curl per endpoint (PowerShell: use `curl.exe`, not the `curl` alias):

```powershell
# Health
curl.exe http://localhost:8080/v1/health

# Manifest (data version, bundle URL, sha256)
curl.exe http://localhost:8080/v1/manifest

# Data bundle (embedded sample, 3 lines)
curl.exe http://localhost:8080/v1/data/lines-v12.json

# Report quota for a device (install id is any 8-64 char token the app generates)
curl.exe -H "X-Install-Id: demo-device-0001" http://localhost:8080/v1/reports/quota

# Send a report
curl.exe -X POST http://localhost:8080/v1/reports -H "Content-Type: application/json" -H "X-Install-Id: demo-device-0001" -d "{\"type\":\"stop\",\"lat\":-17.7834,\"lng\":-63.1821,\"line_id\":\"76\",\"comment\":\"La parada esta una cuadra mas alla\",\"app_version\":\"0.1.0\",\"data_version\":12}"

# A blocked device (seeded) gets 403
curl.exe -X POST http://localhost:8080/v1/reports -H "Content-Type: application/json" -H "X-Install-Id: blocked-demo-device" -d "{\"type\":\"stop\",\"lat\":-17.78,\"lng\":-63.18,\"app_version\":\"0.1.0\",\"data_version\":12}"

# Send NPS
curl.exe -X POST http://localhost:8080/v1/nps -H "Content-Type: application/json" -H "X-Install-Id: demo-device-0001" -d "{\"score\":9,\"reasons\":[\"me_sirvio\"],\"comment\":\"Muy util\",\"app_version\":\"0.1.0\",\"data_version\":12,\"device_class\":\"android-34\"}"

# Admin: list pending reports
curl.exe -H "X-Admin-Token: dev-admin-token" "http://localhost:8080/v1/admin/reports?status=pending"

# Admin: accept / reject (replace r_xxx with an id from the list; 3 rejects within 90 days block the device 30 days)
curl.exe -X POST -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/reports/r_xxx/accept
curl.exe -X POST -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/reports/r_xxx/reject

# Admin: blocks
curl.exe -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/blocks
curl.exe -X POST -H "X-Admin-Token: dev-admin-token" -H "Content-Type: application/json" http://localhost:8080/v1/admin/blocks -d "{\"install_id\":\"demo-device-0002\",\"days\":30,\"reason\":\"spam\"}"
curl.exe -X DELETE -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/blocks/<device_hash>

# Admin: exports
curl.exe -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/nps.csv
curl.exe -H "X-Admin-Token: dev-admin-token" http://localhost:8080/v1/admin/reports.json
```

Production mode (later, not tonight): `DEV_MODE=false`, set `ADMIN_TOKEN` and `HASH_SALT` to long random strings, data goes to SQLite at `SQLITE_PATH`. The full contract is in `backend/README.md`.

## 4. Run the app against the local backend

The app always talks to a real backend and uses the real map. Without `keys.properties` the base URL is `http://10.0.2.2:8080`, which is how the Android **emulator** reaches `localhost` of your PC.

1. Start the backend (section 3) or the whole stack (section 2b, then use port 80 in the URL so the self-hosted map style is available).
2. Emulator: Run. No file edits are needed for the bare backend; with the stack set `PIYO_API_BASE_URL=http://10.0.2.2`.
3. Physical phone on the same Wi-Fi: find your PC's IP (`ipconfig` → IPv4), create `app/keys.properties` from `app/keys.properties.example` with `PIYO_API_BASE_URL=http://192.168.x.y` (port 80 with the stack, 8080 with the bare backend), allow the port in Windows Firewall if asked, then Run. (`keys.properties` is git-ignored.)
4. Test: Inicio → Menú (⋮) → Datos y conexión → **Buscar actualización**. With release v14 published and the v14 seed you should see "Tenés la última versión"; publish a v15 with `piyo-import` to see the download path (sha256 check, swap).
5. Test a report: Líneas → 76 → Reportar error → Enviar reporte → "Reporte #r_… enviado". Check with the admin curl.

Plain HTTP is allowed by `manifestPlaceholders["usesCleartextTraffic"] = "true"` in `app/app/build.gradle.kts`; set it to `false` once the API has HTTPS.

## 5. Provider configuration, step by step

What the project uses and what each thing is for:

| Service | Used for | Key | Cost | Needed tonight? |
|---|---|---|---|---|
| OpenFreeMap (tiles.openfreemap.org) | Online map tiles in the prod flavor, OpenStreetMap data, MapLibre style "liberty" | none | $0, no limits published, donations voluntary | No config |
| Photon (photon.komoot.io) | Text search with autocomplete, prod flavor | none | $0 fair use, no SLA | No config |
| MapLibre Native (library) | Renders the map; caches fetched tiles so visited areas work offline (no whole-city download) | none | $0 | — |
| Firebase Hosting (Spark) | Hosting `manifest.json` and `lines-vN.json.gz` (docs/03 §3). The Go backend can also serve them; Hosting is the CDN option. | project | $0 on Spark | Optional |
| Firebase Cloud Messaging | Thank-you push per data release (docs/03 §8) | `google-services.json` | $0 | Not implemented in the app yet |
| Google Maps Platform | Only if you choose it over the OSM stack (docs/implementation/MAP_PROVIDERS.md) | API key + billing | from $343/month at 1k MAU (see MAP_PROVIDERS) | No |
| GitHub | Public issues for reports (docs/03 §8), monthly NPS CSV | token on the server | $0 | Not implemented yet (JSON export exists) |

### 5.1 Self-hosted map (default since iteration 2) and Photon

- The prod app loads `API_BASE_URL + /v1/map/style.json`; tiles, glyphs and sprites come from your own server (MAP_HOSTING.md). Nothing to create at any provider; run `deploy/tiles/build-tiles.sh` on the server.
- `MAP_STYLE_URL` in `app/keys.properties` can point at OpenFreeMap (`https://tiles.openfreemap.org/styles/liberty`) as a fallback while the server is down.
- Geocoder URL `https://photon.komoot.io/api/` is the prod default in `app/app/build.gradle.kts`, overridable in `app/keys.properties`.
- Attribution "© OpenStreetMap contributors · OpenFreeMap" is shown in Acerca de and by MapLibre's attribution control. Keep it.
- Photon policy: do not hammer it. The app debounces 300 ms and sends a `User-Agent` with the app name. If the project grows past ~10k MAU, self-host Photon (a ~$5–10/month VPS) or switch to Stadia Maps; see MAP_PROVIDERS.md §5.
- Never point MapLibre at `tile.openstreetmap.org`; the OSM tile policy forbids app use.

### 5.2 Firebase (Hosting + FCM), when you decide to use it

Manual steps in the console (https://console.firebase.google.com):
1. **Create project** → name `piyo` → disable Google Analytics (not needed, avoids a privacy disclosure). Plan: **Spark** (free). Do not upgrade to Blaze.
2. **Add Android app**: package name `bo.piyo.app` (and a second app `bo.piyo.app.dev` if you want the dev flavor registered; not required because dev never contacts Firebase). SHA-1 is only needed for Auth/Dynamic Links, which Piyo does not use.
3. Download **google-services.json** and put it at `app/app/google-services.json` (git-ignored). Then add to `app/build.gradle.kts` plugins `id("com.google.gms.google-services") version "4.4.2" apply false`, apply it in `app/app/build.gradle.kts`, and add `com.google.firebase:firebase-messaging` via the Firebase BOM. This is deliberately not wired tonight so the project builds without the file.
4. Products to enable: **Hosting** (for the data files) and **Cloud Messaging** (for the per-release push). Nothing else: no Firestore, no Auth, no Analytics, no Crashlytics (docs/03 §5).
5. Hosting: `npm i -g firebase-tools`, `firebase login`, `firebase init hosting` in a folder like `piyo/hosting` (public dir with `manifest.json` and `lines-v12.json.gz`), then `firebase deploy --only hosting`. Set `MANIFEST_DATA_URL` to the Hosting URL. Cache headers: files are named by version so `max-age=31536000, immutable`; manifest `max-age=3600`.
6. FCM server side: the push is sent per data release by the pipeline/backend with the Firebase Admin SDK service-account JSON. Keep that JSON only on the server (env var or secret store), never in the repo.

### 5.3 Google Maps / Places (only if you choose it)

The default is the OSM stack and no Google key exists in the project. If you choose Google after reading MAP_PROVIDERS.md:
1. https://console.cloud.google.com → create project `piyo` → **Billing** → link a billing account (required even for free usage). First set a **budget** (Billing → Budgets & alerts → $5/month, email at 50/90/100 %).
2. APIs & Services → Enable **Maps SDK for Android** and **Places API (New)**.
3. Credentials → Create API key → **Restrict**: Application restriction = Android apps, add package `bo.piyo.app` + release SHA-1 (and `bo.piyo.app.dev` + debug SHA-1 if you want it in dev); API restriction = the two APIs above.
4. Quotas: APIs & Services → Quotas → set per-day caps on Dynamic Maps loads and Autocomplete requests below the free allowance (10,000 / 10,000 per month; see MAP_PROVIDERS.md for the current numbers).
5. Key placement: `GOOGLE_MAPS_API_KEY=...` in `app/keys.properties`; read it in `build.gradle.kts` into a `manifestPlaceholders["mapsApiKey"]` and a `<meta-data android:name="com.google.android.geo.API_KEY">` entry. Implement `GoogleMapProvider : MapProvider` in `app/app/src/prod/java/bo/piyo/app/map/google/` with `com.google.maps.android:maps-compose` and `com.google.android.libraries.places:places`. Note the Places ToS: results must be shown on a Google map.

### 5.4 GitHub (reports as issues, NPS CSV)

Not wired yet. When you do: create a fine-grained personal access token limited to the data repo with *Issues: write*; store it only as `GITHUB_TOKEN` in the backend env; implement `GitHubIssueFiler` in `backend/internal/export` (the interface `IssueFiler` and a logging implementation exist).

## 6. Cost warning: steps that can create charges

| Step | Charge risk | Protect yourself first |
|---|---|---|
| Firebase project on **Spark** | none; Spark has no card | Stay on Spark. The console nags to upgrade; do not. |
| Firebase **Blaze** (only needed for Cloud Storage default buckets or Cloud Functions) | pay-as-you-go | Not needed by this design. If ever: set a Cloud Billing budget first. |
| Google Maps Platform | **yes**, after the free monthly allowances, and billing must be linked | Budget alert + per-API daily quotas + key restrictions (5.3). |
| Mapbox / Stadia / MapTiler | yes, above free tiers; Stadia free plan is non-commercial | Only with a spending cap in their dashboards. |
| OpenFreeMap, Photon, MapLibre | none | Fair use; self-host when big. |
| Backend hosting (Fly.io, Railway, a VPS) | $0–5/month | Not part of tonight. DEV_MODE needs no hosting. |
| Google Play developer account | $25 one-time | Only when publishing. |
| Gradle / Go module downloads | none | — |

## 7. Troubleshooting

| # | Symptom | Cause / fix |
|---|---|---|
| T1 | `Could not find or load main class org.gradle.wrapper.GradleWrapperMain` | `app/gradle/wrapper/gradle-wrapper.jar` missing or corrupt. In Android Studio: File → Sync; or run `gradle wrapper --gradle-version 8.11.1` with any installed Gradle; or re-download the jar from `https://raw.githubusercontent.com/gradle/gradle/v8.11.1/gradle/wrapper/gradle-wrapper.jar`. |
| T2 | `Failed to find Platform SDK with path: platforms;android-35` | Install API 35 in the SDK Manager, or set `compileSdk = 36` and `targetSdk = 36` in `app/app/build.gradle.kts` (android-36 is installed). |
| T3 | Version-resolution errors for AGP 8.7.3 / Kotlin 2.1.0 / Compose BOM 2024.12.01 | The pins were chosen from memory, not verified against Maven tonight. Open `app/gradle/libs.versions.toml` and let Android Studio's "Upgrade Assistant" or the hover hints suggest the nearest existing versions. Keep AGP and Android Studio compatible (AGP 8.7 ↔ Ladybug+). |
| T4 | `Unresolved reference: FlowRow` or `ModalBottomSheet` | Compose BOM too old; bump `composeBom` (FlowRow is in foundation 1.5+, ModalBottomSheet in material3 1.1+). |
| T5 | `Unresolved reference` in `MapLibreMapProvider` (`org.maplibre.android...`) | MapLibre 11 renamed packages from `com.mapbox.mapboxsdk` to `org.maplibre.android`. If the version resolves but names differ, check https://github.com/maplibre/maplibre-native/releases for the 11.x API; `getSourceAs`, `GeoJsonSource`, `LineLayer`, `CircleLayer` exist in 11.x. |
| T6 | Downloadable font warning / Sora not showing | The emulator image has no Google Play services. The app falls back to the system sans-serif; nothing to fix. Use a "Google APIs" image to see Sora. |
| T7 | Map is blank | No internet on the emulator (cold boot sometimes loses DNS: toggle airplane mode), or the style URL is blocked. Check Logcat for `MapLibre` / `HTTP 4xx`. |
| T8 | `CLEARTEXT communication to 10.0.2.2 not permitted` | `usesCleartextTraffic` placeholder is `false`; set it to `true` for local HTTP. |
| T9 | Reports return `400 validation_error` (lat/lng outside bounds) | Lat/lng not inside `METRO_BOUNDS` (default Santa Cruz box). In the app this happens if the pin is dragged far; on curl check the sign of the coordinates (south and west are negative). |
| T10 | Reports return `429 quota_exceeded` while testing | 3 per device per **UTC** day. Use another `X-Install-Id`, or restart the DEV_MODE backend (memory store resets). |
| T11 | `403 blocked` for `blocked-demo-device` | Expected: it is a seeded block for 30 days. |
| T12 | `go: missing go.sum entry` | Run `go mod tidy` in `backend/` (already done tonight, but run again after pulling). |
| T13 | Search returns nothing | Photon needs ≥ 2 characters; check Logcat for a 429 (fair-use throttling) and wait a minute. |
| T14 | "No encontramos micros para este viaje" | No variant passes within 400 m of the origin or the destination, or no transfer link joins them. The dataset has 62 of ~120 lines (docs/03 §6); try a destination on a main avenue. |
| T17 | `docker compose up` fails with "Ports are not available … 5432" | Hyper-V reserves port ranges on Windows. `.env` uses `POSTGRES_PORT=55432`; change it again if that one is also reserved (`netsh interface ipv4 show excludedportrange protocol=tcp`). |
| T18 | `api` container restarts with "postgres ping: connection refused" | PostGIS restarts once during first init; the API retries for 45 s and Compose restarts it. Wait 30 s and check `docker compose ps`. |
| T19 | `build-tiles.sh`: "tar: This does not look like a tar archive" or 404 on builds.json | Old script. The build list is at build-metadata.protomaps.dev; the zip is extracted with Python. `git pull` the script. |
| T20 | Prod app: map is grey/blank | The phone cannot reach `PIYO_API_BASE_URL` (check `adb shell curl http://<PC-IP>/v1/health`), or `build-tiles.sh` was not run (`/v1/map/style.json` returns 404 "map style not generated yet"). Windows Firewall may block port 80 for Docker: allow "Docker Desktop Backend" or add an inbound rule for TCP 80. |
| T21 | Prod app: search always "Sin resultados" | Photon public instance refused the query (HTTP 400 for `lang=es` was the cause on 2026-10-28, fixed) or throttled (429). Test from the PC: `curl "https://photon.komoot.io/api/?q=ventura&lat=-17.78&lon=-63.18"`. |
| T15 | NPS sheet never appears | By design (docs/02): 7 days since first open, 5 completed trips, max once per 90 days. Datos y conexión → "Opinar sobre Piyo" opens it anywhere. |
| T16 | `Unresolved reference: entries` on an enum | Needs Kotlin 1.9+; the catalog pins 2.1.0, so this only appears if the Kotlin version was lowered. Replace `entries` with `values()`. |

## 8. Configuration reference

There are no product flavors and no mock data since iteration 3 (the `src/dev` mocks, `MockMapProvider`, `MockPlaces`, the mock repositories and `BuildConfig.MOCK_DATA` were removed). `app/app/build.gradle.kts` has a single `defaultConfig` that reads `app/keys.properties` (git-ignored; template in `app/keys.properties.example`) and exposes three `BuildConfig` strings:

| Key in `keys.properties` | BuildConfig | Default when absent | Used by |
|---|---|---|---|
| `PIYO_API_BASE_URL` | `API_BASE_URL` | `http://10.0.2.2:8080` | `PiyoApi` (manifest, package download, reports, NPS, suggestions) and, when `MAP_STYLE_URL` is empty, the map style `API_BASE_URL/v1/map/style.json` |
| `MAP_STYLE_URL` | `MAP_STYLE_URL` | empty (= style from the API) | `MapLibreMapProvider` |
| `GEOCODER_URL` | `GEOCODER_URL` | `https://photon.komoot.io/api/` | `PhotonGeocoder` (text search) |

Backend (compose `api` service / `.env`): `MAX_CONTRIBUTIONS_PER_MONTH` (default 3) caps route contributions per device and UTC month; the PostgreSQL table is created by `backend/internal/store/postgres/migrations/002_contributions.sql` on start (idempotent), SQLite creates it lazily. Review with the admin endpoints listed in `backend/README.md`.

The dependency graph is built once in `app/app/src/main/java/bo/piyo/app/di/AppModule.kt`: `AssetLinesRepository` (bundled seed + downloaded releases, exposed as `officialLines`) wrapped by `EffectiveLinesRepository` (applies the active local provider from `ProvidersRepository`; this is `lines`, what screens and the planner use), `HttpContributionsRepository` (`/v1/contributions`), `AndroidLocationProvider` (framework `LocationManager`, falls back to the Plaza 24 de Septiembre when there is no permission or fix), `MapLibreMapProvider` + `PhotonGeocoder`, `HttpReportsRepository`, `HttpNpsRepository`, `HttpManifestRepository`. Variants are only `debug` and `release`; the application id is `bo.piyo.app` for both. The backend's `DEV_MODE` seed (`backend/internal/seed`: demo devices, sample reports and NPS rows) still exists for backend-only testing and is unrelated to the app build.
