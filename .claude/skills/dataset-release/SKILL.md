---
name: dataset-release
description: Import an upstream line dataset with piyo-import, publish an immutable release vN (manifest, checksums, PostGIS load), and refresh the app's bundled seed. Use when the transit data changes.
---

# /dataset-release — publish dataset vN

Two ways to make vN: **from upstream** (this page, `piyo-import`) or **from accepted contributions**
(`piyo-release` on top of the latest release, then `POST /v1/admin/datasets/{N}/publish` after deploying;
see README "New dataset release from accepted contributions"). Never run both for the same N.

Argument: path to the upstream `lines.json` and the new version number.
Upstream layouts supported: `osm` (`data/lines.json`) and `named-routes` (see `docs/DATA_SOURCES.md`).

## Steps
1. **Pick N** = highest existing `deploy/data/releases/vN` + 1 (`ls ../deploy/data/releases`).
   Releases are immutable: never overwrite an existing folder.
2. **Validate upstream** in the data repo first (its own `validate` script) and note warnings.
3. **Dry run** (no DB):
   ```sh
   go test ./...                                   # builder tests for both layouts
   go run ./cmd/piyo-import -source <lines.json> -out ../deploy/data/releases -version N
   ```
   Read the printed summary: lines, variants, stops, transfers, bbox, attribution, package bytes.
   Compare with the previous manifest (`cat ../deploy/data/releases/v(N-1)/manifest.json`); a drop in
   lines or a bbox jump needs an explanation before publishing.
4. **Load PostGIS** (stack up, password from `deploy/.env`, never paste it in a PR):
   ```sh
   go run ./cmd/piyo-import -source <lines.json> -out ../deploy/data/releases -version N -db "$DATABASE_URL"
   curl -s localhost/v1/datasets | head            # new version listed
   curl -s localhost/v1/manifest                   # points at vN, sha256 present
   ```
5. **App seed**: copy the compact package to `app/app/src/main/assets/dataset-seed.json` (uncompressed
   JSON as today), run `cd app && ./gradlew :app:testDebugUnitTest` — `DatasetParserTest` asserts the
   version and counts, update those assertions deliberately.
6. **Cross-check trips**: `python tools/check_trips.py` (or the 4 hand-checked trips in
   `docs/implementation/EVIDENCE.md`) and one device run with `/verify-app` flows 2–4.
7. **Docs**: `DATA_MODEL.md` release table row, `EVIDENCE.md` section with the summary line and the
   trip checks, `SETUP_AND_RUN.md` if the command changed.
8. **Attribution**: confirm `manifest.json` `attribution`/`license` match `docs/DATA_SOURCES.md`.
   Do not hard-code provider names anywhere; the importer derives them in `internal/dataset/sources.go`.
