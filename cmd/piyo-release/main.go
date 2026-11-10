// Command piyo-release builds the next dataset release from the latest one
// plus the accepted route contributions that no release includes yet
// (docs/implementation/CONTRIBUTIONS_DESIGN.md §6).
//
//	ADMIN_TOKEN=... piyo-release -api https://<api> [-releases ./releases] [-ids c_1,c_2] [-dry-run] [-force]
//	piyo-release -contributions accepted.json [-releases ./releases] [-dry-run]
//
// It reads releases/vN, fetches the contributions (GET
// /v1/admin/contributions?status=accepted&published=false, then each one with
// its payload) or reads them from a file (a JSON array of what GET
// /v1/admin/contributions/{id} returns under "contribution"), applies them,
// validates the result and writes releases/v(N+1) with the list of included
// contributions in manifest.json "changes". Nothing on the server changes:
// deploy the new release, then call POST /v1/admin/datasets/{N+1}/publish to
// mark the contributions published and notify their senders.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"piyo-backend/internal/config"
	"piyo-backend/internal/dataset"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "piyo-release:", err)
		os.Exit(1)
	}
}

// contribution is the admin API shape; only what the merge needs.
type contribution struct {
	ID                 string          `json:"id"`
	Status             string          `json:"status"`
	BaseDatasetVersion int             `json:"base_dataset_version"`
	RouteIDs           []string        `json:"route_ids"`
	PublishedVersion   int             `json:"published_version"`
	Payload            json.RawMessage `json:"payload"`
}

func run() error {
	var (
		releases = flag.String("releases", envOr("RELEASES_DIR", "./releases"), "releases directory (env RELEASES_DIR)")
		apiBase  = flag.String("api", os.Getenv("PIYO_API"), "API base URL to fetch accepted contributions from (env PIYO_API); needs ADMIN_TOKEN")
		file     = flag.String("contributions", "", "read contributions from this JSON file instead of the API")
		idsFlag  = flag.String("ids", "", "comma-separated contribution ids to include (default: every accepted, unpublished one)")
		version  = flag.Int("version", 0, "version to produce (0 = latest + 1)")
		force    = flag.Bool("force", false, "apply contributions whose line changed after they were made, and adds that reuse a line id")
		dryRun   = flag.Bool("dry-run", false, "merge and validate, print the report, write nothing")
		notes    = flag.String("notes", "", "release notes stored in manifest.json changes.notes")
		minApp   = flag.String("min-app", envOr("MIN_APP_VERSION", "1"), "minimum supported app version")
		bounds   = flag.String("bounds", envOr("METRO_BOUNDS", config.DefaultMetroBounds), "minLat,minLng,maxLat,maxLng every point must be inside (env METRO_BOUNDS)")
		report   = flag.String("report", "", "also write the merge report as JSON to this file")
	)
	flag.Parse()

	b, err := config.ParseBounds(*bounds)
	if err != nil {
		return fmt.Errorf("bounds: %w", err)
	}
	baseV := dataset.LatestVersion(*releases)
	if baseV == 0 {
		return fmt.Errorf("no release in %s: build one with piyo-import first", *releases)
	}
	base, _, err := dataset.ReadPackage(*releases, baseV)
	if err != nil {
		return err
	}

	var list []contribution
	switch {
	case *file != "":
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return fmt.Errorf("%s: %w", *file, err)
		}
	case *apiBase != "":
		token := os.Getenv("ADMIN_TOKEN")
		if token == "" {
			return errors.New("ADMIN_TOKEN is not set (export it; it is never printed)")
		}
		if list, err = fetchAccepted(strings.TrimRight(*apiBase, "/"), token); err != nil {
			return err
		}
	default:
		return errors.New("give -api <url> (with ADMIN_TOKEN) or -contributions <file>")
	}

	want := map[string]bool{}
	for _, id := range strings.Split(*idsFlag, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	var cs []dataset.Contribution
	for _, c := range list {
		if len(want) > 0 && !want[c.ID] {
			continue
		}
		if c.Status != "" && c.Status != "accepted" {
			fmt.Printf("  skip %s: status %s\n", c.ID, c.Status)
			continue
		}
		if c.PublishedVersion > 0 {
			fmt.Printf("  skip %s: already in v%d\n", c.ID, c.PublishedVersion)
			continue
		}
		var dc dataset.Contribution
		if err := json.Unmarshal(c.Payload, &dc); err != nil {
			return fmt.Errorf("contribution %s payload: %w", c.ID, err)
		}
		dc.ID = c.ID
		if dc.BaseDatasetVersion == 0 {
			dc.BaseDatasetVersion = c.BaseDatasetVersion
		}
		cs = append(cs, dc)
	}
	for id := range want {
		found := false
		for _, c := range cs {
			found = found || c.ID == id
		}
		if !found {
			return fmt.Errorf("contribution %s is not an accepted, unpublished contribution", id)
		}
	}
	if len(cs) == 0 {
		fmt.Println("no accepted contribution waiting for a release; nothing to do")
		return nil
	}

	fmt.Printf("base v%d: %d lines, %d variants; merging %d contribution(s)\n", baseV, len(base.Lines), len(base.Variants), len(cs))
	pkg, rep, err := dataset.MergeContributions(base, cs, dataset.MergeOptions{
		Version: *version, Force: *force, Bounds: [4]float64{b.MinLat, b.MinLng, b.MaxLat, b.MaxLng},
	})
	if rep != nil {
		printReport(rep)
		if *report != "" {
			if out, e := json.MarshalIndent(rep, "", "  "); e == nil {
				_ = os.WriteFile(*report, out, 0o644)
			}
		}
	}
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("dry run: v%d is valid, nothing written\n", pkg.DatasetVersion)
		return nil
	}
	man, err := dataset.WriteRelease(*releases, pkg, *minApp, &dataset.ReleaseChanges{
		BaseVersion: baseV, ContributionIDs: rep.ContributionIDs(), LinesAdded: rep.LinesAdded, LinesUpdated: rep.LinesUpdated, Notes: *notes,
	})
	if err != nil {
		return err
	}
	// Re-read what was written: checksum and invariants of the files on disk.
	if back, _, err := dataset.ReadPackage(*releases, pkg.DatasetVersion); err != nil {
		return fmt.Errorf("written release does not read back: %w", err)
	} else if err := dataset.Validate(back, [4]float64{}); err != nil {
		return fmt.Errorf("written release: %w", err)
	}
	fmt.Printf("wrote %s/v%d: %s (%d bytes, sha256 %s)\n", *releases, pkg.DatasetVersion, man.Package.Name, man.Package.Bytes, man.Package.SHA256[:12])
	fmt.Printf("\nnext:\n  1. deploy the API with releases/v%d (Cloud Run: bash deploy/cloudrun/deploy.sh <project>; VPS: copy the folder to deploy/data/releases)\n", pkg.DatasetVersion)
	fmt.Printf("  2. curl -s <api>/v1/manifest   # dataset_version must be %d\n", pkg.DatasetVersion)
	fmt.Printf("  3. curl -X POST -H \"X-Admin-Token: $ADMIN_TOKEN\" <api>/v1/admin/datasets/%d/publish   # marks %d contribution(s), notifies their senders\n", pkg.DatasetVersion, len(rep.ContributionIDs()))
	return nil
}

func printReport(r *dataset.MergeReport) {
	for _, a := range r.Applied {
		fmt.Printf("  applied %s: line %s (%s, %d variants)\n", a.ContributionID, a.LineID, a.Op, a.Variants)
	}
	for _, s := range r.Skipped {
		fmt.Printf("  SKIPPED %s: line %s: %s\n", s.ContributionID, s.LineID, s.Reason)
	}
	for _, w := range r.Warnings {
		fmt.Printf("  warning: %s\n", w)
	}
	if r.CountsAfter != nil {
		fmt.Printf("  v%d -> v%d: lines %d -> %d, variants %d -> %d, transfers %d -> %d\n", r.BaseVersion, r.Version,
			r.CountsBefore["lines"], r.CountsAfter["lines"], r.CountsBefore["variants"], r.CountsAfter["variants"],
			r.CountsBefore["transfers"], r.CountsAfter["transfers"])
	}
}

// fetchAccepted lists accepted, unpublished contributions and loads each payload.
func fetchAccepted(base, token string) ([]contribution, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(path string, v any) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-Admin-Token", token)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return json.Unmarshal(body, v)
	}
	var list struct {
		Contributions []contribution `json:"contributions"`
	}
	if err := get("/v1/admin/contributions?status=accepted&published=false&limit=1000", &list); err != nil {
		return nil, err
	}
	out := make([]contribution, 0, len(list.Contributions))
	// Oldest first: when two contributions touch the same line, the first accepted-and-sent wins.
	for i := len(list.Contributions) - 1; i >= 0; i-- {
		var one struct {
			Contribution contribution `json:"contribution"`
		}
		if err := get("/v1/admin/contributions/"+url.PathEscape(list.Contributions[i].ID), &one); err != nil {
			return nil, err
		}
		out = append(out, one.Contribution)
	}
	return out, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
