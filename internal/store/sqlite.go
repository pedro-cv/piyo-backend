// SQLite Store implementation (pure Go via modernc.org/sqlite, no cgo), used
// when DEV_MODE=false. The schema is created on open with
// CREATE TABLE IF NOT EXISTS, so there is no separate migration step.
//
// Times are stored as fixed-width UTC strings (timeLayout) so that plain
// string comparison in SQL orders them chronologically. NPS reasons are
// stored as a JSON array in a TEXT column.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"piyo-backend/internal/model"
)

// timeLayout is RFC 3339 with a fixed 9-digit fraction so values sort
// lexicographically.
const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

const schema = `
CREATE TABLE IF NOT EXISTS reports (
	id           TEXT PRIMARY KEY,
	device_hash  TEXT NOT NULL,
	line_id      TEXT NOT NULL DEFAULT '',
	type         TEXT NOT NULL,
	lat          REAL NOT NULL,
	lng          REAL NOT NULL,
	comment      TEXT NOT NULL DEFAULT '',
	app_version  TEXT NOT NULL,
	data_version INTEGER NOT NULL,
	status       TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	reviewed_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_reports_device_created ON reports(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_reports_status_created ON reports(status, created_at);

CREATE TABLE IF NOT EXISTS nps (
	id           TEXT PRIMARY KEY,
	device_hash  TEXT NOT NULL,
	score        INTEGER NOT NULL,
	reasons      TEXT NOT NULL DEFAULT '[]',
	comment      TEXT NOT NULL DEFAULT '',
	app_version  TEXT NOT NULL,
	data_version INTEGER NOT NULL,
	device_class TEXT NOT NULL DEFAULT '',
	created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nps_created ON nps(created_at);

CREATE TABLE IF NOT EXISTS blocks (
	device_hash TEXT PRIMARY KEY,
	until       TEXT NOT NULL,
	reason      TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL
);

-- FCM token per device (docs/implementation/PRIVACY.md §push).
CREATE TABLE IF NOT EXISTS push_tokens (
	device_hash TEXT PRIMARY KEY,
	token       TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_push_tokens_updated ON push_tokens(updated_at);
`

// SQLite is a Store backed by a single SQLite file.
type SQLite struct {
	db *sql.DB
}

var _ Store = (*SQLite)(nil)

// OpenSQLite opens (creating if needed) the database at path and applies the
// schema. The parent directory is created when missing.
func OpenSQLite(ctx context.Context, path string) (*SQLite, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("sqlite: create dir: %w", err)
		}
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// One writer at a time keeps SQLite happy and avoids SQLITE_BUSY churn.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return &SQLite{db: db}, nil
}

// Close closes the database.
func (s *SQLite) Close() error { return s.db.Close() }

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		// Be lenient with rows written by other tools.
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	return t.UTC(), err
}

// --- reports ---------------------------------------------------------------

const reportCols = `id, device_hash, line_id, type, lat, lng, comment, app_version, data_version, status, created_at, reviewed_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanReport(sc rowScanner) (*model.Report, error) {
	var r model.Report
	var created string
	var reviewed sql.NullString
	if err := sc.Scan(&r.ID, &r.DeviceHash, &r.LineID, &r.Type, &r.Lat, &r.Lng, &r.Comment,
		&r.AppVersion, &r.DataVersion, &r.Status, &created, &reviewed); err != nil {
		return nil, err
	}
	var err error
	if r.CreatedAt, err = parseTime(created); err != nil {
		return nil, fmt.Errorf("sqlite: report %s created_at: %w", r.ID, err)
	}
	if reviewed.Valid && reviewed.String != "" {
		t, err := parseTime(reviewed.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: report %s reviewed_at: %w", r.ID, err)
		}
		r.ReviewedAt = &t
	}
	return &r, nil
}

// CreateReport inserts r.
func (s *SQLite) CreateReport(ctx context.Context, r *model.Report) error {
	var reviewed any
	if r.ReviewedAt != nil {
		reviewed = fmtTime(*r.ReviewedAt)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO reports (`+reportCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.DeviceHash, r.LineID, r.Type, r.Lat, r.Lng, r.Comment,
		r.AppVersion, r.DataVersion, r.Status, fmtTime(r.CreatedAt), reviewed)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("sqlite: insert report: %w", err)
	}
	return nil
}

// CountReportsSince counts the device's reports created at or after since.
func (s *SQLite) CountReportsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reports WHERE device_hash = ? AND created_at >= ?`,
		deviceHash, fmtTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count reports: %w", err)
	}
	return n, nil
}

// ListReports returns reports newest first, optionally filtered by status.
func (s *SQLite) ListReports(ctx context.Context, status string, limit int) ([]model.Report, error) {
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	limit = clampLimit(limit)

	q := `SELECT ` + reportCols + ` FROM reports`
	args := []any{}
	if filter != "" {
		q += ` WHERE status = ?`
		args = append(args, filter)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list reports: %w", err)
	}
	defer rows.Close()

	out := make([]model.Report, 0, limit)
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetReport returns the report or ErrNotFound.
func (s *SQLite) GetReport(ctx context.Context, id string) (*model.Report, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+reportCols+` FROM reports WHERE id = ?`, id)
	r, err := scanReport(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get report: %w", err)
	}
	return r, nil
}

// SetReportStatus updates status and reviewed_at and returns the new row.
func (s *SQLite) SetReportStatus(ctx context.Context, id, status string, reviewedAt time.Time) (*model.Report, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE reports SET status = ?, reviewed_at = ? WHERE id = ?`,
		status, fmtTime(reviewedAt), id)
	if err != nil {
		return nil, fmt.Errorf("sqlite: update report: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return nil, ErrNotFound
	}
	return s.GetReport(ctx, id)
}

// CountRejectedSince counts rejected reports reviewed at or after since.
func (s *SQLite) CountRejectedSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reports WHERE device_hash = ? AND status = ? AND reviewed_at IS NOT NULL AND reviewed_at >= ?`,
		deviceHash, model.StatusRejected, fmtTime(since)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count rejected: %w", err)
	}
	return n, nil
}

// --- nps ---------------------------------------------------------------------

// CreateNPS inserts n; reasons are serialised as a JSON array.
func (s *SQLite) CreateNPS(ctx context.Context, n *model.NPS) error {
	reasons := n.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	js, err := json.Marshal(reasons)
	if err != nil {
		return fmt.Errorf("sqlite: encode reasons: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO nps (id, device_hash, score, reasons, comment, app_version, data_version, device_class, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		n.ID, n.DeviceHash, n.Score, string(js), n.Comment, n.AppVersion, n.DataVersion, n.DeviceClass, fmtTime(n.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("sqlite: insert nps: %w", err)
	}
	return nil
}

// ListNPS returns NPS entries newest first.
func (s *SQLite) ListNPS(ctx context.Context, limit int) ([]model.NPS, error) {
	limit = clampLimit(limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, device_hash, score, reasons, comment, app_version, data_version, device_class, created_at
		 FROM nps ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list nps: %w", err)
	}
	defer rows.Close()

	out := make([]model.NPS, 0, limit)
	for rows.Next() {
		var n model.NPS
		var reasons, created string
		if err := rows.Scan(&n.ID, &n.DeviceHash, &n.Score, &reasons, &n.Comment,
			&n.AppVersion, &n.DataVersion, &n.DeviceClass, &created); err != nil {
			return nil, fmt.Errorf("sqlite: scan nps: %w", err)
		}
		if err := json.Unmarshal([]byte(reasons), &n.Reasons); err != nil {
			return nil, fmt.Errorf("sqlite: nps %s reasons: %w", n.ID, err)
		}
		if n.Reasons == nil {
			n.Reasons = []string{}
		}
		if n.CreatedAt, err = parseTime(created); err != nil {
			return nil, fmt.Errorf("sqlite: nps %s created_at: %w", n.ID, err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- blocks ------------------------------------------------------------------

// SetBlock upserts the device's block.
func (s *SQLite) SetBlock(ctx context.Context, b model.Block) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO blocks (device_hash, until, reason, created_at) VALUES (?,?,?,?)
		 ON CONFLICT(device_hash) DO UPDATE SET until = excluded.until, reason = excluded.reason, created_at = excluded.created_at`,
		b.DeviceHash, fmtTime(b.Until), b.Reason, fmtTime(b.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: set block: %w", err)
	}
	return nil
}

func scanBlock(sc rowScanner) (*model.Block, error) {
	var b model.Block
	var until, created string
	if err := sc.Scan(&b.DeviceHash, &until, &b.Reason, &created); err != nil {
		return nil, err
	}
	var err error
	if b.Until, err = parseTime(until); err != nil {
		return nil, fmt.Errorf("sqlite: block until: %w", err)
	}
	if b.CreatedAt, err = parseTime(created); err != nil {
		return nil, fmt.Errorf("sqlite: block created_at: %w", err)
	}
	return &b, nil
}

// GetBlock returns the active block or nil when missing/expired.
func (s *SQLite) GetBlock(ctx context.Context, deviceHash string) (*model.Block, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT device_hash, until, reason, created_at FROM blocks WHERE device_hash = ?`, deviceHash)
	b, err := scanBlock(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get block: %w", err)
	}
	if b.Expired(time.Now().UTC()) {
		return nil, nil
	}
	return b, nil
}

// DeleteBlock removes the device's block; ErrNotFound if none exists.
func (s *SQLite) DeleteBlock(ctx context.Context, deviceHash string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM blocks WHERE device_hash = ?`, deviceHash)
	if err != nil {
		return fmt.Errorf("sqlite: delete block: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListBlocks returns every block (including expired ones) ordered by until.
func (s *SQLite) ListBlocks(ctx context.Context) ([]model.Block, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT device_hash, until, reason, created_at FROM blocks ORDER BY until ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list blocks: %w", err)
	}
	defer rows.Close()

	var out []model.Block
	for rows.Next() {
		b, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	if out == nil {
		out = []model.Block{}
	}
	return out, rows.Err()
}

// isUniqueViolation detects a primary-key clash without depending on the
// driver's error types.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}
