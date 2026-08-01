package main

import (
	"sync"
	"time"
)

// Account CodeBuddy 账号运行时状态 + 持久化字段
// 持久化字段和 codebuddy_register.py 生成的 accounts.jsonl 对齐
type Account struct {
	// 持久化字段 —— 从 accounts.jsonl 读入
	Phone        string  `json:"phone"`
	UID          string  `json:"uid"`
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresAt    int64   `json:"expires_at"`    // unix ms
	CreditsTotal float64 `json:"credits_total"` // 签到总积分
	RegisteredAt string  `json:"registered_at"` // 注册时间

	// 运行时状态
	mu                 sync.Mutex
	lastUsed           time.Time
	lastRequestAt      time.Time // 最近一次实际请求时间, 用于 per-account 限流间隔
	inUse              bool
	dead               bool      // token 彻底失效
	exhausted          bool      // 积分耗尽
	exhaustedAt        time.Time
	cooldownUntil      time.Time // 限流冷却: 429/rate-limit 后短暂冷却, 到期自动恢复
	creditsLeft        float64   // 当前剩余积分（运行时更新）
	successCount       int64
	errorCount         int64
	consecutiveErrors  int       // 连续失败计数: 超阈值进冷却, 成功归零
	lastCheckinDay     string    // 上次签到日期 (UTC+8, 2006-01-02)

	dirty bool
}

func (a *Account) markDirty()  { a.dirty = true }
func (a *Account) clearDirty() { a.dirty = false }
func (a *Account) IsDirty() bool {
	return a.dirty
}

// IsAvailable 是否可以分配
func (a *Account) IsAvailable() bool {
	if a.dead || a.inUse {
		return false
	}
	// 限流冷却: 429 后短暂冷却 (30s), 到期自动恢复, 无需人工干预
	if !a.cooldownUntil.IsZero() && time.Now().Before(a.cooldownUntil) {
		return false
	}
	// per-account 最小请求间隔: 距上次请求不足 PerAccountMinInterval 秒则跳过
	// 避免并发请求把单个号打爆触发风控
	if !a.lastRequestAt.IsZero() && time.Since(a.lastRequestAt) < time.Duration(PerAccountMinInterval)*time.Second {
		return false
	}
	// access token 过期: 仅当没有 refresh_token 时才判死;
	// 有 refresh_token 的交给 engine.ensureFreshToken 自动续期
	if a.ExpiresAt > 0 && time.Now().UnixMilli() > a.ExpiresAt && a.RefreshToken == "" {
		a.dead = true
		return false
	}
	if a.exhausted {
		// 积分耗尽: 12 小时后重试 (每日 UTC+8 0 点重置)
		if time.Since(a.exhaustedAt) < 12*time.Hour {
			return false
		}
		a.exhausted = false
	}
	return true
}

func (a *Account) MarkExhausted() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exhausted = true
	a.exhaustedAt = time.Now()
}

func (a *Account) MarkDead() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dead = true
}

// SetCredits 更新运行时剩余积分 (并发安全)
func (a *Account) SetCredits(v float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creditsLeft = v
}

// NeedsCheckin 当天是否还未签到
func (a *Account) NeedsCheckin(day string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastCheckinDay != day
}

// MarkCheckin 记录当天已签到
func (a *Account) MarkCheckin(day string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastCheckinDay = day
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
