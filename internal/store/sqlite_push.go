// Push-token methods of the SQLite store (table created with the schema on open).
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SetPushToken upserts the device's token.
func (s *SQLite) SetPushToken(ctx context.Context, deviceHash, token string, updatedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO push_tokens (device_hash, token, updated_at) VALUES (?,?,?)
		ON CONFLICT(device_hash) DO UPDATE SET token = excluded.token, updated_at = excluded.updated_at`,
		deviceHash, token, fmtTime(updatedAt))
	return err
}

// GetPushToken returns the device's token or "".
func (s *SQLite) GetPushToken(ctx context.Context, deviceHash string) (string, error) {
	var tok string
	err := s.db.QueryRowContext(ctx, `SELECT token FROM push_tokens WHERE device_hash = ?`, deviceHash).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return tok, err
}

// DeletePushToken removes the device's token (no error when missing).
func (s *SQLite) DeletePushToken(ctx context.Context, deviceHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_tokens WHERE device_hash = ?`, deviceHash)
	return err
}

// DeletePushTokensBefore purges tokens last refreshed before before.
func (s *SQLite) DeletePushTokensBefore(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM push_tokens WHERE updated_at < ?`, fmtTime(before))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
