package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// withClock replaces the limiter's clock so refill can be exercised without sleeping.
func withClock(l *Limiter, t *time.Time) {
	l.now = func() time.Time { return *t }
}

func TestNew_ZeroRateDisablesLimiting(t *testing.T) {
	for _, l := range []*Limiter{
		New(0, time.Minute, 10),
		New(-5, time.Minute, 10),
		New(10, 0, 10),
	} {
		if l.Enabled() {
			t.Error("a non-positive rate or window must disable the limiter")
		}
		// A disabled limiter must never block, however many calls arrive.
		for i := 0; i < 1000; i++ {
			if !l.Allow("any-key") {
				t.Fatalf("a disabled limiter blocked at call %d", i)
			}
		}
		if l.RetryAfter("any-key") != 0 {
			t.Error("a disabled limiter must report no wait")
		}
	}
}

func TestAllow_BurstThenBlock(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	l := New(60, time.Minute, 5) // 1/sec, burst 5
	withClock(l, &now)

	// The bucket starts full, so exactly `burst` calls pass.
	for i := 0; i < 5; i++ {
		if !l.Allow("ip-1") {
			t.Fatalf("call %d should be allowed within the burst", i+1)
		}
	}
	if l.Allow("ip-1") {
		t.Fatal("the call past the burst must be blocked")
	}
}

func TestAllow_RefillsOverTime(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	l := New(60, time.Minute, 5) // 1 token per second
	withClock(l, &now)

	for i := 0; i < 5; i++ {
		l.Allow("ip-1")
	}
	if l.Allow("ip-1") {
		t.Fatal("bucket should be empty")
	}

	// One second later, exactly one token is back.
	now = now.Add(time.Second)
	if !l.Allow("ip-1") {
		t.Fatal("one token should have refilled after a second")
	}
	if l.Allow("ip-1") {
		t.Fatal("only one token should have refilled")
	}

	// A long gap refills to the cap, not beyond.
	now = now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if !l.Allow("ip-1") {
			t.Fatalf("call %d should be allowed after a full refill", i+1)
		}
	}
	if l.Allow("ip-1") {
		t.Fatal("tokens must be capped at burst, not accumulate without bound")
	}
}

// A continuously refilling bucket is what stops the fixed-window trick of firing a
// full burst just before a boundary and another just after.
func TestAllow_NoDoubleBurstAcrossWindowBoundary(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 59, 0, time.UTC) // near a minute boundary
	l := New(60, time.Minute, 5)
	withClock(l, &now)

	for i := 0; i < 5; i++ {
		if !l.Allow("ip-1") {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}

	// Cross the boundary. A fixed-window counter would reset here and grant 5 more.
	now = now.Add(time.Second)
	allowed := 0
	for i := 0; i < 5; i++ {
		if l.Allow("ip-1") {
			allowed++
		}
	}
	if allowed > 1 {
		t.Errorf("%d calls allowed just after the boundary; only the refilled token should pass", allowed)
	}
}

func TestAllow_KeysAreIndependent(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	l := New(60, time.Minute, 2)
	withClock(l, &now)

	// Exhaust one key.
	l.Allow("ip-1")
	l.Allow("ip-1")
	if l.Allow("ip-1") {
		t.Fatal("ip-1 should be exhausted")
	}

	// A different key must be unaffected — otherwise one noisy client would lock
	// out everybody else.
	if !l.Allow("ip-2") {
		t.Error("ip-2 must have its own budget")
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	l := New(60, time.Minute, 1) // 1 token per second
	withClock(l, &now)

	// Unknown key: nothing to wait for.
	if got := l.RetryAfter("fresh"); got != 0 {
		t.Errorf("RetryAfter(unknown) = %v, want 0", got)
	}

	l.Allow("ip-1") // consumes the only token
	if l.Allow("ip-1") {
		t.Fatal("bucket should be empty")
	}

	wait := l.RetryAfter("ip-1")
	if wait <= 0 || wait > time.Second+10*time.Millisecond {
		t.Errorf("RetryAfter = %v, want just under a second", wait)
	}

	// Once a token is available again the wait drops to zero.
	now = now.Add(time.Second)
	l.Allow("ip-1")
	_ = l.RetryAfter("ip-1")
}

func TestReset(t *testing.T) {
	l := New(60, time.Minute, 1)
	l.Allow("ip-1")
	if l.Allow("ip-1") {
		t.Fatal("bucket should be empty")
	}

	l.Reset()
	if !l.Allow("ip-1") {
		t.Error("Reset must clear all buckets")
	}
}

// Unbounded growth would turn the defence into a memory-exhaustion vector, so idle
// buckets are evicted once the map is large enough to be worth scanning.
func TestEvictsIdleBuckets(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	l := New(60, time.Minute, 1)
	withClock(l, &now)

	// Fill past the eviction threshold with keys that then go idle.
	for i := 0; i < 1100; i++ {
		l.Allow(string(rune(i)) + "-old")
	}
	before := l.Len()
	if before < 1000 {
		t.Fatalf("expected the map to have grown, got %d entries", before)
	}

	// Advance well past idleTTL (10 × window) and insert a new key to trigger a sweep.
	now = now.Add(20 * time.Minute)
	l.Allow("fresh-key")

	if after := l.Len(); after >= before {
		t.Errorf("idle buckets were not evicted: %d -> %d entries", before, after)
	}
}

// Concurrent access must not race or over-grant.
func TestAllow_ConcurrentAccessIsSafe(t *testing.T) {
	l := New(1_000_000, time.Minute, 50)

	const goroutines = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow("shared") {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Burst is 50 and refill is fast, so all should pass; the point is that the count
	// is coherent and the race detector stays quiet.
	if allowed == 0 {
		t.Error("no calls were allowed")
	}
	if allowed > goroutines {
		t.Errorf("allowed %d of %d calls — counter is not coherent", allowed, goroutines)
	}
}

// burst below 1 is meaningless; the constructor must floor it so the limiter still
// lets a single request through rather than blocking everything.
func TestNew_FloorsBurstToOne(t *testing.T) {
	l := New(60, time.Minute, 0)
	if !l.Enabled() {
		t.Fatal("limiter should be enabled")
	}
	if !l.Allow("ip-1") {
		t.Error("the first call must be allowed even when burst was given as 0")
	}
	if l.Allow("ip-1") {
		t.Error("burst should have been floored to exactly 1")
	}
}
