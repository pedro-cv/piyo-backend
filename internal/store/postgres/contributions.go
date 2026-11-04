// Contribution methods of the PostgreSQL store (table from migrations/002_contributions.sql,
// batch and release columns from 004_contribution_batches.sql).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"piyo-backend/internal/model"
	"piyo-backend/internal/store"
)

const contribCols = `id, device_hash, provider_name, base_dataset_version, route_ids, payload, comment, app_version, status, review_note, created_at, reviewed_at, batch_id, published_version, published_at`
const contribColsNoPayload = `id, device_hash, provider_name, base_dataset_version, route_ids, NULL::jsonb, comment, app_version, status, review_note, created_at, reviewed_at, batch_id, published_version, published_at`

func scanContribution(row pgx.Row) (*model.Contribution, error) {
	var c model.Contribution
	var reviewed, published *time.Time
	var payload []byte
	var routeIDs []string
	if err := row.Scan(&c.ID, &c.DeviceHash, &c.ProviderName, &c.BaseDatasetVersion, &routeIDs, &payload, &c.Comment,
		&c.AppVersion, &c.Status, &c.ReviewNote, &c.CreatedAt, &reviewed, &c.BatchID, &c.PublishedVersion, &published); err != nil {
		return nil, err
	}
	if published != nil {
		t := published.UTC()
		c.PublishedAt = &t
	}
	if len(payload) > 0 {
		c.Payload = payload
	}
	c.RouteIDs = routeIDs
	if c.RouteIDs == nil {
		c.RouteIDs = []string{}
	}
	c.CreatedAt = c.CreatedAt.UTC()
	if reviewed != nil {
		t := reviewed.UTC()
		c.ReviewedAt = &t
	}
	return &c, nil
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertContribution(ctx context.Context, db execer, c *model.Contribution) error {
	payload := c.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	ids := c.RouteIDs
	if ids == nil {
		ids = []string{}
	}
	_, err := db.Exec(ctx, `INSERT INTO contributions (`+contribCols+`)
		VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		c.ID, c.DeviceHash, c.ProviderName, c.BaseDatasetVersion, ids, string(payload), c.Comment, c.AppVersion, c.Status, c.ReviewNote,
		c.CreatedAt, c.ReviewedAt, c.BatchID, c.PublishedVersion, c.PublishedAt)
	if isUnique(err) {
		return store.ErrAlreadyExists
	}
	return err
}

func (s *Store) CreateContribution(ctx context.Context, c *model.Contribution) error {
	return insertContribution(ctx, s.pool, c)
}

func (s *Store) CreateContributions(ctx context.Context, cs []*model.Contribution) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, c := range cs {
		if err := insertContribution(ctx, tx, c); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// CountContributionsSince counts batches; rows from before migration 004 count as their own batch.
func (s *Store) CountContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(DISTINCT COALESCE(NULLIF(batch_id, ''), id)) FROM contributions WHERE device_hash=$1 AND created_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}

func (s *Store) DeleteContribution(ctx context.Context, id, deviceHash string) error {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM contributions WHERE id=$1 AND device_hash=$2`, id, deviceHash).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != model.StatusPending {
		return store.ErrNotPending
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM contributions WHERE id=$1 AND device_hash=$2 AND status='pending'`, id, deviceHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotPending // reviewed between the read and the delete
	}
	return nil
}

func (s *Store) MarkContributionsPublished(ctx context.Context, ids []string, version int, at time.Time) ([]model.Contribution, error) {
	if len(ids) == 0 {
		return []model.Contribution{}, nil
	}
	rows, err := s.pool.Query(ctx, `UPDATE contributions SET published_version=$1, published_at=$2
		WHERE id = ANY($3) AND status='accepted' AND published_version=0
		RETURNING `+contribColsNoPayload, version, at, ids)
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

func (s *Store) queryContribs(ctx context.Context, where string, args ...any) ([]model.Contribution, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+contribColsNoPayload+` FROM contributions `+where, args...)
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

func (s *Store) ListContributions(ctx context.Context, status string, limit int) ([]model.Contribution, error) {
	f, err := statusFilter(status)
	if err != nil {
		return nil, err
	}
	if f == "" {
		return s.queryContribs(ctx, fmt.Sprintf(`ORDER BY created_at DESC LIMIT %d`, clamp(limit)))
	}
	return s.queryContribs(ctx, fmt.Sprintf(`WHERE status=$1 ORDER BY created_at DESC LIMIT %d`, clamp(limit)), f)
}

func (s *Store) ListContributionsByDevice(ctx context.Context, deviceHash string, limit int) ([]model.Contribution, error) {
	return s.queryContribs(ctx, fmt.Sprintf(`WHERE device_hash=$1 ORDER BY created_at DESC LIMIT %d`, clamp(limit)), deviceHash)
}

func (s *Store) GetContribution(ctx context.Context, id string) (*model.Contribution, error) {
	c, err := scanContribution(s.pool.QueryRow(ctx, `SELECT `+contribCols+` FROM contributions WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

func (s *Store) SetContributionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Contribution, error) {
	if !model.IsReportStatus(status) {
		return nil, store.ErrInvalidStatus
	}
	tag, err := s.pool.Exec(ctx, `UPDATE contributions SET status=$1, review_note=$2, reviewed_at=$3 WHERE id=$4`, status, note, reviewedAt, id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, store.ErrNotFound
	}
	return s.GetContribution(ctx, id)
}

func (s *Store) CountRejectedContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM contributions WHERE device_hash=$1 AND status='rejected' AND reviewed_at >= $2`, deviceHash, since).Scan(&n)
	return n, err
}
