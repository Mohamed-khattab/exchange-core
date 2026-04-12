package ratelimit

import (
	"context"
	"sync"
	"time"
)

// TokenBucket implements a simple token bucket rate limiter.
type TokenBucket struct {
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

func newTokenBucket(rate float64, burst int) TokenBucket {
	return TokenBucket{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rate,
		lastRefill: time.Now(),
	}
}

// Allow checks if a token is available and consumes one if so.
func (b *TokenBucket) Allow() bool {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.maxTokens {
		b.tokens = b.maxTokens
	}
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

type clientLimiter struct {
	write    TokenBucket
	read     TokenBucket
	lastSeen time.Time
}

// Registry manages per-client rate limiters.
type Registry struct {
	mu         sync.Mutex
	limiters   map[string]*clientLimiter
	writeRate  float64
	writeBurst int
	readRate   float64
	readBurst  int
}

// NewRegistry creates a rate limiter registry with the given limits.
func NewRegistry(writeRate float64, writeBurst int, readRate float64, readBurst int) *Registry {
	return &Registry{
		limiters:   make(map[string]*clientLimiter),
		writeRate:  writeRate,
		writeBurst: writeBurst,
		readRate:   readRate,
		readBurst:  readBurst,
	}
}

// Allow checks if the client is within their rate limit.
// isWrite distinguishes between write (order submit/cancel) and read (queries) requests.
func (reg *Registry) Allow(clientID string, isWrite bool) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()

	cl, ok := reg.limiters[clientID]
	if !ok {
		cl = &clientLimiter{
			write: newTokenBucket(reg.writeRate, reg.writeBurst),
			read:  newTokenBucket(reg.readRate, reg.readBurst),
		}
		reg.limiters[clientID] = cl
	}
	cl.lastSeen = time.Now()

	if isWrite {
		return cl.write.Allow()
	}
	return cl.read.Allow()
}

// StartCleanup runs a background goroutine that evicts stale client entries.
// Clients not seen for 10 minutes are removed.
func (reg *Registry) StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				reg.cleanup()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (reg *Registry) cleanup() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for key, cl := range reg.limiters {
		if cl.lastSeen.Before(cutoff) {
			delete(reg.limiters, key)
		}
	}
}
