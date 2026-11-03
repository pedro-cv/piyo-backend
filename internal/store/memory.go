// In-memory Store implementation, used when DEV_MODE=true. Nothing survives a
// restart; the seed package repopulates it on boot. Guarded by a RWMutex.
package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"piyo-backend/internal/model"
)

// Memory is a Store backed by Go maps.
type Memory struct {
	mu          sync.RWMutex
	reports     map[string]*model.Report
	nps         map[string]*model.NPS
	blocks      map[string]model.Block
	suggestions map[string]*model.Suggestion
	contribs    map[string]*model.Contribution
	pushTokens  map[string]pushToken
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		reports: make(map[string]*model.Report),
		nps:     make(map[string]*model.NPS),
		blocks:  make(map[string]model.Block),
	}
}

var _ Store = (*Memory)(nil)

// CreateReport stores a copy of r.
func (m *Memory) CreateReport(_ context.Context, r *model.Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.reports[r.ID]; exists {
		return ErrAlreadyExists
	}
	cp := *r
	m.reports[r.ID] = &cp
	return nil
}

// CountReportsSince counts the device's reports created at or after since.
func (m *Memory) CountReportsSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, r := range m.reports {
		if r.DeviceHash == deviceHash && !r.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// ListReports returns reports newest first, optionally filtered by status.
func (m *Memory) ListReports(_ context.Context, status string, limit int) ([]model.Report, error) {
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	limit = clampLimit(limit)

	m.mu.RLock()
	out := make([]model.Report, 0, len(m.reports))
	for _, r := range m.reports {
		if filter == "" || r.Status == filter {
			out = append(out, *r)
		}
	}
	m.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetReport returns a copy of the report or ErrNotFound.
func (m *Memory) GetReport(_ context.Context, id string) (*model.Report, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.reports[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *r
	return &cp, nil
}

// SetReportStatus updates status and ReviewedAt and returns the new state.
func (m *Memory) SetReportStatus(_ context.Context, id, status string, reviewedAt time.Time) (*model.Report, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.reports[id]
	if !ok {
		return nil, ErrNotFound
	}
	r.Status = status
	t := reviewedAt
	r.ReviewedAt = &t
	cp := *r
	return &cp, nil
}

// CountRejectedSince counts rejected reports reviewed at or after since.
func (m *Memory) CountRejectedSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, r := range m.reports {
		if r.DeviceHash == deviceHash && r.Status == model.StatusRejected &&
			r.ReviewedAt != nil && !r.ReviewedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// CreateNPS stores a copy of n.
func (m *Memory) CreateNPS(_ context.Context, n *model.NPS) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.nps[n.ID]; exists {
		return ErrAlreadyExists
	}
	cp := *n
	cp.Reasons = append([]string(nil), n.Reasons...)
	m.nps[n.ID] = &cp
	return nil
}

// ListNPS returns NPS entries newest first.
func (m *Memory) ListNPS(_ context.Context, limit int) ([]model.NPS, error) {
	limit = clampLimit(limit)
	m.mu.RLock()
	out := make([]model.NPS, 0, len(m.nps))
	for _, n := range m.nps {
		cp := *n
		cp.Reasons = append([]string(nil), n.Reasons...)
		out = append(out, cp)
	}
	m.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetBlock inserts or replaces the device's block.
func (m *Memory) SetBlock(_ context.Context, b model.Block) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blocks[b.DeviceHash] = b
	return nil
}

// GetBlock returns the active block or nil when missing/expired.
func (m *Memory) GetBlock(_ context.Context, deviceHash string) (*model.Block, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.blocks[deviceHash]
	if !ok || b.Expired(time.Now().UTC()) {
		return nil, nil
	}
	return &b, nil
}

// DeleteBlock removes the device's block; ErrNotFound if none exists.
func (m *Memory) DeleteBlock(_ context.Context, deviceHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.blocks[deviceHash]; !ok {
		return ErrNotFound
	}
	delete(m.blocks, deviceHash)
	return nil
}

// ListBlocks returns every block (including expired ones) ordered by Until.
func (m *Memory) ListBlocks(_ context.Context) ([]model.Block, error) {
	m.mu.RLock()
	out := make([]model.Block, 0, len(m.blocks))
	for _, b := range m.blocks {
		out = append(out, b)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Until.Before(out[j].Until) })
	return out, nil
}

// Close is a no-op for the in-memory store.
func (m *Memory) Close() error { return nil }
