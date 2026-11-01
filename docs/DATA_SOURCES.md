# Data sources and attribution

Piyo shows public-transport line geometry that it does not create itself. This is the one place in the
repositories that names the upstream providers; code, UI and READMEs refer to them generically
("the route data provider", "official data") and display whatever `attribution` / `license` text the
dataset release carries.

| Dataset family | Upstream | License / terms | Attribution string shipped in releases |
|---|---|---|---|
| OSM-derived (`data/lines.json`, releases ≤ v13) | OpenStreetMap via Overpass | ODbL 1.0 | `© OpenStreetMap contributors` |
| Named-routes provider (`data-microcruz/lines.json`, releases ≥ v14) | Microcruz route API (`microcruz.tel.bo`) | Route geometry © Microcruz; colours from the provider's colour list and line names | `Recorridos © Microcruz` |

How the importer decides: `backend/internal/dataset/sources.go` maps the source file's `source`/`license`
text to a family; the app shows `attribution` on the line screen and `license` under Datos → Acerca de.

## Before publishing a repo that bundles this data

The Android app ships the current release as `app/app/src/main/assets/dataset-seed.json`. OSM-derived
releases may be redistributed under ODbL with attribution. For the named-routes provider, confirm you have
permission to redistribute the geometry in a public repository and in APKs; if not, keep the seed out of
git (download it in CI from the API, or ship only the OSM-derived seed) and point `PIYO_API_BASE_URL` at a
server that serves the releases.
