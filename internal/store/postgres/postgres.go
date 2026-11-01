// Package postgres is the PostgreSQL + PostGIS implementation of store.Store
// plus the transit-data tables used by the importer. Schema in
// backend/migrations (embedded, applied on Open; every statement is
// idempotent).
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a store.Store backed by a pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

var _ store.Store = (*Store)(nil)

// Open connects with dsn (postgres://user:pass@host:5432/db) and applies the
// migrations in lexical order.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres dsn: %w", err)
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// The database container may still be initialising (PostGIS restarts
	// once after init); retry for ~45 s before giving up.
	var pingErr error
	for attempt := 0; attempt < 30; attempt++ {
		if pingErr = pool.Ping(ctx); pingErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
	if pingErr != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", pingErr)
	}
	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Pool exposes the pool for the importer.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		sqlText, err := fs.ReadFile(migrations, "migrations/"+n)
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, string(sqlText)); err != nil {
			return fmt.Errorf("migration %s: %w", n, err)
		}
	}
	return nil
}

// Close releases the pool.
func (s *Store) Close() error { s.pool.Close(); return nil }

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func statusFilter(status string) (string, error) {
	if status == "" || status == store.StatusAll {
		return "", nil
	}
	if !model.IsReportStatus(status) {
		return "", store.ErrInvalidStatus
	}
	return status, nil
}

func clamp(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

// --- reports -----------------------------------------------------------------

const reportCols = `id, device_hash, line_id, type, ST_Y(geom::geometry), ST_X(geom::geometry), comment, app_version, data_version, status, created_at, reviewed_at`

func scanReport(row pgx.Row) (*model.Report, error) {
	var r model.Report
	var reviewed *time.Time
	if err := row.Scan(&r.ID, &r.DeviceHash, &r.LineID, &r.Type, &r.Lat, &r.Lng, &r.Comment, &r.AppVersion, &r.DataVersion, &r.Status, &r.CreatedAt, &reviewed); err != nil {
		return nil, err
	}
	r.CreatedAt = r.CreatedAt.UTC()
	if reviewed != nil {
		t := reviewed.UTC()
		r.ReviewedAt = &t
	}
	return &r, nil
}

func (s *Store) CreateReport(ctx context.Context, r *model.Report) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO reports (id, device_hash, line_id, type, geom, comment, app_version, data_version, status, created_at, reviewed_at)
		VALUES ($1,$2,$3,$4, ST_SetSRID(ST_MakePoint($5,$6),4326)::geography, $7,$8,$9,$10,$11,$12)`,
		r.ID, r.DeviceHash, r.LineID, r.Type, r.Lng, r.Lat, r.Comment, r.AppVersion, r.DataVersion, r.Status, r.CreatedAt, r.ReviewedAt)
	if isUnique(err) {
		return store.ErrAlreadyExists
	}
	return err
}

func (s *Store) CountReportsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM reports WHERE device_hash=$1 AND created_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}

func (s *Store) ListReports(ctx context.Context, status string, limit int) ([]model.Report, error) {
	f, err := statusFilter(status)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + reportCols + ` FROM reports`
	args := []any{}
	if f != "" {
		q += ` WHERE status=$1`
		args = append(args, f)
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, clamp(limit))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Report{}
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetReport(ctx context.Context, id string) (*model.Report, error) {
	r, err := scanReport(s.pool.QueryRow(ctx, `SELECT `+reportCols+` FROM reports WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return r, err
}

func (s *Store) SetReportStatus(ctx context.Context, id, status string, reviewedAt time.Time) (*model.Report, error) {
	if !model.IsReportStatus(status) {
		return nil, store.ErrInvalidStatus
	}
	tag, err := s.pool.Exec(ctx, `UPDATE reports SET status=$1, reviewed_at=$2 WHERE id=$3`, status, reviewedAt, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, store.ErrNotFound
	}
	return s.GetReport(ctx, id)
}

func (s *Store) CountRejectedSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM reports WHERE device_hash=$1 AND status='rejected' AND reviewed_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}

// --- suggestions ------------------------------------------------------------

const suggCols = `id, device_hash, kind, line_id, variant_id, dataset_version, ST_Y(geom::geometry), ST_X(geom::geometry), payload, comment, app_version, status, review_note, created_at, reviewed_at`

func scanSuggestion(row pgx.Row) (*model.Suggestion, error) {
	var sg model.Suggestion
	var reviewed *time.Time
	var payload []byte
	if err := row.Scan(&sg.ID, &sg.DeviceHash, &sg.Kind, &sg.LineID, &sg.VariantID, &sg.DatasetVersion, &sg.Lat, &sg.Lng, &payload, &sg.Comment, &sg.AppVersion, &sg.Status, &sg.ReviewNote, &sg.CreatedAt, &reviewed); err != nil {
		return nil, err
	}
	sg.Payload = payload
	sg.CreatedAt = sg.CreatedAt.UTC()
	if reviewed != nil {
		t := reviewed.UTC()
		sg.ReviewedAt = &t
	}
	return &sg, nil
}

func (s *Store) CreateSuggestion(ctx context.Context, sg *model.Suggestion) error {
	payload := sg.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO suggestions (id, device_hash, kind, line_id, variant_id, dataset_version, geom, payload, comment, app_version, status, review_note, created_at, reviewed_at)
		VALUES ($1,$2,$3,$4,$5,$6, ST_SetSRID(ST_MakePoint($7,$8),4326)::geography, $9::jsonb,$10,$11,$12,$13,$14,$15)`,
		sg.ID, sg.DeviceHash, sg.Kind, sg.LineID, sg.VariantID, sg.DatasetVersion, sg.Lng, sg.Lat, string(payload), sg.Comment, sg.AppVersion, sg.Status, sg.ReviewNote, sg.CreatedAt, sg.ReviewedAt)
	if isUnique(err) {
		return store.ErrAlreadyExists
	}
	return err
}

func (s *Store) CountSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM suggestions WHERE device_hash=$1 AND created_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}

func (s *Store) ListSuggestions(ctx context.Context, status, lineID string, limit int) ([]model.Suggestion, error) {
	f, err := statusFilter(status)
	if err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if f != "" {
		args = append(args, f)
		where = append(where, fmt.Sprintf("status=$%d", len(args)))
	}
	if lineID != "" {
		args = append(args, lineID)
		where = append(where, fmt.Sprintf("line_id=$%d", len(args)))
	}
	q := `SELECT ` + suggCols + ` FROM suggestions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC LIMIT %d`, clamp(limit))
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Suggestion{}
	for rows.Next() {
		sg, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sg)
	}
	return out, rows.Err()
}

func (s *Store) GetSuggestion(ctx context.Context, id string) (*model.Suggestion, error) {
	sg, err := scanSuggestion(s.pool.QueryRow(ctx, `SELECT `+suggCols+` FROM suggestions WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return sg, err
}

func (s *Store) SetSuggestionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Suggestion, error) {
	if !model.IsReportStatus(status) {
		return nil, store.ErrInvalidStatus
	}
	tag, err := s.pool.Exec(ctx, `UPDATE suggestions SET status=$1, review_note=$2, reviewed_at=$3 WHERE id=$4`, status, note, reviewedAt, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, store.ErrNotFound
	}
	return s.GetSuggestion(ctx, id)
}

func (s *Store) CountRejectedSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM suggestions WHERE device_hash=$1 AND status='rejected' AND reviewed_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}

// --- nps ------------------------------------------------------------------------

func (s *Store) CreateNPS(ctx context.Context, n *model.NPS) error {
	reasons := n.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO nps (id, device_hash, score, reasons, comment, app_version, data_version, device_class, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, n.ID, n.DeviceHash, n.Score, reasons, n.Comment, n.AppVersion, n.DataVersion, n.DeviceClass, n.CreatedAt)
	if isUnique(err) {
		return store.ErrAlreadyExists
	}
	return err
}

func (s *Store) ListNPS(ctx context.Context, limit int) ([]model.NPS, error) {
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT id, device_hash, score, reasons, comment, app_version, data_version, device_class, created_at FROM nps ORDER BY created_at DESC LIMIT %d`, clamp(limit)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.NPS{}
	for rows.Next() {
		var n model.NPS
		if err := rows.Scan(&n.ID, &n.DeviceHash, &n.Score, &n.Reasons, &n.Comment, &n.AppVersion, &n.DataVersion, &n.DeviceClass, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.CreatedAt = n.CreatedAt.UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- blocks ----------------------------------------------------------------------

func (s *Store) SetBlock(ctx context.Context, b model.Block) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO blocks (device_hash, until_at, reason, created_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (device_hash) DO UPDATE SET until_at=EXCLUDED.until_at, reason=EXCLUDED.reason, created_at=EXCLUDED.created_at`,
		b.DeviceHash, b.Until, b.Reason, b.CreatedAt)
	return err
}

func (s *Store) GetBlock(ctx context.Context, deviceHash string) (*model.Block, error) {
	var b model.Block
	err := s.pool.QueryRow(ctx, `SELECT device_hash, until_at, reason, created_at FROM blocks WHERE device_hash=$1 AND until_at > now()`, deviceHash).
		Scan(&b.DeviceHash, &b.Until, &b.Reason, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.Until, b.CreatedAt = b.Until.UTC(), b.CreatedAt.UTC()
	return &b, nil
}

func (s *Store) DeleteBlock(ctx context.Context, deviceHash string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM blocks WHERE device_hash=$1`, deviceHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListBlocks(ctx context.Context) ([]model.Block, error) {
	rows, err := s.pool.Query(ctx, `SELECT device_hash, until_at, reason, created_at FROM blocks ORDER BY until_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Block{}
	for rows.Next() {
		var b model.Block
		if err := rows.Scan(&b.DeviceHash, &b.Until, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		b.Until, b.CreatedAt = b.Until.UTC(), b.CreatedAt.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}
