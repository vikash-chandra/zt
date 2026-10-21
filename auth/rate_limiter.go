package auth

import (
	"sync"
	"time"
)

type ipAttempt struct {
	count     int
	firstSeen time.Time
}

// LoginRateLimiter restricts login attempts per client IP
type LoginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string]*ipAttempt
	maxAttempts int
	window      time.Duration
}

// NewLoginRateLimiter creates a new rate limiter (default: 5 attempts / 5 mins)
func NewLoginRateLimiter(maxAttempts int, window time.Duration) *LoginRateLimiter {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	rl := &LoginRateLimiter{
		attempts:    make(map[string]*ipAttempt),
		maxAttempts: maxAttempts,
		window:      window,
	}

	// Periodic cleanup of stale IP records
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		for range ticker.C {
			rl.cleanup()
		}
	}()

	return rl
}

// IsAllowed returns true if the IP has not exceeded max attempts
func (rl *LoginRateLimiter) IsAllowed(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	att, exists := rl.attempts[ip]
	if !exists {
		return true
	}

	if now.Sub(att.firstSeen) > rl.window {
		delete(rl.attempts, ip)
		return true
	}

	return att.count < rl.maxAttempts
}

// RecordFailure records a failed login attempt for the IP
func (rl *LoginRateLimiter) RecordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	att, exists := rl.attempts[ip]
	if !exists || now.Sub(att.firstSeen) > rl.window {
		rl.attempts[ip] = &ipAttempt{count: 1, firstSeen: now}
		return
	}
	att.count++
}

// Reset clears recorded failures for the IP upon successful login
func (rl *LoginRateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

func (rl *LoginRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, att := range rl.attempts {
		if now.Sub(att.firstSeen) > rl.window {
			delete(rl.attempts, ip)
		}
	}
}
