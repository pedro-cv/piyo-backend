package dataset

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Two tiny sources with the real layouts (OSM data/lines.json and Microcruz
// data-microcruz/lines.json) and the colour file next to the Microcruz one.

const osmSource = `{
 "generated_at": "2026-10-28T03:56:29.973Z",
 "source": "OpenStreetMap via Overpass API (https://overpass-api.de)",
 "license": "ODbL 1.0 — © OpenStreetMap contributors",
 "lines": [
  {"line_id": "10", "name": "Línea 10", "mode": "bus", "source_url": "https://www.openstreetmap.org/relation/1",
   "variants": [
    {"variant_id": "osm-1", "name": "Línea 10", "mode": "bus", "direction": "Barrio A → Barrio B",
     "stops": [{"seq": 1, "name": "Mercado", "lat": -17.78, "lng": -63.18, "osm_node": 5}, {"seq": 2, "name": null, "lat": -17.781, "lng": -63.181, "osm_node": 6}],
     "polyline": [[-17.78, -63.18], [-17.781, -63.181], [-17.782, -63.182]], "gaps": 1, "source_url": "https://www.openstreetmap.org/relation/1"},
    {"variant_id": "osm-2", "name": "Línea 10", "mode": "bus", "direction": null, "stops": [],
     "polyline": [[-17.782, -63.182], [-17.781, -63.181], [-17.78, -63.18]], "gaps": 0, "source_url": "https://www.openstreetmap.org/relation/2"}
   ]},
  {"line_id": "trufi-x", "name": "Trufi a Samaipata", "mode": "share_taxi", "source_url": "",
   "variants": [{"variant_id": "osm-3", "name": "Trufi", "mode": "share_taxi", "direction": null, "stops": [],
     "polyline": [[-17.70, -63.10], [-17.71, -63.11]], "gaps": 0, "source_url": ""}]}
 ]}`

const microcruzSource = `{
 "generated_at": "2026-10-29T00:49:28.569Z",
 "source": "Microcruz API (https://microcruz.tel.bo/api/routes.php)",
 "license": "Route geometry © Microcruz; line colors from colored-lines file and line names",
 "lines": [
  {"line_id": "41", "name": "Línea 41", "mode": "microcruz", "color": "#e32c2c", "scraped_at": "2026-10-28T20:40:00.000Z",
   "variants": [
    {"variant_id": "microcruz-linea41 Rojo", "name": "linea41 Rojo", "mode": "microcruz", "direction": "Rojo", "color": "#e32c2c", "color_source": "color-file",
     "stops_source": "none", "stops": [], "polyline": [[-17.78, -63.18], [-17.79, -63.19], [-17.78, -63.18]],
     "ida": [[-17.78, -63.18], [-17.785, -63.185], [-17.79, -63.19]], "vuelta": [[-17.79, -63.19], [-17.786, -63.184], [-17.78, -63.18]], "gaps": 0,
     "source_url": "https://microcruz.tel.bo/api/routes.php?names=linea41%20Rojo", "scraped_at": "2026-10-28T20:40:00.000Z"},
    {"variant_id": "microcruz-linea41 Verde", "name": "linea41 Verde", "mode": "microcruz", "direction": "Verde", "color": "#008000", "color_source": "name-color-word",
     "stops_source": "none", "stops": [], "polyline": [[-17.70, -63.10], [-17.71, -63.11], [-17.70, -63.10]],
     "ida": [[-17.70, -63.10], [-17.705, -63.105], [-17.71, -63.11]], "vuelta": [[-17.71, -63.11], [-17.706, -63.104], [-17.70, -63.10]], "gaps": 0,
     "source_url": "", "scraped_at": "2026-10-28T20:40:00.000Z"}
   ], "source_url": "", "license": "", "scraped_at": "2026-10-28T20:40:00.000Z"},
  {"line_id": "104B", "name": "Línea 104B", "mode": "microcruz", "color": "#808080",
   "variants": [
    {"variant_id": "microcruz-linea104B", "name": "linea104B", "mode": "microcruz", "direction": null, "color": "#808080", "color_source": "gray",
     "stops": [], "polyline": [[-17.60, -63.00], [-17.61, -63.01], [-17.60, -63.00]],
     "ida": [[-17.60, -63.00], [-17.605, -63.005], [-17.61, -63.01]], "vuelta": [[-17.61, -63.01], [-17.606, -63.004], [-17.60, -63.00]], "gaps": 0, "source_url": ""}
   ]},
  {"line_id": "7", "name": "Línea 7", "mode": "microcruz", "color": "#123456",
   "variants": [
    {"variant_id": "microcruz-linea7", "name": "linea7", "mode": "microcruz", "direction": null, "color": "#123456", "color_source": "color-file",
     "stops": [], "polyline": [[-17.60, -63.00], [-17.61, -63.01]], "ida": [], "vuelta": [], "gaps": 0, "source_url": ""}
   ]}
 ]}`

// 41's ida half in microcruzSource runs from (-17.78,-63.18) to (-17.79,-63.19): put "Urb. La Pascana"
// at the END and "Mercado Plan 3000" at the START, so the names must be swapped.
const geocodeFile = `{
 "Urb. La Pascana": {"lat": -17.79, "lng": -63.19},
 "Mercado Plan 3000": {"lat": -17.78, "lng": -63.18},
 "Segundo Anillo": null
}`

const colorFile = `[
 {"id": "line_1", "text": "41", "color": "#e32c2c", "line-detail": "Urb. La Pascana - Mercado Plan 3000", "url": ""},
 {"id": "line_2", "text": "7", "color": "#123456", "line-detail": "Segundo Anillo", "url": ""}
]`

func writeSources(t *testing.T) (osmPath, microPath string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	micro := filepath.Join(root, "data-microcruz")
	for _, d := range []string{data, micro} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	osmPath = filepath.Join(data, "lines.json")
	microPath = filepath.Join(micro, "lines.json")
	for p, c := range map[string]string{osmPath: osmSource, microPath: microcruzSource, filepath.Join(data, ColorFileName): colorFile, filepath.Join(data, GeocodeFileName): geocodeFile} {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func variantByID(t *testing.T, pkg *Package, id string) Variant {
	t.Helper()
	for _, v := range pkg.Variants {
		if v.VariantID == id {
			return v
		}
	}
	t.Fatalf("variant %s not in package (have %d)", id, len(pkg.Variants))
	return Variant{}
}

func lineByID(t *testing.T, pkg *Package, id string) Line {
	t.Helper()
	for _, l := range pkg.Lines {
		if l.LineID == id {
			return l
		}
	}
	t.Fatalf("line %s not in package", id)
	return Line{}
}

func TestBuildOSMSource(t *testing.T) {
	osmPath, _ := writeSources(t)
	pkg, err := Build(osmPath, BuildOptions{Version: 13, Now: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.PackageFormat != PackageFormat || pkg.License != "ODbL 1.0 — © OpenStreetMap contributors" || pkg.Attribution != AttributionOSM {
		t.Errorf("header: format=%d license=%q attribution=%q", pkg.PackageFormat, pkg.License, pkg.Attribution)
	}
	if len(pkg.Lines) != 2 || len(pkg.Variants) != 3 || len(pkg.Stops) != 2 {
		t.Fatalf("counts lines=%d variants=%d stops=%d", len(pkg.Lines), len(pkg.Variants), len(pkg.Stops))
	}
	l10 := lineByID(t, pkg, "10")
	if l10.Display != "10" || l10.Mode != "micro" || l10.ColorSource != "derived" || l10.Color == "" {
		t.Errorf("line 10: %+v", l10)
	}
	v1 := variantByID(t, pkg, "osm-1")
	if v1.Direction != "ida" || v1.Origin != "Barrio A" || v1.Headsign != "Barrio B" || v1.Name != "" || v1.Branch != "" {
		t.Errorf("osm-1: %+v", v1)
	}
	if len(v1.StopIDs) != 2 || v1.StopIDs[0] != "n5" || len(v1.Streets) != 1 || v1.Streets[0] != "Mercado" || v1.Gaps != 1 {
		t.Errorf("osm-1 stops/streets: %+v", v1)
	}
	v2 := variantByID(t, pkg, "osm-2")
	if v2.Direction != "vuelta" || v2.Headsign != "Línea 10" || len(v2.Streets) != 1 {
		t.Errorf("osm-2: %+v", v2)
	}
	tr := lineByID(t, pkg, "trufi-x")
	if tr.Mode != "trufi" || tr.Display != "trufi-x" || tr.FareBs != "" {
		t.Errorf("trufi: %+v", tr)
	}
	if pts := DecodePolyline(v1.Polyline); len(pts) != 3 || pts[2].Lat != -17.782 {
		t.Errorf("polyline round trip: %v", pts)
	}
	// Lines sort numerically first, named ids last.
	if pkg.Lines[0].LineID != "10" || pkg.Lines[1].LineID != "trufi-x" {
		t.Errorf("order: %s, %s", pkg.Lines[0].LineID, pkg.Lines[1].LineID)
	}
}

func TestBuildMicrocruzSource(t *testing.T) {
	_, microPath := writeSources(t)
	pkg, err := Build(microPath, BuildOptions{Version: 14})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Attribution != AttributionNamedRoutes || pkg.License != "Route geometry © Microcruz; line colors from colored-lines file and line names" {
		t.Errorf("header: license=%q attribution=%q", pkg.License, pkg.Attribution)
	}
	// 41 Rojo and 41 Verde each split in ida/vuelta; 104B split; 7 has no halves → one variant.
	if len(pkg.Lines) != 3 || len(pkg.Variants) != 7 || len(pkg.Stops) != 0 {
		t.Fatalf("counts lines=%d variants=%d stops=%d", len(pkg.Lines), len(pkg.Variants), len(pkg.Stops))
	}
	// Order: 7, 41, 104B (number then suffix).
	if got := []string{pkg.Lines[0].LineID, pkg.Lines[1].LineID, pkg.Lines[2].LineID}; got[0] != "7" || got[1] != "41" || got[2] != "104B" {
		t.Errorf("order: %v", got)
	}

	l41 := lineByID(t, pkg, "41")
	if l41.Mode != "micro" || l41.Color != "#E32C2C" || l41.ColorSource != "color-file" || l41.SourceUpdatedAt != "2026-10-28T20:40:00.000Z" {
		t.Errorf("line 41: %+v", l41)
	}
	if len(l41.VariantIDs) != 4 || l41.VariantIDs[0] != "microcruz-linea41 Rojo-ida" || l41.VariantIDs[1] != "microcruz-linea41 Rojo-vuelta" {
		t.Errorf("line 41 variant ids: %v", l41.VariantIDs)
	}
	// The geocode file says the ida half runs Plan 3000 → La Pascana, so the names are swapped.
	rojoIda := variantByID(t, pkg, "microcruz-linea41 Rojo-ida")
	if rojoIda.Direction != "ida" || rojoIda.Name != "linea41 Rojo" || rojoIda.Branch != "Rojo" || rojoIda.Origin != "Mercado Plan 3000" || rojoIda.Headsign != "Urb. La Pascana" || rojoIda.DirectionSource != "geocoded" {
		t.Errorf("41 Rojo ida: %+v", rojoIda)
	}
	if rojoIda.Color != "" { // same as the line colour → not repeated
		t.Errorf("41 Rojo ida colour should be empty, got %q", rojoIda.Color)
	}
	if rojoIda.Points != 3 || len(rojoIda.Streets) != 2 || rojoIda.Streets[0] != "Mercado Plan 3000" {
		t.Errorf("41 Rojo ida geometry/streets: %+v", rojoIda)
	}
	rojoVuelta := variantByID(t, pkg, "microcruz-linea41 Rojo-vuelta")
	if rojoVuelta.Direction != "vuelta" || rojoVuelta.Origin != "Urb. La Pascana" || rojoVuelta.Headsign != "Mercado Plan 3000" {
		t.Errorf("41 Rojo vuelta: %+v", rojoVuelta)
	}
	// 41 Verde has the same names but its ida runs (-17.70,-63.10) → (-17.71,-63.11), far from both
	// geocoded points, equally: ambiguous → the colour-file order is kept and marked "assumed".
	if v := variantByID(t, pkg, "microcruz-linea41 Verde-ida"); v.DirectionSource != "assumed" || v.Origin != "Urb. La Pascana" {
		t.Errorf("41 Verde ida: %+v", v)
	}
	if p := DecodePolyline(rojoVuelta.Polyline); len(p) != 3 || p[0].Lat != -17.79 || p[2].Lat != -17.78 {
		t.Errorf("41 Rojo vuelta polyline: %v", p)
	}
	verdeIda := variantByID(t, pkg, "microcruz-linea41 Verde-ida")
	if verdeIda.Branch != "Verde" || verdeIda.Color != "#008000" || verdeIda.ColorSource != "name-color-word" {
		t.Errorf("41 Verde ida colour: %+v", verdeIda)
	}

	// Grey placeholder → derived colour; "104B" keeps its id as display.
	l104b := lineByID(t, pkg, "104B")
	if l104b.Display != "104B" || l104b.ColorSource != "derived" || l104b.Color == "#808080" || l104b.Color == "" {
		t.Errorf("line 104B: %+v", l104b)
	}
	v104b := variantByID(t, pkg, "microcruz-linea104B-ida")
	if v104b.Branch != "" || v104b.Origin != "" || v104b.Headsign != "Línea 104B" || len(v104b.Streets) != 1 {
		t.Errorf("104B ida (no colour-file row): %+v", v104b)
	}

	// Route without halves: one variant from the complete polyline; a detail without " - " is headsign only.
	v7 := variantByID(t, pkg, "microcruz-linea7")
	if v7.Direction != "ida" || v7.Origin != "" || v7.Headsign != "Segundo Anillo" || v7.Name != "linea7" || v7.Points != 2 {
		t.Errorf("line 7 variant: %+v", v7)
	}
}

func TestTransfersCompactRoundTrip(t *testing.T) {
	_, microPath := writeSources(t)
	pkg, err := Build(microPath, BuildOptions{Version: 14, TransferM: 150})
	if err != nil {
		t.Fatal(err)
	}
	// 41 Rojo and 104B/7 are far apart; 41 Rojo ida/vuelta share the line and never link to each other.
	// 104B (−17.60/−63.00) and 7 (same points) are different lines within 150 m → links both ways.
	if len(pkg.Transfers) == 0 {
		t.Fatal("expected transfer links between line 7 and line 104B")
	}
	for _, l := range pkg.Transfers {
		a, b := pkg.Variants[l[0]], pkg.Variants[l[2]]
		if a.LineID == b.LineID {
			t.Errorf("link inside one line: %v", l)
		}
		if a.LineID != "7" && a.LineID != "104B" {
			t.Errorf("unexpected link from line %s", a.LineID)
		}
		if l[4] > 150 || l[1] >= a.Points || l[3] >= b.Points {
			t.Errorf("link out of range: %v", l)
		}
	}
	list := pkg.TransferList()
	if len(list) != len(pkg.Transfers) {
		t.Fatalf("expand: %d vs %d", len(list), len(pkg.Transfers))
	}
	// Re-set from the expanded list must give the same compact links.
	before, _ := json.Marshal(pkg.Transfers)
	pkg.SetTransfers(list)
	after, _ := json.Marshal(pkg.Transfers)
	if string(before) != string(after) {
		t.Errorf("round trip changed links:\n%s\n%s", before, after)
	}
	// Empty collections are arrays, never null (the app parser rejects null).
	if rawAll, _ := json.Marshal(pkg); !bytes.Contains(rawAll, []byte(`"stops":[]`)) {
		t.Errorf("stops must marshal as []: %s", rawAll[:200])
	}
	// Wire shape: arrays of five ints.
	var wire struct {
		Transfers [][]int `json:"transfers"`
	}
	raw, _ := json.Marshal(pkg)
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Transfers) == 0 || len(wire.Transfers[0]) != 5 {
		t.Errorf("wire form: err=%v %v", err, wire.Transfers)
	}
}

func TestWriteReleaseManifest(t *testing.T) {
	_, microPath := writeSources(t)
	pkg, err := Build(microPath, BuildOptions{Version: 14})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	man, err := Write(dir, pkg, "1")
	if err != nil {
		t.Fatal(err)
	}
	if man.PackageFormat != PackageFormat || man.Attribution != AttributionNamedRoutes || man.Counts["variants"] != 7 || man.Package.Name != "dataset-v14.json.gz" {
		t.Errorf("manifest: %+v", man)
	}
	if LatestVersion(dir) != 14 {
		t.Errorf("latest.json: %d", LatestVersion(dir))
	}
	cat := NewCatalog(dir)
	m2, err := cat.Latest()
	if err != nil || m2.DatasetVersion != 14 || cat.FilePath(14, "transfers.json") == "" || cat.FilePath(14, "../etc/passwd") != "" {
		t.Errorf("catalog: %v %+v", err, m2)
	}
}

func TestHelpers(t *testing.T) {
	cases := []struct{ name, num, branch string }{
		{"linea41 Rojo", "41", "Rojo"}, {"linea104B", "104B", ""}, {"linea125 UV Doce", "125", "UV Doce"}, {"Línea 7", "7", ""}, {"weird", "x", ""},
	}
	for _, c := range cases {
		n, b := parseRouteName(c.name, "x")
		if n != c.num || b != c.branch {
			t.Errorf("parseRouteName(%q) = %q,%q want %q,%q", c.name, n, b, c.num, c.branch)
		}
	}
	order := []struct {
		a, b string
		less bool
	}{{"9", "10", true}, {"104", "104B", true}, {"104B", "105", true}, {"151", "trufi", true}, {"trufi", "10", false}, {"a", "b", true}}
	for _, o := range order {
		if lineLess(o.a, o.b) != o.less {
			t.Errorf("lineLess(%q,%q) want %v", o.a, o.b, o.less)
		}
	}
	if c, s := resolveColor("#abcdef", "color-file", "1", "micro"); c != "#ABCDEF" || s != "color-file" {
		t.Errorf("resolveColor keep: %s %s", c, s)
	}
	if c, s := resolveColor("#808080", "gray", "1", "micro"); c == "#808080" || s != "derived" {
		t.Errorf("resolveColor gray: %s %s", c, s)
	}
	if c, s := resolveColor("", "", "1", "trufi"); c != hslHex(265, 0.45, 0.40) || s != "derived" {
		t.Errorf("resolveColor trufi: %s %s", c, s)
	}
	if attributionFor("OpenStreetMap via Overpass", "ODbL") != AttributionOSM || attributionFor("Microcruz API", "") != AttributionNamedRoutes {
		t.Error("attributionFor")
	}
}
