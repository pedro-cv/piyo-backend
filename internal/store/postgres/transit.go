// Transit-data writes used by the importer: upsert lines, variants, stops
// and record a dataset release. Everything runs in one transaction so a
// failed import leaves the previous state intact.
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"piyo-backend/internal/dataset"
)

// ImportPackage upserts the package's lines/variants/stops and inserts the
// dataset row with its manifest. Variants and stops no longer present are
// deleted (the source is the master until the editing tool exists).
func (s *Store) ImportPackage(ctx context.Context, pkg *dataset.Package, man *dataset.Manifest, notes string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, l := range pkg.Lines {
		var srcUpdated *time.Time
		if t, err := time.Parse(time.RFC3339Nano, l.SourceUpdatedAt); err == nil {
			srcUpdated = &t
		}
		if _, err := tx.Exec(ctx, `INSERT INTO lines (line_id, display, name, mode, color, color_source, fare_bs, source_url, source_updated_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
			ON CONFLICT (line_id) DO UPDATE SET display=EXCLUDED.display, name=EXCLUDED.name, mode=EXCLUDED.mode, color=EXCLUDED.color, color_source=EXCLUDED.color_source,
			fare_bs=EXCLUDED.fare_bs, source_url=EXCLUDED.source_url, source_updated_at=EXCLUDED.source_updated_at, updated_at=now()`,
			l.LineID, l.Display, l.Name, l.Mode, l.Color, l.ColorSource, l.FareBs, l.SourceURL, srcUpdated); err != nil {
			return fmt.Errorf("line %s: %w", l.LineID, err)
		}
	}
	for _, st := range pkg.Stops {
		if _, err := tx.Exec(ctx, `INSERT INTO stops (stop_id, name, geom, updated_at) VALUES ($1,$2, ST_SetSRID(ST_MakePoint($3,$4),4326)::geography, now())
			ON CONFLICT (stop_id) DO UPDATE SET name=EXCLUDED.name, geom=EXCLUDED.geom, updated_at=now()`, st.StopID, st.Name, st.Lng, st.Lat); err != nil {
			return fmt.Errorf("stop %s: %w", st.StopID, err)
		}
	}
	seqByLine := map[string]int{}
	keep := make([]string, 0, len(pkg.Variants))
	for _, v := range pkg.Variants {
		pts := dataset.DecodePolyline(v.Polyline)
		if len(pts) < 2 {
			continue
		}
		var sb strings.Builder
		sb.WriteString("LINESTRING(")
		for i, p := range pts {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%.6f %.6f", p.Lng, p.Lat)
		}
		sb.WriteByte(')')
		streets, _ := json.Marshal(v.Streets)
		seq := seqByLine[v.LineID]
		seqByLine[v.LineID] = seq + 1
		if _, err := tx.Exec(ctx, `INSERT INTO variants (variant_id, line_id, seq, name, branch, direction, headsign, origin, color, color_source, geom, length_m, gaps, streets, source_url, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, ST_GeogFromText($11), $12,$13,$14::jsonb,$15, now())
			ON CONFLICT (variant_id) DO UPDATE SET line_id=EXCLUDED.line_id, seq=EXCLUDED.seq, name=EXCLUDED.name, branch=EXCLUDED.branch, direction=EXCLUDED.direction,
			headsign=EXCLUDED.headsign, origin=EXCLUDED.origin, color=EXCLUDED.color, color_source=EXCLUDED.color_source,
			geom=EXCLUDED.geom, length_m=EXCLUDED.length_m, gaps=EXCLUDED.gaps, streets=EXCLUDED.streets, source_url=EXCLUDED.source_url, updated_at=now()`,
			v.VariantID, v.LineID, seq, v.Name, v.Branch, v.Direction, v.Headsign, v.Origin, v.Color, v.ColorSource, sb.String(), v.LengthM, v.Gaps, string(streets), v.SourceURL); err != nil {
			return fmt.Errorf("variant %s: %w", v.VariantID, err)
		}
		keep = append(keep, v.VariantID)
		if _, err := tx.Exec(ctx, `DELETE FROM variant_stops WHERE variant_id=$1`, v.VariantID); err != nil {
			return err
		}
		for i, sid := range v.StopIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO variant_stops (variant_id, seq, stop_id) VALUES ($1,$2,$3)`, v.VariantID, i+1, sid); err != nil {
				return fmt.Errorf("variant_stops %s/%d: %w", v.VariantID, i, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM variants WHERE NOT (variant_id = ANY($1))`, keep); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM stops WHERE stop_id NOT IN (SELECT stop_id FROM variant_stops)`); err != nil {
		return err
	}
	mb, err := json.Marshal(man)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO datasets (version, generated_at, published_at, manifest, notes) VALUES ($1,$2,$3,$4::jsonb,$5)
		ON CONFLICT (version) DO UPDATE SET generated_at=EXCLUDED.generated_at, published_at=EXCLUDED.published_at, manifest=EXCLUDED.manifest, notes=EXCLUDED.notes`,
		pkg.DatasetVersion, pkg.GeneratedAt, time.Now().UTC(), string(mb), notes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// LatestDatasetVersion returns the highest version recorded, 0 when none.
func (s *Store) LatestDatasetVersion(ctx context.Context) (int, error) {
	var v *int
	if err := s.pool.QueryRow(ctx, `SELECT MAX(version) FROM datasets`).Scan(&v); err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// Counts returns row counts for the health/admin endpoints.
func (s *Store) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for _, t := range []string{"lines", "variants", "stops", "datasets", "reports", "suggestions", "nps", "blocks"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+t).Scan(&n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, nil
}
