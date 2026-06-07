package auth

import (
	"sync"
	"time"
)

// RateLimiter throttles login attempts per client key (remote IP). It is a
// coarse front-line guard in front of PAM's own pam_faillock: it bounds how
// fast a single source can even reach the verifier, independent of which user
// they target, so faillock isn't the only thing standing between an attacker
// and an unbounded online guessing rate.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*bucket
	max      int           // failures before lockout
	window   time.Duration // failures are counted within this trailing window
	lockout  time.Duration // how long a tripped key stays locked
}

type bucket struct {
	fails    int
	first    time.Time
	lockedTo time.Time
}

// NewRateLimiter: allow `max` failures per `window`, then lock for `lockout`.
func NewRateLimiter(max int, window, lockout time.Duration) *RateLimiter {
	return &RateLimiter{attempts: make(map[string]*bucket), max: max, window: window, lockout: lockout}
}

// Allowed reports whether key may attempt a login right now.
func (r *RateLimiter) Allowed(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.attempts[key]
	if b == nil {
		return true
	}
	return time.Now().After(b.lockedTo)
}

// Fail records a failed attempt and trips the lockout once max is exceeded
// within the window.
func (r *RateLimiter) Fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	b := r.attempts[key]
	if b == nil || now.Sub(b.first) > r.window {
		b = &bucket{first: now}
		r.attempts[key] = b
	}
	b.fails++
	if b.fails >= r.max {
		b.lockedTo = now.Add(r.lockout)
		b.fails = 0
		b.first = now
	}
}

// Reset clears a key's counters after a successful login.
func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, key)
}
