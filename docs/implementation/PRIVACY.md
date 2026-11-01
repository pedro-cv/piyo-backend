# Privacy assumptions

Piyo has no accounts, no analytics and no ads (docs/02 principle 2, docs/03 §5). This document lists what the first implementation stores, where, and why, so the policy text for Google Play can be written from it.

## 1. On the phone only (never sent)

| Data | Where | Purpose | Retention |
|---|---|---|---|
| Line bundle (public OSM-derived data) | `assets/` + `filesDir/lines-current.json` | Offline routing | Replaced on update |
| Favorites: Casa, Trabajo (name + coordinates), favorite line ids | DataStore `piyo_favorites` | Fast repeat trips | Until the user clears them or app data |
| Settings: dark mode, notifications switch, first-run flag, map-downloaded flag | DataStore `piyo_settings` | UI preferences | Same |
| Recent searches (last 5 names) | DataStore `piyo_settings` | Convenience | Same |
| NPS gating counters: first open timestamp, completed trips count, last NPS timestamp, dismissals | DataStore `piyo_settings` | Show the NPS sheet at most once per 90 days (docs/02) | Same |
| Offline outbox: pending reports and NPS bodies | DataStore `piyo_settings` | Send when online | Deleted after delivery |
| Last computed itineraries | memory (`TripSession`) | Resultados/Ruta screens | Until the Activity dies |
| GPS position | memory | "Mi ubicación", "pasan cerca" | Never persisted, never sent |

Location permission is requested lazily (not implemented tonight: the prod `AndroidLocationProvider` reads the last known fix only if permission was granted, otherwise uses the Plaza as origin). Coordinates are never included in NPS; they are included in a **report** only because the report is about a place.

## 2. Sent to the backend

| Field | Reports | NPS | Notes |
|---|---|---|---|
| `X-Install-Id` header | yes | yes | Random UUID generated on first run (`SettingsRepository.installId`). Not derived from Android ID, IMEI, advertising id or any hardware identifier. Cleared with app data. |
| `type`, `lat`, `lng`, `line_id`, `comment` ≤ 500 | yes | — | The subject of the report. The pin defaults to the nearest point of the line, not the user's position, unless the user leaves it there. |
| `score`, `reasons[]`, `comment` ≤ 140 | — | yes | |
| `app_version`, `data_version` | yes | yes | To interpret reports against the right data release. |
| `device_class` | — | yes | Coarse string such as `android-34`. No model, no brand. |
| `X-App-Version` header | yes | yes | Same as app_version. |

| **Contributions** (`POST /v1/contributions`, iteration 4) | only when the user taps "Enviar aportes" | | The routes the user picked from their local provider (`lines`, `variants` with polylines, `edits.before` original polylines), the provider **name** they typed, `base_dataset_version`, optional `comment` ≤ 500, `app_version`, and the same `X-Install-Id` header. The provider file itself never contains the install id, so an exported/shared file identifies nobody. |

Local data providers (`filesDir/providers/*.json`), saved places with icon/colour and the notification setting stay on the phone (§1). The notification is posted locally after a dataset update; no push token exists.

Not sent: user agent beyond OkHttp's default (TODO next iteration: set a neutral UA), contacts, photos, account, language, timezone, battery, other apps.

## 3. What the backend stores (`backend/internal/model`)

- **Device hash** = `sha256(HASH_SALT + install_id)`, hex. The raw install id is never written to the store or logs. Without the salt the hash cannot be linked back to an install; the salt lives only in the server environment.
- Reports: device hash, type, lat/lng, line id, comment, app/data version, status, timestamps.
- NPS: device hash, score, reasons, comment, app/data version, device class, timestamp. The hash is kept only to let a maintainer spot a single device flooding the table; exports (`/v1/admin/nps.csv`) **should drop it** before publishing the monthly CSV (TODO next iteration: strip the column in the exporter).
- Contributions: device hash, provider name, base dataset version, route ids, payload (routes), comment, app version, status, review note, timestamps.
- Blocks: device hash, until, reason.
- IP addresses: used in memory for the per-IP rate limiter and discarded; not logged, not persisted. The access log records method, path, status and duration only.
- FCM tokens (future): per docs/03 §8, stored `report → token` only until the data release that applies the report, then deleted; max 90 days. Not implemented tonight.

## 4. Abuse control, and why it is not identity

3 reports per device per UTC day; 5 suggestions per day; **3 route contributions per device per UTC month** (`MAX_CONTRIBUTIONS_PER_MONTH`, so reviewers have time to verify each route); rejected contributions count towards the same auto-block; a device with 3 rejected reports within 90 days is blocked 30 days (`REJECTS_TO_BLOCK`, `REJECT_WINDOW_DAYS`, `BLOCK_DAYS`). Clearing app data resets the install id and therefore the quota and the block. This is accepted: the goal is friction against casual abuse, not identification (docs/03 §8). Blocked devices can still send NPS because NPS is only aggregated.

## 5. Third parties contacted by the app

| Party | When | What they see |
|---|---|---|
| Piyo map server (Martin + Caddy, same host as the API) or OpenFreeMap when `MAP_STYLE_URL` points to it | Map on screen | Tile requests: IP, tile coordinates (coarse area being viewed), User-Agent |
| Photon (komoot) | Typing in search | Query text, bias coordinates (the user's position or the Plaza), IP, User-Agent `Piyo/<version>` |
| Google Play services fonts provider | Font load | Standard provider request on the device; no app data |
| Piyo backend | Reports, NPS, manifest | Section 2 |

Since iteration 3 there is a single build; there is no offline-only flavor. With no network the app still works for lines and routing from the bundled dataset and contacts nobody.

## 6. Open items for the policy text

- Add a privacy screen or a link in *Acerca de* once the policy is published.
- Decide whether to show reports publicly as GitHub issues (docs/03 §8): a report's comment and coordinates become public; the UI note already says reports are anonymous, it should also say "público".
- Strip the device hash from the published NPS CSV (see §3).
