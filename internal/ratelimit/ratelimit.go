// Package ratelimit provides per-host rate limiting for API calls and downloads.
package ratelimit

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const maxLimiters = 5000

type limiterEntry struct {
	limiter  *rate.Limiter
	lastUsed time.Time
}

// Manager keeps a rate.Limiter per hostname. Limiters for hosts that have
// been idle for longer than the eviction threshold are candidates for cleanup.
type Manager struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
}

// New returns a new rate-limit Manager.
func New() *Manager {
	return &Manager{limiters: make(map[string]*limiterEntry)}
}

// normalizeHost strips port and lowercases.
func normalizeHost(host string) string {
	host = strings.ToLower(host)
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}

// classify returns the rate limit profile for the given host.
func classify(host string) (rate.Limit, int) {
	switch {
	case strings.Contains(host, "x.com") || strings.Contains(host, "twitter"):
		return rate.Limit(2), 2
	case strings.Contains(host, "reddit"):
		return rate.Limit(1), 2
	case strings.Contains(host, "instagram") || strings.Contains(host, "cdninstagram"):
		return rate.Limit(1), 2
	default:
		return rate.Limit(10), 10
	}
}

// GetLimiter returns the limiter for a host, creating it on first access.
func (m *Manager) GetLimiter(host string) *rate.Limiter {
	host = normalizeHost(host)
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.limiters[host]; ok {
		e.lastUsed = time.Now()
		return e.limiter
	}
	r, b := classify(host)
	m.evictLocked()
	l := rate.NewLimiter(r, b)
	m.limiters[host] = &limiterEntry{limiter: l, lastUsed: time.Now()}
	return l
}

// EvictIdle removes limiters that have not been accessed in the given duration.
func (m *Manager) EvictIdle(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	for host, e := range m.limiters {
		if e.lastUsed.Before(cutoff) {
			delete(m.limiters, host)
		}
	}
}

// Len returns the current number of cached limiters.
func (m *Manager) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.limiters)
}

// evictLocked trims the oldest entries if the map exceeds maxLimiters.
// Must be called while mu is held.
func (m *Manager) evictLocked() {
	if len(m.limiters) <= maxLimiters {
		return
	}
	type kv struct {
		host string
		at   time.Time
	}
	list := make([]kv, 0, len(m.limiters))
	for h, e := range m.limiters {
		list = append(list, kv{host: h, at: e.lastUsed})
	}
	// Sort oldest-first so we drop the most stale entries.
	for i := 0; i < len(list)-1; i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].at.Before(list[i].at) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	toDrop := len(m.limiters) - maxLimiters
	for i := 0; i < toDrop; i++ {
		delete(m.limiters, list[i].host)
	}
}
