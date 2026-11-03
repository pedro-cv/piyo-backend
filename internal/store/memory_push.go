// Push-token methods of the in-memory store.
package store

import (
	"context"
	"time"
)

type pushToken struct {
	token     string
	updatedAt time.Time
}

// SetPushToken upserts the device's token.
func (m *Memory) SetPushToken(_ context.Context, deviceHash, token string, updatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pushTokens == nil {
		m.pushTokens = make(map[string]pushToken)
	}
	m.pushTokens[deviceHash] = pushToken{token: token, updatedAt: updatedAt.UTC()}
	return nil
}

// GetPushToken returns the device's token or "".
func (m *Memory) GetPushToken(_ context.Context, deviceHash string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pushTokens[deviceHash].token, nil
}

// DeletePushToken removes the device's token (no error when missing).
func (m *Memory) DeletePushToken(_ context.Context, deviceHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pushTokens, deviceHash)
	return nil
}

// DeletePushTokensBefore purges tokens last refreshed before before.
func (m *Memory) DeletePushTokensBefore(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for h, t := range m.pushTokens {
		if t.updatedAt.Before(before) {
			delete(m.pushTokens, h)
			n++
		}
	}
	return n, nil
}
