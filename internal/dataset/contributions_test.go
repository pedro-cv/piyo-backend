package dataset

import (
	"errors"
	"testing"
	"time"
)

var metro = [4]float64{-18.05, -63.35, -17.60, -62.95}

func poly(pts ...Point) string { return EncodePolyline(pts) }

var (
	pA = Point{Lat: -17.7834, Lng: -63.1821}
	pB = Point{Lat: -17.7900, Lng: -63.1950}
	pC = Point{Lat: -17.8000, Lng: -63.1700}
	pD = Point{Lat: -17.7700, Lng: -63.2000}
)

// basePackage: line 1 (ida/vuelta) and line 2 (ida), crossing at pA.
func basePackage() *Package {
	p := &Package{
		DatasetVersion: 15, PackageFormat: PackageFormat, FareBs: "3.50", License: "test", Attribution: "© test", Source: "test",
		Lines: []Line{
			{LineID: "1", Display: "1", Name: "Línea 1", Mode: "micro", Color: "#112233", VariantIDs: []string{"1-ida", "1-vuelta"}, SourceURL: "https://upstream/1"},
			{LineID: "2", Display: "2", Name: "Línea 2", Mode: "micro", Color: "#445566", VariantIDs: []string{"2-ida"}},
		},
		Stops: []Stop{{StopID: "s1", Lat: pA.Lat, Lng: pA.Lng}},
		Variants: []Variant{
			{VariantID: "1-ida", LineID: "1", Direction: "ida", Headsign: "B", Polyline: poly(pA, pB), StopIDs: []string{"s1"}, Streets: []string{}},
			{VariantID: "1-vuelta", LineID: "1", Direction: "vuelta", Headsign: "A", Polyline: poly(pB, pA), StopIDs: []string{}, Streets: []string{}},
			{VariantID: "2-ida", LineID: "2", Direction: "ida", Headsign: "C", Polyline: poly(pD, pA, pC), StopIDs: []string{}, Streets: []string{}},
		},
	}
	p.SetTransfers(nil)
	return p
}

func addContribution(id, lineID string) Contribution {
	return Contribution{
		ID: id, BaseDatasetVersion: 15,
		Lines:    []ContributedLine{{LineID: lineID, Display: "T9", Name: "Trufi 9", Mode: "trufi", Color: "#aa2233"}},
		Variants: []ContributedVariant{{VariantID: lineID + ":ida", LineID: lineID, Direction: "ida", Headsign: "Plan 3000", Polyline: poly(pC, pA, pD)}},
		Edits:    map[string]ContributedEdit{lineID: {Op: "add", BasedOnVersion: 15}},
	}
}

func updateContribution(id, lineID string, before map[string]string, pts ...Point) Contribution {
	return Contribution{
		ID: id, BaseDatasetVersion: 15,
		Lines:    []ContributedLine{{LineID: lineID, Display: lineID, Name: "Línea " + lineID, Mode: "micro"}},
		Variants: []ContributedVariant{{VariantID: lineID + "-ida", LineID: lineID, Direction: "ida", Headsign: "Nuevo", Polyline: poly(pts...)}},
		Edits:    map[string]ContributedEdit{lineID: {Op: "update", BasedOnVersion: 15, Before: before}},
	}
}

func TestMergeAddAndUpdate(t *testing.T) {
	base := basePackage()
	cs := []Contribution{
		addContribution("c_add", "u-t9-ab12"),
		updateContribution("c_upd", "1", map[string]string{"1-ida": poly(pA, pB)}, pA, pC, pB),
	}
	pkg, rep, err := MergeContributions(base, cs, MergeOptions{Bounds: metro, Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.DatasetVersion != 16 || len(pkg.Lines) != 3 || len(rep.Applied) != 2 || len(rep.Skipped) != 0 {
		t.Fatalf("v%d lines=%d applied=%v skipped=%v", pkg.DatasetVersion, len(pkg.Lines), rep.Applied, rep.Skipped)
	}
	if got := rep.ContributionIDs(); len(got) != 2 || got[0] != "c_add" || got[1] != "c_upd" {
		t.Fatalf("ids %v", got)
	}
	var l1 Line
	for _, l := range pkg.Lines {
		if l.LineID == "1" {
			l1 = l
		}
	}
	// The update replaces every variant of the line: 1-vuelta is gone, 1-ida has the new geometry.
	if len(l1.VariantIDs) != 1 || l1.VariantIDs[0] != "1-ida" || l1.SourceURL != "" {
		t.Fatalf("line 1 after update: %+v", l1)
	}
	for _, v := range pkg.Variants {
		if v.VariantID == "1-vuelta" {
			t.Fatal("old variant kept")
		}
		if v.VariantID == "1-ida" && (v.Points != 3 || len(v.StopIDs) != 0 || v.LengthM == 0) {
			t.Fatalf("1-ida: %+v", v)
		}
		if v.VariantID == "u-t9-ab12:ida" && v.Headsign != "Plan 3000" {
			t.Fatalf("added variant: %+v", v)
		}
	}
	// Stop s1 was only used by the replaced 1-ida, so it is pruned.
	if len(pkg.Stops) != 0 {
		t.Fatalf("stops %v", pkg.Stops)
	}
	if len(pkg.Transfers) == 0 {
		t.Fatal("transfers not recomputed")
	}
	if len(base.Lines) != 2 || len(base.Variants) != 3 {
		t.Fatal("base package modified")
	}
	if rep.LinesAdded[0] != "u-t9-ab12" || rep.LinesUpdated[0] != "1" {
		t.Fatalf("report lines %v %v", rep.LinesAdded, rep.LinesUpdated)
	}
}

func TestMergeSkipsConflicts(t *testing.T) {
	base := basePackage()
	outside := Point{Lat: -16.5, Lng: -68.1}
	cs := []Contribution{
		// The line changed after the contribution was made: its "before" is not today's geometry.
		updateContribution("c_stale", "2", map[string]string{"2-ida": poly(pD, pC)}, pD, pB),
		// "add" with an id that already exists.
		addContribution("c_dup", "1"),
		// A variant id that belongs to another line.
		{ID: "c_steal", Lines: []ContributedLine{{LineID: "u-x", Display: "X"}},
			Variants: []ContributedVariant{{VariantID: "2-ida", LineID: "u-x", Direction: "ida", Polyline: poly(pA, pB)}}},
		// Outside the metro area.
		{ID: "c_out", Lines: []ContributedLine{{LineID: "u-out", Display: "O"}},
			Variants: []ContributedVariant{{VariantID: "u-out:ida", LineID: "u-out", Direction: "ida", Polyline: poly(pA, outside)}}},
		// Applied, then the same line again in this release.
		addContribution("c_first", "u-t9"),
		addContribution("c_second", "u-t9"),
	}
	pkg, rep, err := MergeContributions(base, cs, MergeOptions{Bounds: metro})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 1 || rep.Applied[0].ContributionID != "c_first" {
		t.Fatalf("applied %v", rep.Applied)
	}
	skipped := map[string]bool{}
	for _, s := range rep.Skipped {
		skipped[s.ContributionID] = true
	}
	for _, id := range []string{"c_stale", "c_dup", "c_steal", "c_out", "c_second"} {
		if !skipped[id] {
			t.Errorf("%s not skipped: %v", id, rep.Skipped)
		}
	}
	if len(pkg.Lines) != 3 {
		t.Fatalf("lines %d", len(pkg.Lines))
	}

	// -force applies the stale update and the add that reuses a line id.
	pkg, rep, err = MergeContributions(base, cs[:2], MergeOptions{Bounds: metro, Force: true})
	if err != nil || len(rep.Applied) != 2 || len(pkg.Lines) != 2 {
		t.Fatalf("force: err=%v applied=%v lines=%d", err, rep.Applied, len(pkg.Lines))
	}
}

func TestMergeNothingApplied(t *testing.T) {
	_, rep, err := MergeContributions(basePackage(), []Contribution{addContribution("c_dup", "2")}, MergeOptions{})
	if !errors.Is(err, ErrNothingToMerge) || len(rep.Skipped) != 1 {
		t.Fatalf("err=%v rep=%+v", err, rep)
	}
	if _, _, err := MergeContributions(basePackage(), nil, MergeOptions{Version: 15}); err == nil {
		t.Fatal("same version accepted")
	}
}

func TestValidate(t *testing.T) {
	p := basePackage()
	if err := Validate(p, [4]float64{}); err != nil {
		t.Fatal(err)
	}
	p.Variants = append(p.Variants, p.Variants[0])
	if err := Validate(p, [4]float64{}); err == nil {
		t.Fatal("duplicate variant accepted")
	}
	p = basePackage()
	p.Lines[1].VariantIDs = []string{"1-ida"}
	if err := Validate(p, [4]float64{}); err == nil {
		t.Fatal("variant of another line accepted")
	}
}

func TestWriteReleaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	base := basePackage()
	if _, err := Write(dir, base, "1"); err != nil {
		t.Fatal(err)
	}
	back, man, err := ReadPackage(dir, 15)
	if err != nil || len(back.Variants) != 3 || man.DatasetVersion != 15 || man.Changes != nil {
		t.Fatalf("read base: %v %+v", err, man)
	}
	pkg, rep, err := MergeContributions(back, []Contribution{addContribution("c_1", "u-t9")}, MergeOptions{Bounds: metro})
	if err != nil {
		t.Fatal(err)
	}
	ch := &ReleaseChanges{BaseVersion: 15, ContributionIDs: rep.ContributionIDs(), LinesAdded: rep.LinesAdded, LinesUpdated: rep.LinesUpdated}
	if _, err := WriteRelease(dir, pkg, "1", ch); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRelease(dir, pkg, "1", ch); !errors.Is(err, ErrReleaseExists) {
		t.Fatalf("rewrite of v16: %v", err)
	}
	if LatestVersion(dir) != 16 {
		t.Fatalf("latest %d", LatestVersion(dir))
	}
	m, err := NewCatalog(dir).Manifest(16)
	if err != nil || m.Changes == nil || m.Changes.ContributionIDs[0] != "c_1" || m.Changes.BaseVersion != 15 {
		t.Fatalf("manifest changes: %v %+v", err, m)
	}
	if _, _, err := ReadPackage(dir, 16); err != nil {
		t.Fatal(err)
	}
}
