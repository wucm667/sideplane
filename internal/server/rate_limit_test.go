package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wucm667/sideplane/internal/store"
)

func TestEnrollmentRateLimitReturnsTooManyRequests(t *testing.T) {
	clock := &rateLimitFakeClock{now: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)}
	nodeStore := store.NewMemoryNodeStore()
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:     nodeStore,
		Freshness: DefaultFreshnessPolicy(),
		RateLimits: RateLimitConfig{
			EnrollmentLimit:   2,
			OperatorAuthLimit: 100,
			Window:            time.Minute,
			Now:               clock.Now,
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := rateLimitJSONRequest(http.MethodPost, "/api/enroll", `{"token":"wrong","nodeId":"node-a"}`, "203.0.113.10:4000")
		handler.ServeHTTP(rec, req)
		assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", "enrollment token rejected")
	}

	rec := httptest.NewRecorder()
	req := rateLimitJSONRequest(http.MethodPost, "/api/enroll", `{"token":"wrong","nodeId":"node-a"}`, "203.0.113.10:4000")
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusTooManyRequests, "too_many_requests", "rate limit exceeded; retry later")
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}

	token, err := nodeStore.CreateEnrollmentToken(context.Background(), clock.Now().Add(time.Hour), clock.Now())
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	rec = httptest.NewRecorder()
	req = rateLimitJSONRequest(http.MethodPost, "/api/enroll", `{"token":"`+token.Token+`","nodeId":"node-ok"}`, "203.0.113.11:4000")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusOK)
}

func TestOperatorAuthRateLimitCountsFailuresOnly(t *testing.T) {
	clock := &rateLimitFakeClock{now: time.Date(2026, 6, 20, 13, 0, 0, 0, time.UTC)}
	handler, err := NewHandlerWithConfig(HandlerConfig{
		Store:         store.NewMemoryNodeStore(),
		Freshness:     DefaultFreshnessPolicy(),
		OperatorToken: "correct-token",
		RateLimits: RateLimitConfig{
			EnrollmentLimit:   100,
			OperatorAuthLimit: 2,
			Window:            time.Minute,
			Now:               clock.Now,
		},
	})
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := rateLimitJSONRequest(http.MethodPost, "/api/enrollment-tokens", `{}`, "203.0.113.20:4000")
		req.Header.Set("Authorization", "Bearer wrong-token")
		handler.ServeHTTP(rec, req)
		assertAPIError(t, rec, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))
	}

	rec := httptest.NewRecorder()
	req := rateLimitJSONRequest(http.MethodPost, "/api/enrollment-tokens", `{}`, "203.0.113.20:4000")
	req.Header.Set("Authorization", "Bearer wrong-token")
	handler.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusTooManyRequests, "too_many_requests", "rate limit exceeded; retry later")

	rec = httptest.NewRecorder()
	req = rateLimitJSONRequest(http.MethodPost, "/api/enrollment-tokens", `{}`, "203.0.113.20:4000")
	req.Header.Set("Authorization", "Bearer correct-token")
	handler.ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)
}

func TestRateLimiterEvictsExpiredKeys(t *testing.T) {
	clock := &rateLimitFakeClock{now: time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)}
	limiter := newFixedWindowRateLimiterWithMaxKeys(1, time.Minute, 2, clock.Now)

	if ok, _ := limiter.allow("node-a"); !ok {
		t.Fatal("first key was unexpectedly limited")
	}
	if ok, _ := limiter.allow("node-b"); !ok {
		t.Fatal("second key was unexpectedly limited")
	}
	if got := limiter.len(); got != 2 {
		t.Fatalf("limiter entries = %d, want 2", got)
	}

	clock.Advance(time.Minute + time.Second)
	if deleted := limiter.pruneExpired(clock.Now()); deleted != 2 {
		t.Fatalf("deleted entries = %d, want 2", deleted)
	}
	if got := limiter.len(); got != 0 {
		t.Fatalf("limiter entries after prune = %d, want 0", got)
	}

	if ok, _ := limiter.allow("node-c"); !ok {
		t.Fatal("new key after prune was unexpectedly limited")
	}
	if ok, _ := limiter.allow("node-d"); !ok {
		t.Fatal("second new key after prune was unexpectedly limited")
	}
	if ok, _ := limiter.allow("node-e"); !ok {
		t.Fatal("max-key eviction should keep accepting new keys")
	}
	if got := limiter.len(); got != 2 {
		t.Fatalf("limiter entries after max-key eviction = %d, want 2", got)
	}
}

func rateLimitJSONRequest(method string, path string, body string, remoteAddr string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	return req
}

type rateLimitFakeClock struct {
	now time.Time
}

func (c *rateLimitFakeClock) Now() time.Time {
	return c.now
}

func (c *rateLimitFakeClock) Advance(duration time.Duration) {
	c.now = c.now.Add(duration)
}
