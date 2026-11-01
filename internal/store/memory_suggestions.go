// Suggestion methods of the in-memory store.
package store

import (
	"context"
	"sort"
	"time"

	"piyo-backend/internal/model"
)

func (m *Memory) suggestionsMap() map[string]*model.Suggestion {
	if m.suggestions == nil {
		m.suggestions = make(map[string]*model.Suggestion)
	}
	return m.suggestions
}

// CreateSuggestion stores a copy of sg.
func (m *Memory) CreateSuggestion(_ context.Context, sg *model.Suggestion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sm := m.suggestionsMap()
	if _, exists := sm[sg.ID]; exists {
		return ErrAlreadyExists
	}
	cp := *sg
	cp.Payload = append([]byte(nil), sg.Payload...)
	sm[sg.ID] = &cp
	return nil
}

// CountSuggestionsSince counts the device's suggestions created at or after since.
func (m *Memory) CountSuggestionsSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.suggestions {
		if s.DeviceHash == deviceHash && !s.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// ListSuggestions returns suggestions newest first.
func (m *Memory) ListSuggestions(_ context.Context, status, lineID string, limit int) ([]model.Suggestion, error) {
	filter, err := normalizeStatus(status)
	if err != nil {
		return nil, err
	}
	limit = clampLimit(limit)
	m.mu.RLock()
	out := make([]model.Suggestion, 0, len(m.suggestions))
	for _, s := range m.suggestions {
		if (filter == "" || s.Status == filter) && (lineID == "" || s.LineID == lineID) {
			out = append(out, *s)
		}
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetSuggestion returns a copy or ErrNotFound.
func (m *Memory) GetSuggestion(_ context.Context, id string) (*model.Suggestion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.suggestions[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *s
	return &cp, nil
}

// SetSuggestionStatus updates status, note and ReviewedAt.
func (m *Memory) SetSuggestionStatus(_ context.Context, id, status, note string, reviewedAt time.Time) (*model.Suggestion, error) {
	if !model.IsReportStatus(status) {
		return nil, ErrInvalidStatus
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.suggestions[id]
	if !ok {
		return nil, ErrNotFound
	}
	s.Status = status
	s.ReviewNote = note
	t := reviewedAt
	s.ReviewedAt = &t
	cp := *s
	return &cp, nil
}

// CountRejectedSuggestionsSince counts rejected suggestions reviewed at or after since.
func (m *Memory) CountRejectedSuggestionsSince(_ context.Context, deviceHash string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.suggestions {
		if s.DeviceHash == deviceHash && s.Status == model.StatusRejected && s.ReviewedAt != nil && !s.ReviewedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
