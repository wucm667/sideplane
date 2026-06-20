package server

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultEnrollmentRateLimit   = 20
	DefaultOperatorAuthRateLimit = 60
	DefaultRateLimitWindow       = time.Minute
	defaultRateLimitMaxKeys      = 4096
)

// RateLimitConfig controls in-process brute-force protection for auth-like
// endpoints. Set a disable flag, or pass limit 0 from the server command, to
// turn off a specific limiter for local/test deployments.
type RateLimitConfig struct {
	EnrollmentLimit     int
	OperatorAuthLimit   int
	Window              time.Duration
	DisableEnrollment   bool
	DisableOperatorAuth bool
	Now                 func() time.Time
}

type normalizedRateLimitConfig struct {
	enrollmentLimit     int
	operatorAuthLimit   int
	window              time.Duration
	disableEnrollment   bool
	disableOperatorAuth bool
	now                 func() time.Time
}

type fixedWindowRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time
	entries map[string]rateLimitEntry
}

type rateLimitEntry struct {
	windowStart time.Time
	count       int
	lastSeen    time.Time
}

func normalizeRateLimitConfig(cfg RateLimitConfig) normalizedRateLimitConfig {
	normalized := normalizedRateLimitConfig{
		enrollmentLimit:   cfg.EnrollmentLimit,
		operatorAuthLimit: cfg.OperatorAuthLimit,
		window:            cfg.Window,
		now:               cfg.Now,
	}
	if normalized.enrollmentLimit == 0 && !cfg.DisableEnrollment {
		normalized.enrollmentLimit = DefaultEnrollmentRateLimit
	}
	if normalized.operatorAuthLimit == 0 && !cfg.DisableOperatorAuth {
		normalized.operatorAuthLimit = DefaultOperatorAuthRateLimit
	}
	if normalized.window == 0 {
		normalized.window = DefaultRateLimitWindow
	}
	if normalized.now == nil {
		normalized.now = utcNow
	}
	normalized.disableEnrollment = cfg.DisableEnrollment || normalized.enrollmentLimit <= 0 || normalized.window <= 0
	normalized.disableOperatorAuth = cfg.DisableOperatorAuth || normalized.operatorAuthLimit <= 0 || normalized.window <= 0
	return normalized
}

func newFixedWindowRateLimiter(limit int, window time.Duration, now func() time.Time) *fixedWindowRateLimiter {
	return newFixedWindowRateLimiterWithMaxKeys(limit, window, defaultRateLimitMaxKeys, now)
}

func newFixedWindowRateLimiterWithMaxKeys(limit int, window time.Duration, maxKeys int, now func() time.Time) *fixedWindowRateLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}
	if maxKeys <= 0 {
		maxKeys = defaultRateLimitMaxKeys
	}
	if now == nil {
		now = utcNow
	}
	return &fixedWindowRateLimiter{
		limit:   limit,
		window:  window,
		maxKeys: maxKeys,
		now:     now,
		entries: map[string]rateLimitEntry{},
	}
}

func (l *fixedWindowRateLimiter) allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	now := l.now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneExpiredLocked(now)
	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.windowStart.Add(l.window)) {
		l.entries[key] = rateLimitEntry{windowStart: now, count: 1, lastSeen: now}
		l.enforceMaxKeysLocked()
		return true, 0
	}
	if entry.count >= l.limit {
		entry.lastSeen = now
		l.entries[key] = entry
		return false, entry.windowStart.Add(l.window).Sub(now)
	}
	entry.count++
	entry.lastSeen = now
	l.entries[key] = entry
	return true, 0
}

func (l *fixedWindowRateLimiter) pruneExpired(now time.Time) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pruneExpiredLocked(now.UTC())
}

func (l *fixedWindowRateLimiter) len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *fixedWindowRateLimiter) pruneExpiredLocked(now time.Time) int {
	deleted := 0
	for key, entry := range l.entries {
		if !now.Before(entry.windowStart.Add(l.window)) {
			delete(l.entries, key)
			deleted++
		}
	}
	return deleted
}

func (l *fixedWindowRateLimiter) enforceMaxKeysLocked() {
	for len(l.entries) > l.maxKeys {
		var oldestKey string
		var oldest time.Time
		first := true
		for key, entry := range l.entries {
			if first || entry.lastSeen.Before(oldest) {
				oldestKey = key
				oldest = entry.lastSeen
				first = false
			}
		}
		delete(l.entries, oldestKey)
	}
}

func remoteRateLimitKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	if value := strings.TrimSpace(r.RemoteAddr); value != "" {
		return value
	}
	return "unknown"
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded; retry later")
}
