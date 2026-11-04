// Contribution methods of the SQLite store. The table is created lazily and
// missing columns are added, so existing databases upgrade in place.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"piyo-backend/internal/model"
)

const contributionsSchema = `
CREATE TABLE IF NOT EXISTS contributions (
	id                   TEXT PRIMARY KEY,
	device_hash          TEXT NOT NULL,
	provider_name        TEXT NOT NULL DEFAULT '',
	base_dataset_version INTEGER NOT NULL,
	route_ids            TEXT NOT NULL DEFAULT '[]',
	payload              TEXT NOT NULL DEFAULT '{}',
	comment              TEXT NOT NULL DEFAULT '',
	app_version          TEXT NOT NULL,
	status               TEXT NOT NULL,
	review_note          TEXT NOT NULL DEFAULT '',
	created_at           TEXT NOT NULL,
	reviewed_at          TEXT,
	batch_id             TEXT NOT NULL DEFAULT '',
	published_version    INTEGER NOT NULL DEFAULT 0,
	published_at         TEXT
);
CREATE INDEX IF NOT EXISTS idx_contrib_device_created ON contributions(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_contrib_status_created ON contributions(status, created_at);
`

// contributionsAddedColumns are the columns added after the table first shipped
// (batch split and release tracking; migration 004 in the PostgreSQL store).
var contributionsAddedColumns = []struct{ name, ddl string }{
	{"batch_id", "ALTER TABLE contributions ADD COLUMN batch_id TEXT NOT NULL DEFAULT ''"},
	{"published_version", "ALTER TABLE contributions ADD COLUMN published_version INTEGER NOT NULL DEFAULT 0"},
	{"published_at", "ALTER TABLE contributions ADD COLUMN published_at TEXT"},
}

var sqliteContribOnce sync.Map // *SQLite -> struct{}

func (s *SQLite) ensureContributionsTable(ctx context.Context) error {
	if _, done := sqliteContribOnce.Load(s); done {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, contributionsSchema); err != nil {
		return fmt.Errorf("contributions schema: %w", err)
	}
	have := map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info('contributions')`)
	if err != nil {
		return fmt.Errorf("contributions columns: %w", err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		have[n] = true
	}
	rows.Close()
	for _, c := range contributionsAddedColumns {
		if !have[c.name] {
			if _, err := s.db.ExecContext(ctx, c.ddl); err != nil {
				return fmt.Errorf("contributions add %s: %w", c.name, err)
			}
		}
	}
	sqliteContribOnce.Store(s, struct{}{})
	return nil
}

const contribCols = "id, device_hash, provider_name, base_dataset_version, route_ids, payload, comment, app_version, status, review_note, created_at, reviewed_at, batch_id, published_version, published_at"
const contribColsNoPayload = "id, device_hash, provider_name, base_dataset_version, route_ids, '' AS payload, comment, app_version, status, review_note, created_at, reviewed_at, batch_id, published_version, published_at"

func scanContribution(sc rowScanner) (*model.Contribution, error) {
	var c model.Contribution
	var routeIDs, payload, created string
	var reviewed, published sql.NullString
	if err := sc.Scan(&c.ID, &c.DeviceHash, &c.ProviderName, &c.BaseDatasetVersion, &routeIDs, &payload, &c.Comment,
		&c.AppVersion, &c.Status, &c.ReviewNote, &created, &reviewed, &c.BatchID, &c.PublishedVersion, &published); err != nil {
		return nil, err
	}
	if published.Valid {
		pt, err := parseTime(published.String)
		if err != nil {
			return nil, err
		}
		c.PublishedAt = &pt
	}
	if payload != "" {
		c.Payload = []byte(payload)
	}
	c.RouteIDs = []string{}
	_ = json.Unmarshal([]byte(routeIDs), &c.RouteIDs)
	t, err := parseTime(created)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = t
	if reviewed.Valid {
		rt, err := parseTime(reviewed.String)
		if err != nil {
			return nil, err
		}
		c.ReviewedAt = &rt
	}
	return &c, nil
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertContribution(ctx context.Context, db sqlExecer, c *model.Contribution) error {
	var reviewed, published any
	if c.ReviewedAt != nil {
		reviewed = fmtTime(*c.ReviewedAt)
	}
	if c.PublishedAt != nil {
		published = fmtTime(*c.PublishedAt)
	}
	ids, _ := json.Marshal(c.RouteIDs)
	if c.RouteIDs == nil {
		ids = []byte("[]")
	}
	payload := string(c.Payload)
	if payload == "" {
		payload = "{}"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO contributions (`+contribCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.DeviceHash, c.ProviderName, c.BaseDatasetVersion, string(ids), payload, c.Comment, c.AppVersion, c.Status, c.ReviewNote,
		fmtTime(c.CreatedAt), reviewed, c.BatchID, c.PublishedVersion, published)
	if err != nil && isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	return err
}

// CreateContribution inserts c.
func (s *SQLite) CreateContribution(ctx context.Context, c *model.Contribution) error {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return err
	}
	return insertContribution(ctx, s.db, c)
}

// CreateContributions inserts every row in one transaction.
func (s *SQLite) CreateContributions(ctx context.Context, cs []*model.Contribution) error {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range cs {
		if err := insertContribution(ctx, tx, c); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountContributionsSince counts the device's batches created at or after since.
func (s *SQLite) CountContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT CASE WHEN batch_id = '' THEN id ELSE batch_id END) FROM contributions WHERE device_hash = ? AND created_at >= ?`,
		deviceHash, fmtTime(since)).Scan(&n)
	return n, err
}

// DeleteContribution removes a pending contribution owned by deviceHash.
func (s *SQLite) DeleteContribution(ctx context.Context, id, deviceHash string) error {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return err
	}
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT status FROM contributions WHERE id = ? AND device_hash = ?`, id, deviceHash).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != model.StatusPending {
		return ErrNotPending
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM contributions WHERE id = ? AND device_hash = ? AND status = ?`, id, deviceHash, model.StatusPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotPending // reviewed between the read and the delete
	}
	return nil
}

// MarkContributionsPublished marks accepted, unpublished contributions as part of version.
func (s *SQLite) MarkContributionsPublished(ctx context.Context, ids []string, version int, at time.Time) ([]model.Contribution, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return nil, err
	}
	out := []model.Contribution{}
	for _, id := range ids {
		res, err := s.db.ExecContext(ctx, `UPDATE contributions SET published_version = ?, published_at = ? WHERE id = ? AND status = ? AND published_version = 0`,
			version, fmtTime(at), id, model.StatusAccepted)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		c, err := s.GetContribution(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c.Summary())
	}
	return out, nil
}

func (s *SQLite) queryContribs(ctx context.Context, where string, args ...any) ([]model.Contribution, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+contribColsNoPayload+` FROM contributions `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Contribution{}
	for rows.Next() {
		c, err := scanContribution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListContributions returns summaries newest first.
func (s *SQLite) ListContributions(ctx context.Context, status string, limit int) ([]model.Contribution, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return nil, err
	}
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	if filter == "" {
		return s.queryContribs(ctx, `ORDER BY created_at DESC LIMIT ?`, clampLimit(limit))
	}
	return s.queryContribs(ctx, `WHERE status = ? ORDER BY created_at DESC LIMIT ?`, filter, clampLimit(limit))
}

// ListContributionsByDevice returns the device's summaries newest first.
func (s *SQLite) ListContributionsByDevice(ctx context.Context, deviceHash string, limit int) ([]model.Contribution, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return nil, err
	}
	return s.queryContribs(ctx, `WHERE device_hash = ? ORDER BY created_at DESC LIMIT ?`, deviceHash, clampLimit(limit))
}

// GetContribution returns the contribution with its payload or ErrNotFound.
func (s *SQLite) GetContribution(ctx context.Context, id string) (*model.Contribution, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return nil, err
	}
	c, err := scanContribution(s.db.QueryRowContext(ctx, `SELECT `+contribCols+` FROM contributions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// SetContributionStatus updates status, note and reviewed_at.
func (s *SQLite) SetContributionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Contribution, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	if err := s.ensureContributionsTable(ctx); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE contributions SET status = ?, review_note = ?, reviewed_at = ? WHERE id = ?`, status, note, fmtTime(reviewedAt), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetContribution(ctx, id)
}

// CountRejectedContributionsSince counts rejected contributions reviewed at or after since.
func (s *SQLite) CountRejectedContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	if err := s.ensureContributionsTable(ctx); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contributions WHERE device_hash = ? AND status = ? AND reviewed_at >= ?`,
		deviceHash, model.StatusRejected, fmtTime(since)).Scan(&n)
	return n, err
}
