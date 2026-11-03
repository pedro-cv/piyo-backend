// Push-token methods of the PostgreSQL store (table from migrations/003_push_tokens.sql).
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) SetPushToken(ctx context.Context, deviceHash, token string, updatedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO push_tokens (device_hash, token, updated_at) VALUES ($1,$2,$3)
		ON CONFLICT (device_hash) DO UPDATE SET token = EXCLUDED.token, updated_at = EXCLUDED.updated_at`,
		deviceHash, token, updatedAt.UTC())
	return err
}

func (s *Store) GetPushToken(ctx context.Context, deviceHash string) (string, error) {
	var tok string
	err := s.pool.QueryRow(ctx, `SELECT token FROM push_tokens WHERE device_hash=$1`, deviceHash).Scan(&tok)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return tok, err
}

func (s *Store) DeletePushToken(ctx context.Context, deviceHash string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM push_tokens WHERE device_hash=$1`, deviceHash)
	return err
}

func (s *Store) DeletePushTokensBefore(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM push_tokens WHERE updated_at < $1`, before.UTC())
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
