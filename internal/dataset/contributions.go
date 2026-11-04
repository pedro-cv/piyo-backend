// Building release vN+1 from release vN plus accepted route contributions
// (docs/implementation/CONTRIBUTIONS_DESIGN.md §6). A contribution carries
// whole lines in the package shapes the app writes (DatasetWriter): an "add"
// creates a line, an "update" replaces every variant of an existing line. The
// original polylines the user started from ("before") detect lines that
// changed in a release published after the contribution was made; those are
// skipped unless forced, so a reviewer never overwrites newer official data
// by accident. Lines are never deleted by a merge.
package dataset

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ContributedLine / ContributedVariant / ContributedEdit mirror the payload
// stored with a contribution (POST /v1/contributions).
type ContributedLine struct {
	LineID  string `json:"line_id"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Color   string `json:"color"`
	FareBs  string `json:"fare_bs"`
}

type ContributedVariant struct {
	VariantID string   `json:"variant_id"`
	LineID    string   `json:"line_id"`
	Name      string   `json:"name"`
	Branch    string   `json:"branch"`
	Direction string   `json:"direction"`
	Headsign  string   `json:"headsign"`
	Origin    string   `json:"origin"`
	Color     string   `json:"color"`
	Polyline  string   `json:"polyline"`
	Streets   []string `json:"streets"`
}

type ContributedEdit struct {
	Op             string            `json:"op"` // add | update
	BasedOnVersion int               `json:"based_on_version"`
	Before         map[string]string `json:"before"` // variant_id -> original polyline
}

// Contribution is one accepted contribution to merge: its id and payload.
type Contribution struct {
	ID                 string                     `json:"id"`
	BaseDatasetVersion int                        `json:"base_dataset_version"`
	Lines              []ContributedLine          `json:"lines"`
	Variants           []ContributedVariant       `json:"variants"`
	Edits              map[string]ContributedEdit `json:"edits"`
}

// ColorSourceContribution marks a colour set by a contribution.
const ColorSourceContribution = "contribution"

// MergeOptions configures MergeContributions.
type MergeOptions struct {
	Version    int        // version of the new release (default base + 1)
	Force      bool       // apply contributions whose line changed since they were made
	TransferM  float64    // max walking distance of a transfer link (default 150)
	MaxPerPair int        // max transfer links per pair of variants (default 4)
	Bounds     [4]float64 // minLat, minLng, maxLat, maxLng every point must be inside (zero = no check)
	Now        time.Time  // generated_at (default time.Now)
}

// MergeReport says what a merge did, for the CLI output and the release notes.
type MergeReport struct {
	BaseVersion  int            `json:"base_version"`
	Version      int            `json:"version"`
	Applied      []MergeOutcome `json:"applied"`
	Skipped      []MergeOutcome `json:"skipped"`
	Warnings     []string       `json:"warnings,omitempty"`
	LinesAdded   []string       `json:"lines_added"`
	LinesUpdated []string       `json:"lines_updated"`
	CountsBefore map[string]int `json:"counts_before"`
	CountsAfter  map[string]int `json:"counts_after"`
}

// MergeOutcome is one contribution line applied or skipped.
type MergeOutcome struct {
	ContributionID string `json:"contribution_id"`
	LineID         string `json:"line_id"`
	Op             string `json:"op,omitempty"`
	Variants       int    `json:"variants,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// ContributionIDs returns the ids of the contributions with at least one line applied, in order.
func (r *MergeReport) ContributionIDs() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range r.Applied {
		if !seen[a.ContributionID] {
			seen[a.ContributionID] = true
			out = append(out, a.ContributionID)
		}
	}
	return out
}

// ErrNothingToMerge is returned when no contribution could be applied.
var ErrNothingToMerge = errors.New("dataset: no contribution could be applied")

func packageCounts(p *Package) map[string]int {
	return map[string]int{"lines": len(p.Lines), "variants": len(p.Variants), "stops": len(p.Stops), "transfers": len(p.Transfers)}
}

// MergeContributions returns a new package: base with the contributions
// applied in order. base is not modified. The result is validated.
func MergeContributions(base *Package, cs []Contribution, opt MergeOptions) (*Package, *MergeReport, error) {
	if opt.Version == 0 {
		opt.Version = base.DatasetVersion + 1
	}
	if opt.Version <= base.DatasetVersion {
		return nil, nil, fmt.Errorf("dataset: version %d must be newer than the base v%d", opt.Version, base.DatasetVersion)
	}
	if opt.TransferM <= 0 {
		opt.TransferM = 150
	}
	if opt.MaxPerPair <= 0 {
		opt.MaxPerPair = 4
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	rep := &MergeReport{BaseVersion: base.DatasetVersion, Version: opt.Version, CountsBefore: packageCounts(base),
		Applied: []MergeOutcome{}, Skipped: []MergeOutcome{}, LinesAdded: []string{}, LinesUpdated: []string{}}

	lines := make(map[string]Line, len(base.Lines))
	order := make([]string, 0, len(base.Lines))
	for _, l := range base.Lines {
		lines[l.LineID] = l
		order = append(order, l.LineID)
	}
	variants := make(map[string]Variant, len(base.Variants))
	for _, v := range base.Variants {
		variants[v.VariantID] = v
	}
	touched := map[string]string{} // line id -> contribution that changed it in this merge

	for _, c := range cs {
		byLine := map[string][]ContributedVariant{}
		for _, v := range c.Variants {
			byLine[v.LineID] = append(byLine[v.LineID], v)
		}
		for _, cl := range c.Lines {
			id := strings.TrimSpace(cl.LineID)
			skip := func(reason string) {
				rep.Skipped = append(rep.Skipped, MergeOutcome{ContributionID: c.ID, LineID: id, Reason: reason})
			}
			if by, ok := touched[id]; ok {
				skip("line already changed by " + by + " in this release")
				continue
			}
			edit := c.Edits[id]
			op := edit.Op
			if op == "" {
				op = "add"
			}
			old, exists := lines[id]
			switch {
			case op == "add" && exists && !opt.Force:
				skip("line id already exists in v" + strconv.Itoa(base.DatasetVersion) + " (use -force to replace it)")
				continue
			case op == "update" && !exists:
				rep.Warnings = append(rep.Warnings, c.ID+": line "+id+" is an update but is not in v"+strconv.Itoa(base.DatasetVersion)+"; added as a new line")
			case op == "update" && exists:
				if stale := staleVariants(old, variants, edit.Before); len(stale) > 0 && !opt.Force {
					skip("line changed after v" + strconv.Itoa(edit.BasedOnVersion) + " (variants " + strings.Join(stale, ", ") + "); review it again or use -force")
					continue
				}
				if len(edit.Before) == 0 && c.BaseDatasetVersion < base.DatasetVersion {
					rep.Warnings = append(rep.Warnings, c.ID+": line "+id+" was based on v"+strconv.Itoa(c.BaseDatasetVersion)+" without original geometry; cannot detect newer changes")
				}
			}
			newVariants, reason := buildContributedVariants(id, byLine[id], variants, old, opt.Bounds)
			if reason != "" {
				skip(reason)
				continue
			}
			line := contributedLine(cl, old, exists)
			line.VariantIDs = nil
			if exists {
				for _, vid := range old.VariantIDs {
					delete(variants, vid)
				}
			}
			for _, v := range newVariants {
				variants[v.VariantID] = v
				line.VariantIDs = append(line.VariantIDs, v.VariantID)
			}
			if !exists {
				order = append(order, id)
				rep.LinesAdded = append(rep.LinesAdded, id)
			} else {
				rep.LinesUpdated = append(rep.LinesUpdated, id)
			}
			lines[id] = line
			touched[id] = c.ID
			appliedOp := "update"
			if !exists {
				appliedOp = "add"
			}
			rep.Applied = append(rep.Applied, MergeOutcome{ContributionID: c.ID, LineID: id, Op: appliedOp, Variants: len(newVariants)})
		}
	}
	if len(rep.Applied) == 0 {
		return nil, rep, ErrNothingToMerge
	}

	pkg := &Package{
		DatasetVersion: opt.Version, PackageFormat: PackageFormat, GeneratedAt: opt.Now.UTC(),
		FareBs: base.FareBs, License: base.License, Attribution: base.Attribution, Source: base.Source,
	}
	usedStops := map[string]bool{}
	for _, id := range order {
		l := lines[id]
		pkg.Lines = append(pkg.Lines, l)
		for _, vid := range l.VariantIDs {
			v := variants[vid]
			pkg.Variants = append(pkg.Variants, v)
			for _, s := range v.StopIDs {
				usedStops[s] = true
			}
		}
	}
	sort.SliceStable(pkg.Lines, func(i, j int) bool { return lineLess(pkg.Lines[i].Display, pkg.Lines[j].Display) })
	for _, s := range base.Stops {
		if usedStops[s.StopID] {
			pkg.Stops = append(pkg.Stops, s)
		}
	}
	if pkg.Stops == nil {
		pkg.Stops = []Stop{}
	}
	pkg.BBox = [4]float64{90, 180, -90, -180}
	decoded := make([][]Point, len(pkg.Variants))
	for i, v := range pkg.Variants {
		decoded[i] = DecodePolyline(v.Polyline)
		for _, p := range decoded[i] {
			pkg.BBox[0] = min(pkg.BBox[0], p.Lat)
			pkg.BBox[1] = min(pkg.BBox[1], p.Lng)
			pkg.BBox[2] = max(pkg.BBox[2], p.Lat)
			pkg.BBox[3] = max(pkg.BBox[3], p.Lng)
		}
	}
	pkg.SetTransfers(computeTransfers(func(yield func(id, lineID string, pts []Point)) {
		for i, v := range pkg.Variants {
			yield(v.VariantID, v.LineID, decoded[i])
		}
	}, opt.TransferM, opt.MaxPerPair))
	rep.CountsAfter = packageCounts(pkg)
	// Bounds apply to contributed geometry only (checked above): official releases may reach past METRO_BOUNDS.
	if err := Validate(pkg, [4]float64{}); err != nil {
		return nil, rep, err
	}
	if rep.CountsAfter["lines"] < rep.CountsBefore["lines"] {
		return nil, rep, fmt.Errorf("dataset: merge dropped lines (%d -> %d)", rep.CountsBefore["lines"], rep.CountsAfter["lines"])
	}
	return pkg, rep, nil
}

// staleVariants lists the variants of old whose current polyline differs from
// the "before" the contribution was made against.
func staleVariants(old Line, variants map[string]Variant, before map[string]string) []string {
	var out []string
	for vid, poly := range before {
		cur, ok := variants[vid]
		if !ok || !samePolyline(cur.Polyline, poly) {
			out = append(out, vid)
		}
	}
	sort.Strings(out)
	return out
}

// samePolyline compares decoded geometry, so two encodings of the same points match.
func samePolyline(a, b string) bool {
	if a == b {
		return true
	}
	pa, pb := DecodePolyline(a), DecodePolyline(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if DistanceM(pa[i], pb[i]) > 1 {
			return false
		}
	}
	return true
}

func normMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "trufi":
		return "trufi"
	case "micro", "":
		return "micro"
	default:
		return "other"
	}
}

func contributedLine(cl ContributedLine, old Line, exists bool) Line {
	l := Line{LineID: strings.TrimSpace(cl.LineID)}
	if exists {
		l = old
	}
	l.Display = strings.TrimSpace(cl.Display)
	if n := strings.TrimSpace(cl.Name); n != "" || !exists {
		l.Name = n
	}
	l.Mode = normMode(cl.Mode)
	switch {
	case cl.Color != "" && !strings.EqualFold(cl.Color, l.Color):
		l.Color, l.ColorSource = strings.ToUpper(cl.Color), ColorSourceContribution
	case l.Color == "":
		l.Color, l.ColorSource = colorFor(l.Display, l.Mode), "derived"
	}
	if cl.FareBs != "" {
		l.FareBs = cl.FareBs
	}
	// The upstream page no longer describes this geometry.
	l.SourceURL = ""
	l.SourceUpdatedAt = ""
	return l
}

func buildContributedVariants(lineID string, cvs []ContributedVariant, existing map[string]Variant, old Line, bounds [4]float64) ([]Variant, string) {
	if len(cvs) == 0 {
		return nil, "line has no variants"
	}
	own := map[string]bool{}
	for _, vid := range old.VariantIDs {
		own[vid] = true
	}
	seen := map[string]bool{}
	out := make([]Variant, 0, len(cvs))
	for _, cv := range cvs {
		vid := strings.TrimSpace(cv.VariantID)
		switch {
		case vid == "":
			return nil, "a variant has no variant_id"
		case seen[vid]:
			return nil, "duplicate variant " + vid
		case existing[vid].VariantID != "" && !own[vid]:
			return nil, "variant " + vid + " belongs to line " + existing[vid].LineID
		case cv.Direction != "ida" && cv.Direction != "vuelta" && cv.Direction != "variante":
			return nil, "variant " + vid + ": direction must be ida, vuelta or variante"
		}
		seen[vid] = true
		pts := DecodePolyline(cv.Polyline)
		if len(pts) < 2 {
			return nil, "variant " + vid + ": polyline needs at least 2 points"
		}
		if bounds != ([4]float64{}) {
			for _, p := range pts {
				if p.Lat < bounds[0] || p.Lng < bounds[1] || p.Lat > bounds[2] || p.Lng > bounds[3] {
					return nil, "variant " + vid + ": a point is outside the metro area"
				}
			}
		}
		v := Variant{
			VariantID: vid, LineID: lineID, Name: cv.Name, Branch: cv.Branch, Direction: cv.Direction,
			Headsign: strings.TrimSpace(cv.Headsign), Origin: strings.TrimSpace(cv.Origin),
			Polyline: EncodePolyline(pts), Points: len(pts), LengthM: int(LengthM(pts)),
			StopIDs: []string{}, Streets: cv.Streets,
		}
		if v.Streets == nil {
			v.Streets = []string{}
		}
		if cv.Color != "" {
			v.Color, v.ColorSource = strings.ToUpper(cv.Color), ColorSourceContribution
		}
		out = append(out, v)
	}
	return out, ""
}

// Validate checks the invariants the app and the planner rely on: unique
// line and variant ids, every line has variants that point back to it, every
// polyline has at least two points (inside bounds when given), transfer links
// and stop references resolve.
func Validate(p *Package, bounds [4]float64) error {
	var problems []string
	add := func(format string, a ...any) {
		if len(problems) < 20 {
			problems = append(problems, fmt.Sprintf(format, a...))
		}
	}
	if p.DatasetVersion <= 0 {
		add("dataset_version must be > 0")
	}
	if len(p.Lines) == 0 {
		add("no lines")
	}
	vByID := make(map[string]*Variant, len(p.Variants))
	for i := range p.Variants {
		v := &p.Variants[i]
		if vByID[v.VariantID] != nil {
			add("duplicate variant %s", v.VariantID)
		}
		vByID[v.VariantID] = v
		pts := DecodePolyline(v.Polyline)
		if len(pts) < 2 {
			add("variant %s: polyline has %d points", v.VariantID, len(pts))
		}
		if bounds != ([4]float64{}) {
			for _, pt := range pts {
				if pt.Lat < bounds[0] || pt.Lng < bounds[1] || pt.Lat > bounds[2] || pt.Lng > bounds[3] {
					add("variant %s: point %.5f,%.5f outside bounds", v.VariantID, pt.Lat, pt.Lng)
					break
				}
			}
		}
		switch v.Direction {
		case "ida", "vuelta", "variante":
		default:
			add("variant %s: direction %q", v.VariantID, v.Direction)
		}
	}
	seenLine := map[string]bool{}
	claimed := map[string]string{}
	for _, l := range p.Lines {
		if l.LineID == "" {
			add("a line has no line_id")
		}
		if seenLine[l.LineID] {
			add("duplicate line %s", l.LineID)
		}
		seenLine[l.LineID] = true
		if strings.TrimSpace(l.Display) == "" {
			add("line %s: empty display", l.LineID)
		}
		if len(l.VariantIDs) == 0 {
			add("line %s: no variants", l.LineID)
		}
		for _, vid := range l.VariantIDs {
			v := vByID[vid]
			switch {
			case v == nil:
				add("line %s: variant %s missing", l.LineID, vid)
			case v.LineID != l.LineID:
				add("line %s: variant %s belongs to %s", l.LineID, vid, v.LineID)
			case claimed[vid] != "":
				add("variant %s listed by %s and %s", vid, claimed[vid], l.LineID)
			}
			claimed[vid] = l.LineID
		}
	}
	for _, v := range p.Variants {
		if claimed[v.VariantID] == "" {
			add("variant %s is not listed by any line", v.VariantID)
		}
	}
	stops := make(map[string]bool, len(p.Stops))
	for _, s := range p.Stops {
		stops[s.StopID] = true
	}
	for _, v := range p.Variants {
		for _, s := range v.StopIDs {
			if !stops[s] {
				add("variant %s: stop %s missing", v.VariantID, s)
				break
			}
		}
	}
	for i, t := range p.Transfers {
		if t[0] < 0 || t[0] >= len(p.Variants) || t[2] < 0 || t[2] >= len(p.Variants) {
			add("transfer %d points outside the variants array", i)
			break
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("dataset v%d invalid: %s", p.DatasetVersion, strings.Join(problems, "; "))
	}
	return nil
}

// ReadPackage loads release v from dir and checks the package against the
// manifest checksum.
func ReadPackage(dir string, v int) (*Package, *Manifest, error) {
	vdir := filepath.Join(dir, "v"+strconv.Itoa(v))
	mb, err := os.ReadFile(filepath.Join(vdir, "manifest.json"))
	if err != nil {
		return nil, nil, fmt.Errorf("release v%d: %w", v, err)
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, nil, fmt.Errorf("release v%d manifest: %w", v, err)
	}
	raw, err := os.ReadFile(filepath.Join(vdir, m.Package.Name))
	if err != nil {
		return nil, nil, fmt.Errorf("release v%d package: %w", v, err)
	}
	sum := sha256.Sum256(raw)
	if m.Package.SHA256 != "" && hex.EncodeToString(sum[:]) != m.Package.SHA256 {
		return nil, nil, fmt.Errorf("release v%d: %s does not match the manifest sha256", v, m.Package.Name)
	}
	var r io.Reader = bytes.NewReader(raw)
	if strings.HasSuffix(m.Package.Name, ".gz") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		defer zr.Close()
		r = zr
	}
	var p Package
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, nil, fmt.Errorf("release v%d package: %w", v, err)
	}
	return &p, &m, nil
}

// ErrReleaseExists is returned by WriteRelease for a version already on disk.
var ErrReleaseExists = errors.New("dataset: release already exists (releases are immutable)")

// WriteRelease is Write for a new release with a record of what changed;
// unlike Write it refuses to touch an existing version.
func WriteRelease(dir string, pkg *Package, minAppVersion string, changes *ReleaseChanges) (*Manifest, error) {
	if _, err := os.Stat(filepath.Join(dir, "v"+strconv.Itoa(pkg.DatasetVersion))); err == nil {
		return nil, ErrReleaseExists
	}
	return write(dir, pkg, minAppVersion, changes)
}
