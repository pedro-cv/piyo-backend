// Package dataset defines the published transit data format (see
// docs/implementation/DATA_MODEL.md): a versioned, immutable set of JSON
// files plus one gzip package and a manifest with checksums. It also builds
// a release from the santa-cruz-transit-data source files and serves
// releases from a directory.
package dataset

import (
	"encoding/json"
	"time"
)

// License is the licence used when the source file does not state one (OSM releases).
const License = "ODbL 1.0 — © OpenStreetMap contributors"

// AttributionOSM is the credit shown for OSM-derived releases; the named-routes
// family is defined in sources.go.
const AttributionOSM = "© OpenStreetMap contributors"

// Line is route metadata (lines.json).
type Line struct {
	LineID  string `json:"line_id"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Mode    string `json:"mode"` // micro | trufi | other
	Color   string `json:"color"`
	// ColorSource says where Color comes from: "color-file" (official colour
	// list), "name-color-word" (a colour word in the route name), "derived"
	// (computed from the number, also used when the source only had a grey
	// placeholder) or "" for releases built before this field existed.
	ColorSource string   `json:"color_source,omitempty"`
	FareBs      string   `json:"fare_bs,omitempty"`
	VariantIDs  []string `json:"variant_ids"`
	SourceURL   string   `json:"source_url,omitempty"`
	// SourceUpdatedAt is the source's scraped_at for this line (RFC 3339), when known.
	SourceUpdatedAt string `json:"source_updated_at,omitempty"`
}

// Stop is a request stop shared by variants (stops.json).
type Stop struct {
	StopID string  `json:"stop_id"`
	Name   string  `json:"name,omitempty"`
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
}

// Variant is one direction of a line (variants.json). Polyline is encoded
// with the Google polyline algorithm (precision 1e-5).
type Variant struct {
	VariantID string `json:"variant_id"`
	LineID    string `json:"line_id"`
	// Name is the route name in the source ("linea41 Rojo"); Branch is the
	// part after the number ("Rojo") that tells two routes of the same line
	// apart. Both are empty for sources without named branches (OSM).
	Name      string `json:"name,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Direction string `json:"direction"` // ida | vuelta | variante
	Headsign  string `json:"headsign"`
	Origin    string `json:"origin,omitempty"`
	// DirectionSource says how Origin/Headsign were matched to this half of the route: "geocoded"
	// (both endpoint names geocoded and compared with the geometry), "assumed" (the colour file's
	// "A - B" order taken as ida), or "" when the names come from the source itself (OSM from/to).
	DirectionSource string `json:"direction_source,omitempty"`
	// Color overrides the line colour for this variant when set (same vocabulary as Line.ColorSource).
	Color       string   `json:"color,omitempty"`
	ColorSource string   `json:"color_source,omitempty"`
	Polyline    string   `json:"polyline"`
	Points      int      `json:"points"`
	LengthM     int      `json:"length_m"`
	StopIDs     []string `json:"stop_ids"`
	Streets     []string `json:"streets"`
	Gaps        int      `json:"gaps,omitempty"`
	SourceURL   string   `json:"source_url,omitempty"`
}

// Transfer is a precomputed walking link between two variants: ride variant
// From to index FromIdx, walk WalkM metres, board variant To at index ToIdx.
// This is the in-memory form; on the wire a link is a TransferLink.
type Transfer struct {
	From    string `json:"from"`
	FromIdx int    `json:"from_idx"`
	To      string `json:"to"`
	ToIdx   int    `json:"to_idx"`
	WalkM   int    `json:"walk_m"`
}

// TransferLink is the compact wire form of a Transfer (package format 2):
// [from_variant, from_idx, to_variant, to_idx, walk_m], where from_variant
// and to_variant are positions in the package's variants array. With ~300
// variants nearly every pair of routes meets somewhere, so ~100k links
// exist; as objects with variant ids they were 11 MB, as links 2 MB.
type TransferLink [5]int

// PackageFormat is the current wire format: 2 = compact transfer links,
// per-variant name/branch/colour, attribution. Format 1 (iteration 2) had
// transfers as objects with variant ids.
const PackageFormat = 2

// Package is the whole release in one document (dataset-vN.json[.gz]).
type Package struct {
	DatasetVersion int       `json:"dataset_version"`
	PackageFormat  int       `json:"package_format"`
	GeneratedAt    time.Time `json:"generated_at"`
	FareBs         string    `json:"fare_bs"`
	License        string    `json:"license"`
	// Attribution is the short credit the app shows (e.g. "© OpenStreetMap contributors").
	Attribution string         `json:"attribution,omitempty"`
	Source      string         `json:"source"`
	BBox        [4]float64     `json:"bbox"` // minLat, minLng, maxLat, maxLng
	Lines       []Line         `json:"lines"`
	Stops       []Stop         `json:"stops"`
	Variants    []Variant      `json:"variants"`
	Transfers   []TransferLink `json:"transfers"`
}

// SetTransfers stores links in the compact form, resolving variant ids to
// positions; links whose variant is not in the package are dropped.
func (p *Package) SetTransfers(ts []Transfer) {
	idx := make(map[string]int, len(p.Variants))
	for i, v := range p.Variants {
		idx[v.VariantID] = i
	}
	p.Transfers = make([]TransferLink, 0, len(ts))
	for _, t := range ts {
		a, okA := idx[t.From]
		b, okB := idx[t.To]
		if !okA || !okB {
			continue
		}
		p.Transfers = append(p.Transfers, TransferLink{a, t.FromIdx, b, t.ToIdx, t.WalkM})
	}
}

// TransferList expands the compact links back to Transfers with variant ids.
func (p *Package) TransferList() []Transfer {
	out := make([]Transfer, 0, len(p.Transfers))
	for _, l := range p.Transfers {
		if l[0] < 0 || l[0] >= len(p.Variants) || l[2] < 0 || l[2] >= len(p.Variants) {
			continue
		}
		out = append(out, Transfer{From: p.Variants[l[0]].VariantID, FromIdx: l[1], To: p.Variants[l[2]].VariantID, ToIdx: l[3], WalkM: l[4]})
	}
	return out
}

// FileInfo describes one published file.
type FileInfo struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	URL    string `json:"url,omitempty"`
}

// Manifest is what the app fetches first (manifest.json). The legacy
// top-level data_version/url/bytes/sha256 fields mirror the package so the
// first-iteration app keeps working.
type Manifest struct {
	DatasetVersion int            `json:"dataset_version"`
	PackageFormat  int            `json:"package_format"`
	DataVersion    int            `json:"data_version"`
	GeneratedAt    time.Time      `json:"generated_at"`
	MinAppVersion  string         `json:"min_app_version"`
	FareBs         string         `json:"fare_bs"`
	License        string         `json:"license"`
	Attribution    string         `json:"attribution,omitempty"`
	Source         string         `json:"source,omitempty"`
	BBox           [4]float64     `json:"bbox"`
	Counts         map[string]int `json:"counts"`
	Package        FileInfo       `json:"package"`
	Files          []FileInfo     `json:"files"`
	Message        string         `json:"message,omitempty"`
	// Legacy mirrors of Package.
	URL    string `json:"url"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// WithBase returns a copy of the manifest whose file URLs are absolute under
// base (e.g. "https://piyo.example/v1/datasets/13").
func (m Manifest) WithBase(base string) Manifest {
	out := m
	out.Package.URL = base + "/" + m.Package.Name
	out.URL = out.Package.URL
	out.Files = make([]FileInfo, len(m.Files))
	for i, f := range m.Files {
		f.URL = base + "/" + f.Name
		out.Files[i] = f
	}
	return out
}

// MarshalIndent is a small helper for the importer's output.
func MarshalIndent(v any) ([]byte, error) { return json.MarshalIndent(v, "", " ") }
