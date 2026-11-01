// Building a release from the santa-cruz-transit-data source: normalise
// modes, split directions, dedupe stops, encode polylines, compute transfer
// links and assign colours.
//
// Two source layouts are accepted, detected per variant:
//
//   - data/lines.json (OpenStreetMap): one polyline per variant, inferred
//     stops, mode bus|share_taxi, no colours.
//   - data-<provider>/lines.json (named-routes layout): mode token modeNamedRoutes, a colour and
//     color_source per line and variant, no stops, and besides the complete
//     loop polyline the split "ida" and "vuelta" halves. Each named route
//     ("linea41 Rojo") becomes two variants, <id>-ida and <id>-vuelta, with
//     the route name in Name and the suffix in Branch. Origin and headsign
//     come from the colour file next to the source ("A - B") when present.
package dataset

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source types mirror santa-cruz-transit-data/data/lines.json.
type sourceFile struct {
	GeneratedAt string       `json:"generated_at"`
	Source      string       `json:"source"`
	License     string       `json:"license"`
	Lines       []sourceLine `json:"lines"`
}

type sourceLine struct {
	LineID    string          `json:"line_id"`
	Name      string          `json:"name"`
	Mode      string          `json:"mode"`
	Color     string          `json:"color"` // named-routes only
	SourceURL string          `json:"source_url"`
	ScrapedAt string          `json:"scraped_at"`
	Variants  []sourceVariant `json:"variants"`
}

type sourceVariant struct {
	VariantID   string       `json:"variant_id"`
	Name        string       `json:"name"`
	Mode        string       `json:"mode"`
	Direction   *string      `json:"direction"`
	Color       string       `json:"color"`        // named-routes only
	ColorSource string       `json:"color_source"` // named-routes only: color-file | name-color-word | gray
	Stops       []sourceStop `json:"stops"`
	Polyline    [][2]float64 `json:"polyline"`
	Ida         [][2]float64 `json:"ida"`    // named-routes only: outbound half
	Vuelta      [][2]float64 `json:"vuelta"` // named-routes only: return half
	Gaps        int          `json:"gaps"`
	SourceURL   string       `json:"source_url"`
}

// colorRow is one entry of data/lines-and descriptions-with-color.json.
type colorRow struct {
	Text   string `json:"text"`
	Color  string `json:"color"`
	Detail string `json:"line-detail"`
}

// ColorFileName is the colour/description list kept next to the source data.
const ColorFileName = "lines-and descriptions-with-color.json"

// GeocodeFileName maps endpoint names of the colour file to coordinates ({"name": {"lat":…, "lng":…}}).
// It decides which half of a named-routes route is the "ida" towards the headsign; without it the
// colour file's "A - B" order is assumed.
const GeocodeFileName = "endpoints-geocode.json"

type geoPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type sourceStop struct {
	Seq     int     `json:"seq"`
	Name    *string `json:"name"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	OSMNode int64   `json:"osm_node"`
}

// BuildOptions tune the release.
type BuildOptions struct {
	Version    int
	FareBs     string
	TransferM  float64 // max walking distance for a transfer link (default 150)
	MaxPerPair int     // max transfer links kept per variant pair (default 8)
	Now        time.Time
	// ColorsPath is the colour/description file used for origin/headsign.
	// Empty = look for ColorFileName next to the source and in ../data.
	ColorsPath string
	// GeocodePath is the endpoint-name → coordinates file (see GeocodeFileName); same lookup rule.
	GeocodePath string
}

// Build reads the source lines.json and returns the package.
func Build(sourcePath string, opt BuildOptions) (*Package, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	var src sourceFile
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourcePath, err)
	}
	if opt.TransferM <= 0 {
		opt.TransferM = 150
	}
	if opt.MaxPerPair <= 0 {
		opt.MaxPerPair = 8
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now().UTC()
	}
	if opt.FareBs == "" {
		opt.FareBs = "3.50"
	}

	license := strings.TrimSpace(src.License)
	if license == "" {
		license = License
	}
	pkg := &Package{
		DatasetVersion: opt.Version,
		PackageFormat:  PackageFormat,
		GeneratedAt:    opt.Now.UTC(),
		FareBs:         opt.FareBs,
		License:        license,
		Attribution:    attributionFor(src.Source, license),
		Source:         src.Source,
		BBox:           [4]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64},
		// Empty collections marshal as [] (never null): the app parser expects arrays.
		Lines:     []Line{},
		Stops:     []Stop{},
		Variants:  []Variant{},
		Transfers: []TransferLink{},
	}
	details := loadColorFile(opt.ColorsPath, sourcePath)
	geocoded := loadGeocodeFile(opt.GeocodePath, sourcePath)
	stops := map[string]Stop{}
	type decoded struct {
		id     string
		lineID string
		pts    []Point
	}
	var geoms []decoded

	for _, sl := range src.Lines {
		line := Line{
			LineID:          sl.LineID,
			Display:         displayOf(sl),
			Name:            sl.Name,
			Mode:            modeOf(sl.Mode),
			SourceURL:       sl.SourceURL,
			SourceUpdatedAt: sl.ScrapedAt,
		}
		line.Color, line.ColorSource = resolveColor(sl.Color, lineColorSource(sl), line.Display, line.Mode)
		if line.Mode == "trufi" {
			line.FareBs = "" // unknown per-service fare; app falls back to the global tariff
		}
		addVariant := func(v Variant, pts []Point) {
			for _, p := range pts {
				pkg.BBox[0] = math.Min(pkg.BBox[0], p.Lat)
				pkg.BBox[1] = math.Min(pkg.BBox[1], p.Lng)
				pkg.BBox[2] = math.Max(pkg.BBox[2], p.Lat)
				pkg.BBox[3] = math.Max(pkg.BBox[3], p.Lng)
			}
			v.LineID = sl.LineID
			v.Polyline = EncodePolyline(pts)
			v.Points = len(pts)
			v.LengthM = int(math.Round(LengthM(pts)))
			if v.StopIDs == nil {
				v.StopIDs = []string{}
			}
			if len(v.Streets) == 0 {
				if v.Origin != "" {
					v.Streets = append(v.Streets, v.Origin)
				}
				v.Streets = append(v.Streets, v.Headsign)
			}
			line.VariantIDs = append(line.VariantIDs, v.VariantID)
			pkg.Variants = append(pkg.Variants, v)
			geoms = append(geoms, decoded{v.VariantID, sl.LineID, pts})
		}
		for i, sv := range sl.Variants {
			base := Variant{
				VariantID: sv.VariantID,
				Direction: directionLabel(i),
				Gaps:      sv.Gaps,
				SourceURL: sv.SourceURL,
				Streets:   []string{},
			}
			if sv.Color != "" {
				c, cs := resolveColor(sv.Color, sv.ColorSource, line.Display, line.Mode)
				if c != line.Color {
					base.Color, base.ColorSource = c, cs
				}
			}
			if len(sv.Ida) >= 2 && len(sv.Vuelta) >= 2 {
				// named-routes: one named route, two halves.
				num, branch := parseRouteName(sv.Name, sl.LineID)
				base.Name, base.Branch = sv.Name, branch
				origin, headsign := details.endpoints(num, sv.Color)
				if headsign == "" {
					headsign = branchName(sl.Name, branch)
				}
				if origin != "" {
					base.DirectionSource = "assumed"
					if swap, known := geocoded.reversed(origin, headsign, toPoints(sv.Ida)); known {
						base.DirectionSource = "geocoded"
						if swap {
							origin, headsign = headsign, origin
						}
					}
				}
				ida := base
				ida.VariantID, ida.Direction, ida.Origin, ida.Headsign = sv.VariantID+"-ida", "ida", origin, headsign
				addVariant(ida, toPoints(sv.Ida))
				vuelta := base
				vuelta.VariantID, vuelta.Direction = sv.VariantID+"-vuelta", "vuelta"
				if origin != "" {
					vuelta.Origin, vuelta.Headsign = headsign, origin
				} else {
					vuelta.Headsign = headsign
				}
				addVariant(vuelta, toPoints(sv.Vuelta))
				continue
			}
			// OSM (or a named-routes route without split halves): one polyline.
			origin, headsign := parseDirection(sv.Direction, sl.Name)
			if sv.Name != "" && sv.Mode == modeNamedRoutes {
				num, branch := parseRouteName(sv.Name, sl.LineID)
				base.Name, base.Branch = sv.Name, branch
				if origin == "" {
					origin, headsign = details.endpoints(num, sv.Color)
					if headsign == "" {
						headsign = branchName(sl.Name, branch)
					}
				}
			}
			base.Origin, base.Headsign = origin, headsign
			seen := map[string]bool{}
			for _, st := range sv.Stops {
				id := "n" + strconv.FormatInt(st.OSMNode, 10)
				if st.OSMNode == 0 {
					id = fmt.Sprintf("p%.5f_%.5f", st.Lat, st.Lng)
				}
				if _, ok := stops[id]; !ok {
					s := Stop{StopID: id, Lat: st.Lat, Lng: st.Lng}
					if st.Name != nil {
						s.Name = *st.Name
					}
					stops[id] = s
				}
				base.StopIDs = append(base.StopIDs, id)
				if st.Name != nil && !seen[*st.Name] {
					seen[*st.Name] = true
					base.Streets = append(base.Streets, *st.Name)
				}
			}
			addVariant(base, toPoints(sv.Polyline))
		}
		pkg.Lines = append(pkg.Lines, line)
	}

	for _, s := range stops {
		pkg.Stops = append(pkg.Stops, s)
	}
	sort.Slice(pkg.Stops, func(i, j int) bool { return pkg.Stops[i].StopID < pkg.Stops[j].StopID })
	sort.Slice(pkg.Lines, func(i, j int) bool { return lineLess(pkg.Lines[i].Display, pkg.Lines[j].Display) })

	// Transfers via a grid index (cell ≈ transferM).
	pkg.SetTransfers(computeTransfers(func(yield func(id, lineID string, pts []Point)) {
		for _, g := range geoms {
			yield(g.id, g.lineID, g.pts)
		}
	}, opt.TransferM, opt.MaxPerPair))
	return pkg, nil
}

var arrowRe = regexp.MustCompile(`\s*(→|->|-->|—>)\s*`)

// parseDirection splits "A → B" into origin and headsign; falls back to the line name.
func parseDirection(dir *string, lineName string) (origin, headsign string) {
	if dir == nil || strings.TrimSpace(*dir) == "" {
		return "", lineName
	}
	parts := arrowRe.Split(strings.TrimSpace(*dir), 2)
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return "", strings.TrimSpace(*dir)
}

func directionLabel(i int) string {
	switch i {
	case 0:
		return "ida"
	case 1:
		return "vuelta"
	default:
		return "variante"
	}
}

func toPoints(ll [][2]float64) []Point {
	pts := make([]Point, 0, len(ll))
	for _, p := range ll {
		pts = append(pts, Point{p[0], p[1]})
	}
	return pts
}

func modeOf(m string) string {
	switch m {
	case "share_taxi":
		return "trufi"
	case "bus", "minibus", "bus+share_taxi", "bus+minibus", modeNamedRoutes:
		return "micro"
	default:
		return "other"
	}
}

var (
	numRe = regexp.MustCompile(`\d+`)
	// lineIDRe matches ids that are a number with an optional short suffix ("10", "104B").
	lineIDRe = regexp.MustCompile(`^\d+[A-Za-z]{0,2}$`)
	// routeNameRe splits a named-routes route name into number and branch ("linea41 Rojo" -> "41", "Rojo").
	routeNameRe = regexp.MustCompile(`(?i)^l[\x{ed}i]nea\s*(\d+[A-Za-z]?)\s*(.*)$`)
	hexRe       = regexp.MustCompile(`^#[0-9A-F]{6}$`)
)

func displayOf(sl sourceLine) string {
	if lineIDRe.MatchString(sl.LineID) {
		return sl.LineID
	}
	if n := numRe.FindString(sl.Name); n != "" {
		return n
	}
	return sl.LineID
}

// parseRouteName returns the number and branch of a source route name;
// falls back to the line id and no branch.
func parseRouteName(name, lineID string) (num, branch string) {
	if m := routeNameRe.FindStringSubmatch(strings.TrimSpace(name)); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return lineID, ""
}

// branchName is the headsign fallback: "Linea 41 Rojo" / "Linea 41".
func branchName(lineName, branch string) string {
	if branch == "" {
		return lineName
	}
	return lineName + " " + branch
}

// lineColorSource reports the colour source of a line (first variant that has one).
func lineColorSource(sl sourceLine) string {
	for _, v := range sl.Variants {
		if v.ColorSource != "" {
			return v.ColorSource
		}
	}
	return ""
}

// resolveColor keeps a real source colour and derives one otherwise. A grey
// placeholder ("gray" source) is replaced by the derived colour so lines
// without an official colour still look distinct in the app.
func resolveColor(hex, source, display, mode string) (color, colorSource string) {
	hex = strings.ToUpper(strings.TrimSpace(hex))
	if hexRe.MatchString(hex) && source != "gray" {
		if source == "" {
			source = "color-file"
		}
		return hex, source
	}
	return colorFor(display, mode), "derived"
}

// splitLineID splits "104B" into 104 and "B" for ordering.
func splitLineID(s string) (int, string, bool) {
	n := numRe.FindString(s)
	if n == "" || !strings.HasPrefix(s, n) {
		return 0, s, false
	}
	i, err := strconv.Atoi(n)
	if err != nil {
		return 0, s, false
	}
	return i, s[len(n):], true
}

func lineLess(a, b string) bool {
	ai, as, aok := splitLineID(a)
	bi, bs, bok := splitLineID(b)
	if aok && bok {
		if ai != bi {
			return ai < bi
		}
		return as < bs
	}
	if aok {
		return true
	}
	if bok {
		return false
	}
	return a < b
}

// colorDetails indexes the colour file by line number.
type colorDetails map[string][]colorRow

// loadColorFile reads the colour/description list; a missing file is not an error.
func loadColorFile(path, sourcePath string) colorDetails {
	candidates := []string{path}
	if path == "" {
		dir := filepath.Dir(sourcePath)
		candidates = []string{filepath.Join(dir, ColorFileName), filepath.Join(dir, "..", "data", ColorFileName)}
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var rows []colorRow
		if json.Unmarshal(raw, &rows) != nil {
			continue
		}
		out := colorDetails{}
		for _, r := range rows {
			out[strings.TrimSpace(r.Text)] = append(out[strings.TrimSpace(r.Text)], r)
		}
		return out
	}
	return nil
}

// geocodeIndex is the endpoint-name → coordinates file.
type geocodeIndex map[string]geoPoint

func loadGeocodeFile(path, sourcePath string) geocodeIndex {
	candidates := []string{path}
	if path == "" {
		dir := filepath.Dir(sourcePath)
		candidates = []string{filepath.Join(dir, GeocodeFileName), filepath.Join(dir, "..", "data", GeocodeFileName)}
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]*geoPoint
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		out := geocodeIndex{}
		for k, v := range m {
			if v != nil && v.Lat != 0 {
				out[strings.TrimSpace(k)] = *v
			}
		}
		return out
	}
	return nil
}

// reversed reports whether the "ida" half runs from B to A instead of A to B, using the geocoded
// endpoints: positive when the half ends nearer B and starts nearer A. known is false when either
// name has no coordinates or the geometry is ambiguous (score within ±800 m).
func (g geocodeIndex) reversed(a, b string, ida []Point) (swap, known bool) {
	pa, okA := g[a]
	pb, okB := g[b]
	if !okA || !okB || len(ida) < 2 {
		return false, false
	}
	first, last := ida[0], ida[len(ida)-1]
	A := Point{pa.Lat, pa.Lng}
	B := Point{pb.Lat, pb.Lng}
	score := (DistanceM(last, A) - DistanceM(last, B)) + (DistanceM(first, B) - DistanceM(first, A))
	switch {
	case score > 800:
		return false, true
	case score < -800:
		return true, true
	default:
		return false, false
	}
}

// endpoints returns (origin, headsign) for a route from its "A - B" detail.
// When the number has several rows, the one with the same colour wins; a
// detail without " - " (e.g. "Segundo Anillo") is the headsign only.
func (d colorDetails) endpoints(num, color string) (origin, headsign string) {
	rows := d[num]
	if len(rows) == 0 {
		return "", ""
	}
	pick := rows[0]
	if len(rows) > 1 {
		for _, r := range rows {
			if strings.EqualFold(strings.TrimSpace(r.Color), strings.TrimSpace(color)) {
				pick = r
				break
			}
		}
	}
	detail := strings.TrimSpace(pick.Detail)
	if a, b, ok := strings.Cut(detail, " - "); ok && strings.TrimSpace(b) != "" {
		return strings.TrimSpace(a), strings.TrimSpace(b)
	}
	return "", detail
}

// colorFor mirrors the app's LineColors: known hues, else (n*47)%360, HSL 62% 38%.
func colorFor(display, mode string) string {
	if mode == "trufi" {
		return hslHex(265, 0.45, 0.40)
	}
	known := map[string]float64{"76": 4, "98": 150, "103": 210, "21": 30, "92": 340, "9": 120, "111": 200, "8": 265, "81": 45}
	h, ok := known[display]
	if !ok {
		n, err := strconv.Atoi(numRe.FindString(display))
		if err != nil {
			n = int(hash32(display))
		}
		h = float64((n * 47) % 360)
	}
	return hslHex(h, 0.62, 0.38)
}

func hash32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h % 360
}

func hslHex(h, s, l float64) string {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round((r+m)*255)), int(math.Round((g+m)*255)), int(math.Round((b+m)*255)))
}

// computeTransfers finds, for every pair of variants of different lines,
// up to maxPerPair walking links of at most maxM metres, spread along the
// first variant (local minima at least 12 indices apart).
func computeTransfers(each func(yield func(id, lineID string, pts []Point)), maxM float64, maxPerPair int) []Transfer {
	type entry struct {
		vi, idx int
	}
	type variant struct {
		id, line string
		pts      []Point
	}
	var vars []variant
	each(func(id, lineID string, pts []Point) { vars = append(vars, variant{id, lineID, pts}) })

	cell := maxM / 111320.0 // degrees of latitude per cell
	key := func(p Point) [2]int {
		return [2]int{int(math.Floor(p.Lat / cell)), int(math.Floor(p.Lng / (cell / math.Cos(p.Lat*math.Pi/180))))}
	}
	grid := map[[2]int][]entry{}
	for vi, v := range vars {
		for i, p := range v.pts {
			k := key(p)
			grid[k] = append(grid[k], entry{vi, i})
		}
	}

	type cand struct {
		i, j int
		d    float64
	}
	pairs := map[[2]int][]cand{}
	for a, va := range vars {
		for i, p := range va.pts {
			k := key(p)
			for dx := -1; dx <= 1; dx++ {
				for dy := -1; dy <= 1; dy++ {
					for _, e := range grid[[2]int{k[0] + dx, k[1] + dy}] {
						if e.vi == a || vars[e.vi].line == va.line {
							continue
						}
						d := DistanceM(p, vars[e.vi].pts[e.idx])
						if d <= maxM {
							pk := [2]int{a, e.vi}
							pairs[pk] = append(pairs[pk], cand{i, e.idx, d})
						}
					}
				}
			}
		}
	}

	var out []Transfer
	keys := make([][2]int, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		cs := pairs[k]
		sort.Slice(cs, func(i, j int) bool { return cs[i].d < cs[j].d })
		var kept []cand
		for _, c := range cs {
			ok := true
			for _, kc := range kept {
				if abs(kc.i-c.i) < 12 {
					ok = false
					break
				}
			}
			if ok {
				kept = append(kept, c)
				if len(kept) >= maxPerPair {
					break
				}
			}
		}
		for _, c := range kept {
			out = append(out, Transfer{From: vars[k[0]].id, FromIdx: c.i, To: vars[k[1]].id, ToIdx: c.j, WalkM: int(math.Round(c.d))})
		}
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
