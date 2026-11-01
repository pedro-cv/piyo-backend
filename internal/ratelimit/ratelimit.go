// Package ratelimit is a small in-memory token-bucket limiter keyed by an
// opaque string (the API uses the client IP). Keys live only in process
// memory: nothing is persisted or logged, which is what keeps IP handling
// compliant with the "minimum personal data" rule. A background goroutine
// prunes keys that have been idle for a while so memory stays bounded.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter allows up to `perMinute` requests per key per minute, with a burst
// equal to perMinute.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64

	stopOnce sync.Once
	stop     chan struct{}
	now      func() time.Time
}

// New builds a limiter and starts its pruning goroutine. Call Stop when done.
func New(perMinute int) *Limiter {
	if perMinute <= 0 {
		perMinute = 1
	}
	l := &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(perMinute) / 60.0,
		burst:   float64(perMinute),
		stop:    make(chan struct{}),
		now:     time.Now,
	}
	go l.pruneLoop(time.Minute, 10*time.Minute)
	return l
}

// Allow consumes one token for key and reports whether the request may
// proceed.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Stop halts the pruning goroutine. Safe to call more than once.
func (l *Limiter) Stop() {
	l.stopOnce.Do(func() { close(l.stop) })
}

// pruneLoop deletes buckets idle for longer than idle every `every`.
func (l *Limiter) pruneLoop(every, idle time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.prune(idle)
		}
	}
}

func (l *Limiter) prune(idle time.Duration) {
	cutoff := l.now().Add(-idle)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}
