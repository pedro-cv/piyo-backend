#!/usr/bin/env bash
# Builds everything the self-hosted map needs, into deploy/data/{tiles,assets,style}:
#   1. santa-cruz.pmtiles  : vector tiles of the metro area, extracted (HTTP range requests)
#                            from the latest Protomaps daily planet build (OpenStreetMap data).
#   2. assets/             : glyph PBFs (fonts) and sprites from protomaps/basemaps-assets.
#   3. style/style-{light,dark}.json : MapLibre styles generated with @protomaps/basemaps.
# Needs: docker, curl, tar, node >= 18. Works in Git Bash on Windows and on Linux.
# Re-run to refresh the map (see DEPLOY_VPS.md "Map data update").
set -euo pipefail
cd "$(dirname "$0")/.."
DATA="$(pwd)/data"
mkdir -p "$DATA/tiles" "$DATA/assets" "$DATA/style"

# Metro bbox (minLng,minLat,maxLng,maxLat) = METRO_BOUNDS of the API, with Viru Viru, Warnes, Cotoca, La Guardia.
BBOX="${BBOX:--63.45,-18.10,-62.90,-17.45}"
MAXZOOM="${MAXZOOM:-15}"

echo "== 1/3 tiles"
if [ -z "${BUILD:-}" ]; then
  # Build list (JSON) lives on build-metadata.protomaps.dev; the files on build.protomaps.com.
  BUILD=$(curl -fsSL https://build-metadata.protomaps.dev/builds.json | node -e '
    const b=JSON.parse(require("fs").readFileSync(0,"utf8"));
    const latest=b.filter(x=>x.key.endsWith(".pmtiles")).sort((a,b)=>a.key<b.key?1:-1)[0];
    process.stdout.write(latest.key)')
fi
echo "   using build $BUILD, bbox $BBOX, maxzoom $MAXZOOM"
# Windows Git Bash turns /data into C:/.../data inside docker args; MSYS_NO_PATHCONV prevents it.
MSYS_NO_PATHCONV=1 docker run --rm -v "$DATA/tiles:/out" protomaps/go-pmtiles:latest \
  extract "https://build.protomaps.com/$BUILD" /out/santa-cruz.pmtiles --bbox="$BBOX" --maxzoom="$MAXZOOM"
ls -la "$DATA/tiles/santa-cruz.pmtiles"

echo "== 2/3 fonts and sprites"
if [ ! -d "$DATA/assets/fonts" ]; then
  TMP=$(mktemp -d)
  curl -fsSL -o "$TMP/assets.zip" https://github.com/protomaps/basemaps-assets/archive/refs/heads/main.zip
  # zip extraction: python (Windows/Linux) or unzip, whichever exists
  if command -v python >/dev/null 2>&1; then (cd "$TMP" && python -c "import zipfile; zipfile.ZipFile('assets.zip').extractall('.')")
  elif command -v python3 >/dev/null 2>&1; then (cd "$TMP" && python3 -c "import zipfile; zipfile.ZipFile('assets.zip').extractall('.')")
  else (cd "$TMP" && unzip -q assets.zip); fi
  cp -r "$TMP"/basemaps-assets-main/fonts "$DATA/assets/"
  cp -r "$TMP"/basemaps-assets-main/sprites "$DATA/assets/"
  rm -rf "$TMP"
fi
echo "   fonts: $(ls "$DATA/assets/fonts" | wc -l) stacks, sprites: $(ls "$DATA/assets/sprites")"

echo "== 3/3 styles"
(cd tiles && npm install --silent --no-audit --no-fund 2>/dev/null || npm install --no-audit --no-fund)
node tiles/make-style.mjs "$DATA/style"
ls -la "$DATA/style"
echo "done. Restart martin/caddy if they were running: docker compose restart martin caddy"
