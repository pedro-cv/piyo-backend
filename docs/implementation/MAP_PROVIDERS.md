# Piyo - Map provider comparison (online map + place search)

Date: 2026-10-28. All prices in USD, read from the public pricing/terms pages listed in "Sources" on that date.
Scope: the ONLINE complement to Piyo's map (MapLibre; the bundled MBTiles ~38 MB from `03-infra-and-tech.md` was never shipped, the app uses MapLibre's tile cache for visited areas).
Piyo is free, ad-free, offline-capable (routing and line data on the phone; search and new map areas online), for Santa Cruz de la Sierra (Bolivia), built by a solo developer with a $0-3/month budget.

## 1. Usage assumptions (change these and recompute)

| Parameter | Value | Note |
|---|---|---|
| Monthly active users (MAU) | 1,000 / 10,000 / 100,000 | three scenarios |
| Map opens per user per month | 20 | 1 "map load" = 1 Google "Dynamic Maps" event = 1 MapTiler "session"; Mapbox/Stadia do not bill per load |
| Vector tiles fetched per map open | 30 | only matters for per-tile billing (Stadia, OSM, MapTiler via MapLibre); assumption, caching lowers it |
| Text-search sessions per user per month | 10 | 1 session = typing one query and picking a result |
| Autocomplete requests per session | 5 | one request per keystroke batch; only matters for per-request billing |
| Place lookups per session | 1 | the final request that returns coordinates |

Derived volumes per month:

| MAU | Map loads | Tiles (30/load) | Search sessions | Autocomplete requests (5/session) |
|---|---|---|---|---|
| 1k | 20,000 | 600,000 | 10,000 | 50,000 |
| 10k | 200,000 | 6,000,000 | 100,000 | 500,000 |
| 100k | 2,000,000 | 60,000,000 | 1,000,000 | 5,000,000 |

## 2. Comparison table

| Criterion | Google Maps Platform | Mapbox | OSM-based: MapLibre + OpenFreeMap + Photon | Stadia Maps (4th column; MapTiler below) |
|---|---|---|---|---|
| Map rendering cost @1k / 10k / 100k MAU | $70 / $1,190 / $7,070 | $0 / $0 / $300 | $0 / $0 / $0 | see credits: $20 / $245 / $2,540 total (map + search); free plan is non-commercial only |
| Text search cost @1k / 10k / 100k MAU | $273 / $4,203 / $28,293 | $28.50 / $298.50 / ~$2,550 | $0 / $0 / $0 (fair-use, may be throttled) | included in the credit figures above |
| Points (markers) and lines (polylines) | Yes, Maps SDK + maps-compose; no extra SKU | Yes, PointAnnotation / PolylineAnnotation + maps-compose; no extra cost | Yes, MapLibre symbol/line layers or maplibre-compose; free | Yes (Stadia serves tiles/styles to MapLibre); free |
| Offline support | No offline tiles for Maps SDK; Places caching restricted (place IDs exempt) | Offline tile regions, max 750 tile packs cumulative; requires Mapbox token and counts user as MAU | Full: bundled MBTiles or OpenFreeMap offline download (max 3,000 tiles / 50 MB per download, 4 parallel requests) | ToS allows client cache up to 100 MB per device; bulk download otherwise forbidden |
| Key required | Yes (API key in manifest via secrets-gradle-plugin; billing account required) | Yes (public access token in res/values) | No key, no account, no cookies (OpenFreeMap, Photon) | Yes (api_key query parameter for mobile) |
| Attribution | Google logo/attribution must stay visible; Places results must be on a Google map or show Google Maps logo | Mapbox logo + "(c) Mapbox (c) OpenStreetMap, Improve this map"; telemetry opt-out needed if the built-in button is removed | "OpenFreeMap (c) OpenMapTiles Data from OpenStreetMap" (OpenFreeMap part optional); OSM licence attribution visible | "(c) Stadia Maps (c) OpenMapTiles (c) OpenStreetMap" with links |
| Terms risk | Places content cannot be shown on a non-Google map; pre-fetching/caching of Places content restricted; 30-day caps on cached coordinates in several APIs | Attribution/telemetry rules; price changes possible; offline capped | Photon: "extensive usage will be throttled or completely banned", no availability guarantee. OpenFreeMap: no SLA. OSM public raster tiles (tile.openstreetmap.org) forbid bulk/pre-seeding and need unique User-Agent; Piyo must NOT use them | Free plan: "non-commercial or evaluation purposes" only; a free ad-free civic app is arguably non-commercial but ask Stadia; else $20/month |
| Android/Compose SDK maturity | Very high (play-services-maps 20.1.0, maps-compose by Google) | Very high (com.mapbox.maps:android 11.33.0, maps-compose extension) | High for MapLibre Native (org.maplibre.gl:android-sdk 11.x); maplibre-compose 0.20.0 is pre-1.0 but usable | Uses MapLibre; Stadia has Kotlin SDK for APIs |
| Fit for Piyo (offline-first, low-end phones, Bolivia) | Poor: paid at 1k MAU, no offline, two separate maps in the app would violate Places terms | Medium: free until 25k MAU for the map, search is paid from 500 sessions; heavier SDK | Best: $0, key-free, same renderer online and offline, light; search quality in Bolivia depends on OSM coverage | Medium: cheap, OSM-based, same MapLibre renderer; needs a key and a plan decision |

MapTiler Cloud (not chosen): Free plan 5,000 map sessions, 100,000 API requests, 1,000 search sessions, 1 key, commercial use NOT allowed. Flex plan $30/month: 25,000 sessions, 500,000 API requests, 3,000 search sessions, extra $2.50/1,000 sessions, $2.50/1,000 search sessions, $0.15/1,000 API requests. When tiles are consumed by a third-party SDK (MapLibre) they bill per tile request, not per session, so at 1k MAU (600k tiles, 10k searches) Flex costs about $30 + $15 + $17.50 = $62.50; at 10k MAU ~ $1,098; at 100k ~ $11,448. Stadia is cheaper and simpler, hence the fourth column.

## 3. Cost per scenario with the arithmetic

### Google Maps Platform (Essentials/Pro per-SKU free calls; the $200 credit was replaced in March 2025)
Dynamic Maps: 10,000 free/month; then $7.00 per 1,000 (10,001-100,000), $5.60 (100,001-500,000), $4.20 (500,001-1M), $2.10 (1M-5M).
Autocomplete Requests (Essentials): 10,000 free; $2.83 (10,001-100,000), $2.27 (100,001-500,000), $1.70 (500,001-1M), $0.85 (1M-5M).
Text Search Pro (returns coordinates/name/address): 5,000 free; $32.00 (5,001-100,000), $25.60 (100,001-500,000), $19.20 (500,001-1M).

| MAU | Dynamic Maps | Autocomplete requests | Text Search Pro | Total |
|---|---|---|---|---|
| 1k | 20k loads: 10k x $7 = $70.00 | 50k: 40k x $2.83 = $113.20 | 10k: 5k x $32 = $160.00 | $343.20 |
| 10k | 200k: 90k x 7 + 100k x 5.60 = $1,190.00 | 500k: 90k x 2.83 + 400k x 2.27 = $1,162.70 | 100k: 95k x 32 = $3,040.00 | $5,392.70 |
| 100k | 2M: 630 + 2,240 + 2,100 + 1,000k x 2.10 = $7,070.00 | 5M: 254.70 + 908 + 850 + 4,000k x 0.85 = $5,412.70 | 1M: 3,040 + 10,240 + 9,600 = $22,880.00 | $35,362.70 |

Cheaper Google variant (not costed, partially unverified): session-based autocomplete ("Autocomplete Session Usage" is listed as unlimited free) ending in one Place Details Essentials call; the Place Details price was not on the pages read. Even then the map alone costs $70 at 1k MAU.

### Mapbox
Maps SDK for Mobile: first 25,000 MAU free; $4.00 per 1,000 MAU (25,001-125,000). Search Box API: 500 sessions free; $3.00 per 1,000 (501-100,000), volume discounts down to $2.50 (tier edges above 100k not shown on the page; assumed $2.50 from 100,001).

| MAU | Map (MAU) | Search Box sessions | Total |
|---|---|---|---|
| 1k | within 25k free = $0 | 10k: 9,500 x $3 = $28.50 | $28.50 |
| 10k | $0 | 100k: 99,500 x $3 = $298.50 | $298.50 |
| 100k | 75,000 x $4 = $300.00 | 1M: 298.50 + 900k x $2.50 = $2,548.50 | ~$2,848.50 |

Alternative: Temporary Geocoding API (autocomplete per request) 100,000 free then $0.75/1,000: 1k MAU $0; 10k MAU 400k x 0.75 = $300; 100k ~$2,700-3,000. Similar to Search Box.

### OSM-based stack (MapLibre + OpenFreeMap + Photon)
OpenFreeMap: "no limits on views or requests", no keys, commercial use welcome, donation-funded. Photon public instance: free, fair use, throttled if extensive. MapLibre: open-source renderer, no fees.

| MAU | Tiles | Search | Total |
|---|---|---|---|
| 1k | 600k tile requests, $0 | 50k requests, $0 | $0 |
| 10k | 6M, $0 | 500k, $0 | $0 (Photon throttling possible) |
| 100k | 60M, $0 | 5M, $0 | $0 (Photon self-host likely needed; a small VPS ~ $5-10/month, not verified here) |

Optional: GitHub Sponsors donation to OpenFreeMap (voluntary).

### Stadia Maps
Free plan 200,000 credits/month, non-commercial only. Starter $20/month = 1,000,000 credits, +$0.03 per 1,000 extra credits. Vector tile = 1 credit, Autocomplete v2 = 1 credit/request, Geocoding = 20 credits/request. Credits per user = 600 tiles + 50 autocomplete + 10 geocoding x 20 = 850.

| MAU | Credits | Plan | Total |
|---|---|---|---|
| 1k | 850,000 | within Starter 1M | $20.00 ($0 only if Stadia accepts Piyo as non-commercial AND usage stays under 200k credits, which it does not at 850k) |
| 10k | 8,500,000 | Starter + 7.5M extra x $0.03/1,000 = $225 | $245.00 |
| 100k | 85,000,000 | Starter + 84M extra = $2,520 | $2,540.00 (a higher plan would be cheaper; not read) |

## 4. Recommendation

Keep the OSM-based stack as the default: MapLibre Native/Compose renderer, OpenFreeMap "liberty" style online, bundled MBTiles offline, Photon (photon.komoot.io) for search with autocomplete. Reasons: $0 at every scenario, no key or account to manage or leak, one renderer for online and offline, explicitly commercial-friendly tiles, and Piyo's own data (bus lines, stops) is drawn as our own GeoJSON layers so no provider's content restriction applies.

Mitigations the stack needs:
1. Search bias and filter: call Photon with lat/lon of Santa Cruz, a bbox around the metro area, lang=es, limit=8; debounce 300 ms and require 3+ characters to stay well inside fair use.
2. Fallback order: Photon -> local stop/landmark index bundled in the app (works offline, covers the most common queries) -> show "sin resultados".
3. Never use tile.openstreetmap.org raster tiles (bulk/heavy use forbidden there).
4. Set a unique User-Agent ("Piyo/<version> (contact)") on all Photon and OpenFreeMap requests.
5. If Photon throttles at ~10k+ MAU, self-host Photon for Bolivia (Elasticsearch/OpenSearch, small index) or switch search to Stadia Starter ($20/month, 1 credit per autocomplete request).

When Google is worth it: if OSM coverage of Santa Cruz addresses/POIs proves too thin in field tests and users need "name of a shop" search quality, use Google Places for search only (Autocomplete Session + Place Details) AND show results on the Google map (Places terms forbid a non-Google map) - that means two renderers and about $70+/month from 1k MAU. Google also brings support/SLA and a billing account requirement.

When Mapbox is worth it: if the owner wants a commercial-grade SDK with offline tile packs and support, Mapbox is free for the map up to 25k MAU and ~$28.50/month for search at 1k MAU; it is the cheapest commercial option but not $0.

The decision belongs to the owner. The default implementation (OSM stack) does not block switching: the `MapProvider` interface isolates the choice.

## 5. How each option plugs into the app

The app has `interface MapProvider { suspend fun searchPlaces(query: String, near: LatLng): List<Place>; @Composable fun Map(markers, polylines, ...) }`.

### OSM stack (default)
- Gradle: `implementation("org.maplibre.gl:android-sdk:11.8.0")` (View-based) or `implementation("org.maplibre.compose:maplibre-compose:0.20.0")` (Compose, pre-1.0).
- Style URL: `https://tiles.openfreemap.org/styles/liberty` (also bright, positron). Offline: `mbtiles://` or a local style pointing at the bundled MBTiles.
- Search: `GET https://photon.komoot.io/api/?q=<query>&lat=-17.78&lon=-63.18&lang=es&limit=8` (+ optional `bbox=`), parse GeoJSON features. Reverse: `/reverse?lat=&lon=`.
- No key, no manifest entry, no console project. Attribution text in a corner: "OpenFreeMap (c) OpenMapTiles Data from OpenStreetMap".
- Markers/polylines: GeoJSON source + SymbolLayer/LineLayer, or maplibre-compose `Symbol`/`Line` layers.

### Google Maps Platform
- Gradle: `implementation("com.google.android.gms:play-services-maps:20.1.0")`, `com.google.maps.android:maps-compose` (version not read today), `implementation("com.google.android.libraries.places:places:6.0.2")`, plugin `com.google.android.libraries.mapsplatform.secrets-gradle-plugin`.
- Key: `MAPS_API_KEY=...` and `PLACES_API_KEY=...` in `secrets.properties` (git-ignored), `local.defaults.properties` fallback; manifest `<meta-data android:name="com.google.android.geo.API_KEY" android:value="${MAPS_API_KEY}"/>`; `Places.initializeWithNewPlacesApiEnabled(context, apiKey)`.
- Enable in Cloud Console: Maps SDK for Android, Places API (New); billing account mandatory; restrict the key to the app package + SHA-1.
- Markers/polylines: `Marker` and `Polyline` composables in maps-compose. Place results must be shown on this Google map.

### Mapbox
- Gradle: `implementation("com.mapbox.maps:android-ndk27:11.33.0")` (or without `-ndk27`), `implementation("com.mapbox.extension:maps-compose-ndk27:11.33.0")`, `implementation("com.mapbox.search:mapbox-search-android:2.33.1")`.
- Token: public token in `app/res/values/mapbox_access_token.xml` as `<string name="mapbox_access_token" translatable="false">...</string>` or `MapboxOptions.accessToken = ...` before the MapView; token created in the Mapbox account.
- Offline: TileStore regions, cumulative cap of 750 tile packs.
- Markers/polylines: `PointAnnotation`/`PolylineAnnotation` in maps-compose. Keep the Mapbox logo + attribution button (or add telemetry opt-out).

### Stadia Maps
- Renderer: same MapLibre dependencies as the OSM stack; style URL `https://tiles.stadiamaps.com/styles/<style>.json?api_key=KEY`.
- Search: Autocomplete v2 / Geocoding REST endpoints with `api_key=` query parameter; optional Kotlin SDK (coordinates not read today).
- Key stored in a BuildConfig field from `local.properties`; create account, pick Free (non-commercial) or Starter ($20).
- Attribution "(c) Stadia Maps (c) OpenMapTiles (c) OpenStreetMap" with links.

## 6. Sources (read 2026-10-28 unless noted)

- Google pricing overview (credit replaced by per-SKU free calls; 10K/5K/1K): https://mapsplatform.google.com/pricing/
- Google per-SKU price list (Dynamic Maps $7.00..., Text Search $32/$35, Autocomplete $2.83..., Autocomplete Session Usage unlimited): https://developers.google.com/maps/billing-and-pricing/pricing
- Places SDK for Android policies (results must be on a Google map; caching restricted except place IDs): https://developers.google.com/maps/documentation/places/android-sdk/policies
- Maps SDK for Android policies (attribution must not be removed): https://developers.google.com/maps/documentation/android-sdk/policies
- Google service-specific terms ("No use with a non-Google map", 30-day caching caps): https://cloud.google.com/maps-platform/terms/maps-service-terms - page truncated on fetch; the clauses were confirmed only via search snippets, mark as partially unverified. Core ToS https://cloud.google.com/maps-platform/terms could not be read (truncated) - unverified.
- Places SDK for Android config (places:6.0.2, secrets.properties): https://developers.google.com/maps/documentation/places/android-sdk/config
- Maps SDK for Android config (play-services-maps:20.1.0, API_KEY meta-data): https://developers.google.com/maps/documentation/android-sdk/config
- Place Details Essentials price: NOT read - unverified.
- Mapbox pricing (25,000 MAU free, $4.00/1,000; Search Box 500 free, $3.00/1,000; Temporary Geocoding 100,000 free, $0.75/1,000; Address Autofill 1,000 free, $12.50/1,000): https://www.mapbox.com/pricing
- Mapbox Android install (android-ndk27:11.33.0, maps-compose-ndk27, mapbox_access_token.xml): https://docs.mapbox.com/android/maps/guides/install/
- Mapbox Search SDK (mapbox-search-android:2.33.1): https://docs.mapbox.com/android/search/guides/ and billing note: https://docs.mapbox.com/android/search/guides/pricing/
- Mapbox attribution (logo + text mandatory, telemetry opt-out): https://docs.mapbox.com/help/dive-deeper/attribution/
- Mapbox offline (750 tile packs): https://docs.mapbox.com/android/maps/guides/offline/
- Mapbox ToS (no caching clause found in the fetched text): https://www.mapbox.com/legal/tos
- OpenFreeMap (free, no limits, no keys, commercial OK, attribution, 3,000 tiles/50 MB offline download cap, no SLA): https://openfreemap.org/
- Photon public API policy ("please be fair - extensive usage will be throttled", no availability guarantee): https://photon.komoot.io/ and README (Apache 2.0, "throttled or completely banned"): https://github.com/komoot/photon
- Nominatim usage policy (max 1 req/s, no autocomplete, valid User-Agent, results must be cached): https://operations.osmfoundation.org/policies/nominatim/
- OSM tile usage policy (no bulk download, no .mbtiles archives, unique User-Agent, access may be withdrawn): https://operations.osmfoundation.org/policies/tiles/
- MapLibre Native Android getting started (org.maplibre.gl:android-sdk, example 11.8.0): https://maplibre.org/maplibre-native/android/examples/getting-started/
- MapLibre Compose getting started (org.maplibre.compose:maplibre-compose:0.20.0): https://maplibre.org/maplibre-compose/getting-started/
- MapTiler Cloud pricing (Free 5,000 sessions/100,000 requests/1,000 search sessions, non-commercial; Flex $30): https://www.maptiler.com/cloud/pricing/ ; MapTiler attribution pages returned 404 - unverified.
- Stadia Maps pricing (Free 200,000 credits, non-commercial; Starter $20 = 1,000,000 credits, +3c/1,000; 1 credit/tile, 20/geocode, 1/autocomplete v2): https://stadiamaps.com/pricing/
- Stadia ToS (free tier non-commercial; client cache up to 100 MB per device): https://stadiamaps.com/terms-of-service/
- Stadia authentication (API key for mobile apps): https://docs.stadiamaps.com/authentication/ ; attribution: https://docs.stadiamaps.com/attribution/

## 7. Does the recommended option cost money?

No. The OSM-based stack (MapLibre + OpenFreeMap + Photon) costs $0/month at 1k, 10k and 100k MAU. There is no key, no account and no billing. The only money that could appear is voluntary (a donation to OpenFreeMap) or a self-hosted Photon server (~$5-10/month VPS, estimate, not verified) if photon.komoot.io throttles Piyo at high volume. For comparison: Google $343 / $5,393 / $35,363; Mapbox $28.50 / $298.50 / ~$2,849; Stadia $20 / $245 / $2,540 per month at 1k / 10k / 100k MAU under the assumptions in section 1.


## Addendum 2026-10-28 (iteration 2)

- The online map is now self-hosted (MAP_HOSTING.md): Protomaps extract + Martin + Caddy. OpenFreeMap stays as a documented fallback through `MAP_STYLE_URL`.
- Photon public instance: `lang` only accepts default/de/en/fr; `lang=es` returns HTTP 400. The app sends no `lang` (verified on the phone: "mutualista" → 3 results).
