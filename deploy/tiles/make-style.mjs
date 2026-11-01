// Generates MapLibre styles (light + dark) from the Protomaps basemaps layer
// definitions, in Spanish. The API rewrites sources/glyphs/sprite URLs at
// request time (GET /v1/map/style.json), so the files here use placeholders.
// Usage: node make-style.mjs <outDir>
import { layers, namedFlavor } from "@protomaps/basemaps";
import { writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";

const out = process.argv[2] ?? "../data/style";
mkdirSync(out, { recursive: true });

for (const theme of ["light", "dark"]) {
  const style = {
    version: 8,
    name: `Piyo ${theme}`,
    glyphs: "{ASSETS}/fonts/{fontstack}/{range}.pbf",
    sprite: `{ASSETS}/sprites/v4/${theme}`,
    sources: {
      protomaps: {
        type: "vector",
        tiles: ["{TILES}/santa-cruz/{z}/{x}/{y}"],
        minzoom: 0,
        maxzoom: 15,
        attribution: "© OpenStreetMap contributors · Protomaps",
      },
    },
    layers: layers("protomaps", namedFlavor(theme), { lang: "es" }),
  };
  const file = join(out, `style-${theme}.json`);
  writeFileSync(file, JSON.stringify(style));
  console.log(`wrote ${file} (${style.layers.length} layers)`);
}
