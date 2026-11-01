// Contribution methods of the in-memory store.
package store

import (
	"context"
	"sort"
	"time"

	"piyo-backend/internal/model"
)

func (m *Memory) contribsMap() map[string]*model.Contribution {
	if m.contribs == nil {
		m.contribs = make(map[string]*model.Contribution)
	}
	return m.contribs
}

// CreateContribution stores a copy of c.
func (m *Memory) CreateContribution(_ context.Context, c *model.Contribution) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cm := m.contribsMap()
	if _, exists := cm[c.ID]; exists {
		return ErrAlreadyExists
	}
	cp := *c
	cp.Payload = append([]byte(nil), c.Payload...)
	cp.RouteIDs = append([]string(nil), c.RouteIDs...)
	cm[c.ID] = &cp
	return nil
}

// CountContributionsSince counts the device's contributions created at or after since.
func (m *Memory) CountContributionsSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, c := range m.contribs {
		if c.DeviceHash == deviceHash && !c.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (m *Memory) listContribs(filter func(*model.Contribution) bool, limit int) []model.Contribution {
	m.mu.RLock()
	out := make([]model.Contribution, 0, len(m.contribs))
	for _, c := range m.contribs {
		if filter(c) {
			out = append(out, c.Summary())
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ListContributions returns summaries newest first.
func (m *Memory) ListContributions(_ context.Context, status string, limit int) ([]model.Contribution, error) {
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	return m.listContribs(func(c *model.Contribution) bool { return filter == "" || c.Status == filter }, clampLimit(limit)), nil
}

// ListContributionsByDevice returns the device's summaries newest first.
func (m *Memory) ListContributionsByDevice(_ context.Context, deviceHash string, limit int) ([]model.Contribution, error) {
	return m.listContribs(func(c *model.Contribution) bool { return c.DeviceHash == deviceHash }, clampLimit(limit)), nil
}

// GetContribution returns a copy (with payload) or ErrNotFound.
func (m *Memory) GetContribution(_ context.Context, id string) (*model.Contribution, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.contribs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

// SetContributionStatus updates status, note and ReviewedAt.
func (m *Memory) SetContributionStatus(_ context.Context, id, status, note string, reviewedAt time.Time) (*model.Contribution, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.contribs[id]
	if !ok {
		return nil, ErrNotFound
	}
	c.Status = status
	c.ReviewNote = note
	t := reviewedAt
	c.ReviewedAt = &t
	cp := *c
	return &cp, nil
}

// CountRejectedContributionsSince counts rejected contributions reviewed at or after since.
func (m *Memory) CountRejectedContributionsSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, c := range m.contribs {
		if c.DeviceHash == deviceHash && c.Status == model.StatusRejected && c.ReviewedAt != nil && !c.ReviewedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
