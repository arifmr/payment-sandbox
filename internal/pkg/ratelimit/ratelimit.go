// Package ratelimit provides an in-memory token-bucket limiter.
//
// It exists to blunt credential brute-force against the auth endpoints. A single
// process holds its own buckets, so with N replicas the effective limit is N×rate —
// acceptable for slowing an attacker down, but not a substitute for an edge/gateway
// limiter backed by shared state. See agent_documentation/06-trade-offs.md.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter enforces `rate` events per `window` per key, allowing short bursts up to
// `burst`. Keys are arbitrary strings (client IP, email, …).
//
// The token bucket refills continuously rather than resetting on a fixed boundary,
// so an attacker cannot line up two full bursts either side of a window edge — the
// failure mode of naive fixed-window counters.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	// tokens added per nanosecond
	refillPerNs float64
	burst       float64

	// idleTTL bounds memory: buckets untouched for this long are evicted.
	idleTTL time.Duration

	// now is injectable so tests do not have to sleep.
	now func() time.Time
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// New returns a limiter allowing `rate` events per `window` per key, with bursts up
// to `burst`. A rate of zero disables limiting entirely (Allow always returns true),
// which is how the feature is switched off by configuration.
func New(rate int, window time.Duration, burst int) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		idleTTL: 10 * window,
		now:     time.Now,
	}
	if rate <= 0 || window <= 0 {
		return l // disabled
	}
	if burst < 1 {
		burst = 1
	}
	l.refillPerNs = float64(rate) / float64(window.Nanoseconds())
	l.burst = float64(burst)
	return l
}

// Enabled reports whether this limiter actually restricts anything.
func (l *Limiter) Enabled() bool { return l.refillPerNs > 0 }

// Allow consumes one token for key and reports whether the event may proceed.
func (l *Limiter) Allow(key string) bool {
	if !l.Enabled() {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		// A brand-new key starts full, minus the token this call consumes.
		l.buckets[key] = &bucket{tokens: l.burst - 1, lastSeen: now}
		l.evictIdleLocked(now)
		return true
	}

	// Refill proportionally to elapsed time, capped at burst.
	elapsed := now.Sub(b.lastSeen)
	if elapsed > 0 {
		b.tokens += float64(elapsed.Nanoseconds()) * l.refillPerNs
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RetryAfter reports how long the caller should wait before one token is available.
// Returns zero when a token is already available.
func (l *Limiter) RetryAfter(key string) time.Duration {
	if !l.Enabled() {
		return 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		return 0
	}
	deficit := 1 - b.tokens
	if deficit <= 0 {
		return 0
	}
	return time.Duration(deficit / l.refillPerNs)
}

// Reset drops all state. Intended for tests.
func (l *Limiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buckets = make(map[string]*bucket)
}

// Len reports the number of tracked keys. Intended for tests asserting eviction.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// evictIdleLocked removes stale buckets. Without this, an attacker rotating source
// addresses would grow the map without bound — turning the defence into a memory
// exhaustion vector. Called only when a new key is inserted, so the cost is
// amortised against growth rather than paid on every request.
func (l *Limiter) evictIdleLocked(now time.Time) {
	if len(l.buckets) < 1024 {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.idleTTL {
			delete(l.buckets, k)
		}
	}
}
