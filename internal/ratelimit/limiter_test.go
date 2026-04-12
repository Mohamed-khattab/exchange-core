package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trading/matching-engine/internal/auth"
)

func TestTokenBucketAllow(t *testing.T) {
	b := newTokenBucket(10, 5) // 10/sec, burst of 5

	// Should allow up to burst
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Errorf("expected Allow() = true on call %d", i)
		}
	}

	// Next call should be denied (burst exhausted)
	if b.Allow() {
		t.Error("expected Allow() = false after burst exhausted")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	b := newTokenBucket(100, 10) // 100/sec, burst of 10

	// Exhaust burst
	for i := 0; i < 10; i++ {
		b.Allow()
	}
	if b.Allow() {
		t.Error("expected denial after burst")
	}

	// Simulate time passing (100ms = 10 tokens at 100/sec)
	b.lastRefill = time.Now().Add(-100 * time.Millisecond)
	if !b.Allow() {
		t.Error("expected Allow() = true after refill")
	}
}

func TestTokenBucketDoesNotExceedMax(t *testing.T) {
	b := newTokenBucket(1000, 5)

	// Simulate 10 seconds passing
	b.lastRefill = time.Now().Add(-10 * time.Second)

	// Should only accumulate up to burst (5), not 10000
	allowed := 0
	for i := 0; i < 20; i++ {
		if b.Allow() {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("expected max burst of 5, got %d", allowed)
	}
}

func TestRegistryPerClient(t *testing.T) {
	reg := NewRegistry(100, 3, 100, 3)

	// Client A uses their burst
	for i := 0; i < 3; i++ {
		if !reg.Allow("client-a", true) {
			t.Errorf("client-a should be allowed on call %d", i)
		}
	}
	if reg.Allow("client-a", true) {
		t.Error("client-a should be denied after burst")
	}

	// Client B is independent -- should still have full burst
	for i := 0; i < 3; i++ {
		if !reg.Allow("client-b", true) {
			t.Errorf("client-b should be allowed on call %d", i)
		}
	}
}

func TestRegistrySeparateReadWrite(t *testing.T) {
	reg := NewRegistry(10, 2, 10, 5) // write burst=2, read burst=5

	// Exhaust write burst
	reg.Allow("client-x", true)
	reg.Allow("client-x", true)
	if reg.Allow("client-x", true) {
		t.Error("write should be denied after burst of 2")
	}

	// Read should still work (separate bucket)
	if !reg.Allow("client-x", false) {
		t.Error("read should still be allowed (separate bucket)")
	}
}

func TestRegistryCleanup(t *testing.T) {
	reg := NewRegistry(10, 5, 10, 5)

	// Use two clients
	reg.Allow("stale-client", false)
	reg.Allow("active-client", false)

	// Make stale-client old
	reg.mu.Lock()
	reg.limiters["stale-client"].lastSeen = time.Now().Add(-15 * time.Minute)
	reg.mu.Unlock()

	reg.cleanup()

	reg.mu.Lock()
	defer reg.mu.Unlock()
	if _, ok := reg.limiters["stale-client"]; ok {
		t.Error("stale client should have been cleaned up")
	}
	if _, ok := reg.limiters["active-client"]; !ok {
		t.Error("active client should still exist")
	}
}

// ── Middleware Tests ──────────────────────────────────────────────────────────

func withAuthContext(r *http.Request, clientID string) *http.Request {
	type contextKey string
	ctx := context.WithValue(r.Context(), contextKey("api_key_id"), clientID)
	return r.WithContext(ctx)
}

func TestMiddlewareNoAuthContext(t *testing.T) {
	reg := NewRegistry(10, 2, 10, 2)
	handler := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Request without auth context should pass through (public endpoint)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200 for no-auth request, got %d", rec.Code)
	}
}

func TestMiddlewareReturns429(t *testing.T) {
	reg := NewRegistry(10, 1, 10, 1) // burst of 1
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	handler := reg.Middleware(okHandler)

	makeReq := func() int {
		req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
		// Inject auth context using the actual auth package context key
		ctx := context.WithValue(req.Context(), auth.TestContextKey(), "test-client")
		req = req.WithContext(ctx)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	first := makeReq()
	if first != 200 {
		t.Errorf("first request should be 200, got %d", first)
	}

	second := makeReq()
	if second != 429 {
		t.Errorf("second request should be 429, got %d", second)
	}
}

func TestMiddleware429ResponseFormat(t *testing.T) {
	reg := NewRegistry(10, 0, 10, 0) // zero burst = always deny
	handler := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	ctx := context.WithValue(req.Context(), auth.TestContextKey(), "client-x")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 429 {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Error("expected Retry-After: 1 header")
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Error("expected JSON content type")
	}

	var resp apiResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Status != "error" {
		t.Errorf("resp status = %s", resp.Status)
	}
	if resp.Error != "rate limit exceeded" {
		t.Errorf("resp error = %s", resp.Error)
	}
}

func TestMiddlewareWriteVsRead(t *testing.T) {
	reg := NewRegistry(10, 1, 10, 1)
	handler := reg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Exhaust write burst
	req := httptest.NewRequest("POST", "/v1/orders", nil)
	ctx := context.WithValue(req.Context(), auth.TestContextKey(), "client-y")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("first POST should be 200, got %d", rec.Code)
	}

	// Read should still work (separate bucket)
	req2 := httptest.NewRequest("GET", "/v1/orderbook/BTC-USD", nil)
	ctx2 := context.WithValue(req2.Context(), auth.TestContextKey(), "client-y")
	req2 = req2.WithContext(ctx2)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("GET should still work, got %d", rec2.Code)
	}
}

func TestStartCleanupStopsOnCancel(t *testing.T) {
	reg := NewRegistry(10, 5, 10, 5)
	ctx, cancel := context.WithCancel(context.Background())
	reg.StartCleanup(ctx)
	cancel() // Should not panic
}
