// Command piyo-import turns the santa-cruz-transit-data source into a
// versioned release and (optionally) loads it into PostgreSQL/PostGIS.
//
//	piyo-import -source <data-repo>/data-<provider>/lines.json -out ./releases [-version 14] [-db postgres://...]
//
// The source may be the OSM file (data/lines.json) or the named-routes file
// (data-<provider>/lines.json, see docs/DATA_SOURCES.md); the layout is detected per variant.
//
// Without -version the next version after the newest release in -out is
// used (or after the newest dataset row when -db is given). The command
// writes releases/vN/{manifest.json, dataset-vN.json.gz, lines.json,
// stops.json, variants.json, transfers.json} and releases/latest.json.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"piyo-backend/internal/dataset"
	"piyo-backend/internal/store/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "piyo-import:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		source  = flag.String("source", "../santa-cruz-transit-data/data/lines.json", "path to the source lines.json")
		out     = flag.String("out", envOr("RELEASES_DIR", "./releases"), "releases directory (env RELEASES_DIR)")
		version = flag.Int("version", 0, "dataset version to produce (0 = next)")
		dsn     = flag.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL DSN; when set, the data is loaded into the database (env DATABASE_URL)")
		fare    = flag.String("fare", envOr("FARE_BS", "3.50"), "global micro fare in Bs")
		minApp  = flag.String("min-app", envOr("MIN_APP_VERSION", "1"), "minimum supported app version")
		notes   = flag.String("notes", "", "release notes stored with the dataset row")
		dryRun  = flag.Bool("dry-run", false, "build and print statistics without writing anything")
		transfM = flag.Float64("transfer-m", 150, "max walking distance for a transfer link (m)")
		perPair = flag.Int("max-per-pair", 4, "max transfer links kept per pair of variants")
		colors  = flag.String("colors", "", "colour/description file for origin and headsign (default: next to the source or in ../data)")
		geocode = flag.String("geocode", "", "endpoint name → coordinates file that orients ida/vuelta (default: next to the source or in ../data)")
	)
	flag.Parse()
	ctx := context.Background()

	var pg *postgres.Store
	if *dsn != "" {
		var err error
		if pg, err = postgres.Open(ctx, *dsn); err != nil {
			return err
		}
		defer pg.Close()
	}
	v := *version
	if v == 0 {
		v = dataset.LatestVersion(*out)
		if pg != nil {
			if dv, err := pg.LatestDatasetVersion(ctx); err == nil && dv > v {
				v = dv
			}
		}
		v++
	}
	start := time.Now()
	pkg, err := dataset.Build(*source, dataset.BuildOptions{Version: v, FareBs: *fare, TransferM: *transfM, MaxPerPair: *perPair, ColorsPath: *colors, GeocodePath: *geocode})
	if err != nil {
		return err
	}
	fmt.Printf("built dataset v%d from %s in %s\n  lines=%d variants=%d stops=%d transfers=%d bbox=%v\n",
		v, *source, time.Since(start).Round(time.Millisecond), len(pkg.Lines), len(pkg.Variants), len(pkg.Stops), len(pkg.Transfers), pkg.BBox)
	if *dryRun {
		return nil
	}
	man, err := dataset.Write(*out, pkg, *minApp)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s/v%d: package %s (%d bytes, sha256 %s)\n", *out, v, man.Package.Name, man.Package.Bytes, man.Package.SHA256[:12])
	for _, f := range man.Files {
		fmt.Printf("  %-16s %8d bytes  %s\n", f.Name, f.Bytes, f.SHA256[:12])
	}
	if pg != nil {
		if err := pg.ImportPackage(ctx, pkg, man, *notes); err != nil {
			return fmt.Errorf("database import: %w", err)
		}
		counts, _ := pg.Counts(ctx)
		fmt.Printf("database updated: %v\n", counts)
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
