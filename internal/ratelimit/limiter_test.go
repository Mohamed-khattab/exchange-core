package ratelimit

import (
	"testing"
	"time"
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
