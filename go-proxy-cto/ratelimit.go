package main

import (
	"sync"
	"time"
)

type TokenBucket struct {
	capacity   int
	tokens     float64
	rate       float64
	lastRefill time.Time
	mu         sync.Mutex
}

func NewTokenBucket(rpm int) *TokenBucket {
	if rpm <= 0 {
		rpm = 60
	}
	return &TokenBucket{
		capacity:   rpm,
		tokens:     float64(rpm),
		rate:       float64(rpm) / 60.0,
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > float64(tb.capacity) {
		tb.tokens = float64(tb.capacity)
	}
	tb.lastRefill = now
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

type RateLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*TokenBucket
}

func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*TokenBucket),
	}
}

func (rl *RateLimiter) Allow(key string, rpm int) bool {
	if rpm <= 0 {
		return true
	}
	rl.mu.RLock()
	b, ok := rl.buckets[key]
	rl.mu.RUnlock()
	if !ok {
		rl.mu.Lock()
		b = NewTokenBucket(rpm)
		rl.buckets[key] = b
		rl.mu.Unlock()
	}
	return b.Allow()
}
