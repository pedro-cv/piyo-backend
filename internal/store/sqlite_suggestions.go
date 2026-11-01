// Suggestion methods of the SQLite store. The table is created lazily by
// ensureSuggestionsTable so existing databases upgrade in place.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"piyo-backend/internal/model"
)

const suggestionsSchema = `
CREATE TABLE IF NOT EXISTS suggestions (
	id              TEXT PRIMARY KEY,
	device_hash     TEXT NOT NULL,
	kind            TEXT NOT NULL,
	line_id         TEXT NOT NULL DEFAULT '',
	variant_id      TEXT NOT NULL DEFAULT '',
	dataset_version INTEGER NOT NULL,
	lat             REAL NOT NULL,
	lng             REAL NOT NULL,
	payload         TEXT NOT NULL DEFAULT '{}',
	comment         TEXT NOT NULL DEFAULT '',
	app_version     TEXT NOT NULL,
	status          TEXT NOT NULL,
	review_note     TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	reviewed_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_sugg_device_created ON suggestions(device_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_sugg_status_created ON suggestions(status, created_at);
CREATE INDEX IF NOT EXISTS idx_sugg_line ON suggestions(line_id);
`

var sqliteSuggOnce sync.Map // *SQLite -> struct{}

func (s *SQLite) ensureSuggestionsTable(ctx context.Context) error {
	if _, done := sqliteSuggOnce.Load(s); done {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, suggestionsSchema); err != nil {
		return fmt.Errorf("suggestions schema: %w", err)
	}
	sqliteSuggOnce.Store(s, struct{}{})
	return nil
}

const suggCols = "id, device_hash, kind, line_id, variant_id, dataset_version, lat, lng, payload, comment, app_version, status, review_note, created_at, reviewed_at"

func scanSuggestion(sc rowScanner) (*model.Suggestion, error) {
	var sg model.Suggestion
	var payload, created string
	var reviewed sql.NullString
	if err := sc.Scan(&sg.ID, &sg.DeviceHash, &sg.Kind, &sg.LineID, &sg.VariantID, &sg.DatasetVersion, &sg.Lat, &sg.Lng,
		&payload, &sg.Comment, &sg.AppVersion, &sg.Status, &sg.ReviewNote, &created, &reviewed); err != nil {
		return nil, err
	}
	sg.Payload = []byte(payload)
	t, err := parseTime(created)
	if err != nil {
		return nil, err
	}
	sg.CreatedAt = t
	if reviewed.Valid {
		rt, err := parseTime(reviewed.String)
		if err != nil {
			return nil, err
		}
		sg.ReviewedAt = &rt
	}
	return &sg, nil
}

// CreateSuggestion inserts sg.
func (s *SQLite) CreateSuggestion(ctx context.Context, sg *model.Suggestion) error {
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return err
	}
	var reviewed any
	if sg.ReviewedAt != nil {
		reviewed = fmtTime(*sg.ReviewedAt)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO suggestions (`+suggCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		sg.ID, sg.DeviceHash, sg.Kind, sg.LineID, sg.VariantID, sg.DatasetVersion, sg.Lat, sg.Lng, string(sg.Payload),
		sg.Comment, sg.AppVersion, sg.Status, sg.ReviewNote, fmtTime(sg.CreatedAt), reviewed)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

// CountSuggestionsSince counts the device's suggestions created at or after since.
func (s *SQLite) CountSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suggestions WHERE device_hash = ? AND created_at >= ?`, deviceHash, fmtTime(since)).Scan(&n)
	return n, err
}

// ListSuggestions returns suggestions newest first.
func (s *SQLite) ListSuggestions(ctx context.Context, status, lineID string, limit int) ([]model.Suggestion, error) {
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return nil, err
	}
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	limit = clampLimit(limit)
	var where []string
	var args []any
	if filter != "" {
		where = append(where, "status = ?")
		args = append(args, filter)
	}
	if lineID != "" {
		where = append(where, "line_id = ?")
		args = append(args, lineID)
	}
	q := `SELECT ` + suggCols + ` FROM suggestions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// GetSuggestion returns the suggestion or ErrNotFound.
func (s *SQLite) GetSuggestion(ctx context.Context, id string) (*model.Suggestion, error) {
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return nil, err
	}
	sg, err := scanSuggestion(s.db.QueryRowContext(ctx, `SELECT `+suggCols+` FROM suggestions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sg, err
}

// SetSuggestionStatus updates status, note and reviewed_at.
func (s *SQLite) SetSuggestionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Suggestion, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE suggestions SET status = ?, review_note = ?, reviewed_at = ? WHERE id = ?`, status, note, fmtTime(reviewedAt), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetSuggestion(ctx, id)
}

// CountRejectedSuggestionsSince counts rejected suggestions reviewed at or after since.
func (s *SQLite) CountRejectedSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	if err := s.ensureSuggestionsTable(ctx); err != nil {
		return 0, err
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suggestions WHERE device_hash = ? AND status = ? AND reviewed_at >= ?`,
		deviceHash, model.StatusRejected, fmtTime(since)).Scan(&n)
	return n, err
}
