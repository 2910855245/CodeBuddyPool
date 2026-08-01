package main

import (
	"sync"
	"time"
)

type KeyUsage struct {
	Requests     int64 `json:"requests"`
	Success      int64 `json:"success"`
	Failures     int64 `json:"failures"`
	TokensIn     int64 `json:"tokens_in"`
	TokensOut    int64 `json:"tokens_out"`
	RateLimited  int64 `json:"rate_limited"`
	LastRequest  string `json:"last_request"`
}

type UsageTracker struct {
	mu    sync.RWMutex
	stats map[string]*KeyUsage
}

func NewUsageTracker() *UsageTracker {
	return &UsageTracker{
		stats: make(map[string]*KeyUsage),
	}
}

func (ut *UsageTracker) getOrCreate(key string) *KeyUsage {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	if u, ok := ut.stats[key]; ok {
		return u
	}
	u := &KeyUsage{}
	ut.stats[key] = u
	return u
}

func (ut *UsageTracker) RecordRequest(key string) {
	u := ut.getOrCreate(key)
	ut.mu.Lock()
	u.Requests++
	u.LastRequest = time.Now().Format("2006-01-02 15:04:05")
	ut.mu.Unlock()
}

func (ut *UsageTracker) RecordSuccess(key string) {
	ut.mu.Lock()
	if u, ok := ut.stats[key]; ok {
		u.Success++
	}
	ut.mu.Unlock()
}

func (ut *UsageTracker) RecordFailure(key string) {
	ut.mu.Lock()
	if u, ok := ut.stats[key]; ok {
		u.Failures++
	}
	ut.mu.Unlock()
}

func (ut *UsageTracker) RecordTokens(key string, in, out int64) {
	ut.mu.Lock()
	if u, ok := ut.stats[key]; ok {
		u.TokensIn += in
		u.TokensOut += out
	}
	ut.mu.Unlock()
}

func (ut *UsageTracker) RecordRateLimited(key string) {
	ut.mu.Lock()
	if u, ok := ut.stats[key]; ok {
		u.RateLimited++
	}
	ut.mu.Unlock()
}

func (ut *UsageTracker) GetStats(key string) *KeyUsage {
	ut.mu.RLock()
	defer ut.mu.RUnlock()
	if u, ok := ut.stats[key]; ok {
		c := *u
		return &c
	}
	return &KeyUsage{}
}

func (ut *UsageTracker) AllStats() map[string]*KeyUsage {
	ut.mu.RLock()
	defer ut.mu.RUnlock()
	out := make(map[string]*KeyUsage, len(ut.stats))
	for k, v := range ut.stats {
		c := *v
		out[k] = &c
	}
	return out
}
