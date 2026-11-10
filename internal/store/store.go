// Package store defines the persistence interface used by the HTTP API and
// provides three implementations: an in-memory store for DEV_MODE, a SQLite
// store for a single-binary deployment, and a PostgreSQL/PostGIS store for
// the real service (see docs/implementation/DATABASE_CHOICE.md). All are
// safe for concurrent use.
//
// All times passed in and out are UTC. "status" arguments accept one of
// model.ReportStatuses, or StatusAll ("all" / "") to mean no filter.
package store

import (
	"context"
	"errors"
	"time"

	"piyo-backend/internal/model"
)

// StatusAll is the ListReports status filter that matches every report.
const StatusAll = "all"

// Sentinel errors returned by every implementation.
var (
	ErrNotFound      = errors.New("store: not found")
	ErrAlreadyExists = errors.New("store: already exists")
	ErrInvalidStatus = errors.New("store: invalid status")
	// ErrNotPending is returned when a device withdraws a contribution that
	// was already reviewed (accepted or rejected rows are kept: they are the
	// review history and the auto-block evidence).
	ErrNotPending = errors.New("store: not pending")
)

// Store is the persistence contract. Implementations must be goroutine-safe.
type Store interface {
	// Reports
	CreateReport(ctx context.Context, r *model.Report) error
	CountReportsSince(ctx context.Context, deviceHash string, since time.Time) (int, error)
	ListReports(ctx context.Context, status string, limit int) ([]model.Report, error)
	GetReport(ctx context.Context, id string) (*model.Report, error)
	// SetReportStatus changes the status and records ReviewedAt = reviewedAt.
	SetReportStatus(ctx context.Context, id, status string, reviewedAt time.Time) (*model.Report, error)
	// CountRejectedSince counts reports of the device whose status is
	// "rejected" and whose ReviewedAt is at or after since.
	CountRejectedSince(ctx context.Context, deviceHash string, since time.Time) (int, error)

	// Suggestions (proposed data changes; same statuses as reports)
	CreateSuggestion(ctx context.Context, sg *model.Suggestion) error
	CountSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error)
	// ListSuggestions filters by status (StatusAll for none) and, when
	// lineID != "", by line.
	ListSuggestions(ctx context.Context, status, lineID string, limit int) ([]model.Suggestion, error)
	GetSuggestion(ctx context.Context, id string) (*model.Suggestion, error)
	SetSuggestionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Suggestion, error)
	// CountRejectedSuggestionsSince counts rejected suggestions reviewed at or after since.
	CountRejectedSuggestionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error)

	// Contributions (one route per row, sent from a local data provider; same statuses)
	CreateContribution(ctx context.Context, c *model.Contribution) error
	// CreateContributions inserts every row or none (one request = one batch).
	CreateContributions(ctx context.Context, cs []*model.Contribution) error
	// CountContributionsSince counts the device's batches (model.Contribution.QuotaKey)
	// created at or after since, so splitting a request into rows does not use more quota.
	CountContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error)
	// DeleteContribution removes a pending contribution of the device (withdrawn from the
	// app). ErrNotFound when it does not exist or belongs to another device, ErrNotPending
	// when it was already reviewed.
	DeleteContribution(ctx context.Context, id, deviceHash string) error
	// MarkContributionsPublished sets PublishedVersion/PublishedAt on the given accepted,
	// not yet published contributions and returns those it changed (so a repeated call
	// notifies nobody twice).
	MarkContributionsPublished(ctx context.Context, ids []string, version int, at time.Time) ([]model.Contribution, error)
	// ListContributions filters by status (StatusAll for none). Payloads are omitted.
	ListContributions(ctx context.Context, status string, limit int) ([]model.Contribution, error)
	// ListContributionsByDevice returns the device's contributions newest first, payloads omitted.
	ListContributionsByDevice(ctx context.Context, deviceHash string, limit int) ([]model.Contribution, error)
	GetContribution(ctx context.Context, id string) (*model.Contribution, error)
	SetContributionStatus(ctx context.Context, id, status, note string, reviewedAt time.Time) (*model.Contribution, error)
	CountRejectedContributionsSince(ctx context.Context, deviceHash string, since time.Time) (int, error)

	// NPS
	CreateNPS(ctx context.Context, n *model.NPS) error
	ListNPS(ctx context.Context, limit int) ([]model.NPS, error)

	// Blocks (one per device; SetBlock upserts)
	SetBlock(ctx context.Context, b model.Block) error
	// GetBlock returns nil, nil when the device has no active block (missing
	// or expired).
	GetBlock(ctx context.Context, deviceHash string) (*model.Block, error)
	DeleteBlock(ctx context.Context, deviceHash string) error
	ListBlocks(ctx context.Context) ([]model.Block, error)

	// Push tokens (FCM; one per device, SetPushToken upserts). GetPushToken
	// returns "" and no error when the device has none.
	SetPushToken(ctx context.Context, deviceHash, token string, updatedAt time.Time) error
	GetPushToken(ctx context.Context, deviceHash string) (string, error)
	DeletePushToken(ctx context.Context, deviceHash string) error
	// DeletePushTokensBefore purges tokens not refreshed since before.
	DeletePushTokensBefore(ctx context.Context, before time.Time) (int, error)

	Close() error
}

// normalizeStatus validates a ListReports status filter. It returns "" for
// "no filter".
func normalizeStatus(status string) (string, error) {
	if status == "" || status == StatusAll {
		return "", nil
	}
	if !model.IsReportStatus(status) {
		return "", ErrInvalidStatus
	}
	return status, nil
}

// clampLimit applies a sane default and maximum to list limits.
func clampLimit(limit int) int {
	const def, max = 100, 1000
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}
