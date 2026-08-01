package main

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	log = zap.NewNop()
	m.Run()
}

func newTestPool() *AccountPool {
	return &AccountPool{
		known: make(map[string]bool),
	}
}

func TestInjectAccount_New(t *testing.T) {
	pool := newTestPool()
	a := &Account{
		Phone:        "+8613800000001",
		AccessToken:   "tok-1",
		CreditsTotal: 2000,
	}
	pool.InjectAccount(a)
	if len(pool.accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(pool.accounts))
	}
	if !pool.known[a.Phone] {
		t.Fatal("phone should be in known map")
	}
	if pool.accounts[0].creditsLeft != 2000 {
		t.Fatalf("creditsLeft should be 2000, got %f", pool.accounts[0].creditsLeft)
	}
}

func TestInjectAccount_Duplicate(t *testing.T) {
	pool := newTestPool()
	a1 := &Account{Phone: "+8613800000002", AccessToken: "tok-1", CreditsTotal: 1000}
	pool.InjectAccount(a1)
	// 注入同号码
	a2 := &Account{Phone: "+8613800000002", AccessToken: "tok-2", CreditsTotal: 3000}
	pool.InjectAccount(a2)
	if len(pool.accounts) != 1 {
		t.Fatalf("expected 1 account (dedup), got %d", len(pool.accounts))
	}
	if pool.accounts[0].AccessToken != "tok-1" {
		t.Fatal("first account should not be overwritten")
	}
}

func TestInjectAccount_CreditsLeftInit(t *testing.T) {
	pool := newTestPool()
	// CreditsTotal > 0 但 creditsLeft == 0 → 应自动初始化
	a := &Account{Phone: "+8613800000003", AccessToken: "tok", CreditsTotal: 1500}
	pool.InjectAccount(a)
	if a.creditsLeft != 1500 {
		t.Fatalf("creditsLeft should be initialized to 1500, got %f", a.creditsLeft)
	}
}

func TestInjectAccount_CreditsLeftPreserved(t *testing.T) {
	pool := newTestPool()
	// creditsLeft 已有值 → 不应被覆盖
	a := &Account{Phone: "+8613800000004", AccessToken: "tok", CreditsTotal: 1500}
	a.creditsLeft = 800
	pool.InjectAccount(a)
	if a.creditsLeft != 800 {
		t.Fatalf("creditsLeft should stay 800, got %f", a.creditsLeft)
	}
}

// ---- isAuthError 精确匹配 ----

func TestIsAuthError_PreciseMatch(t *testing.T) {
	tests := []struct {
		msg    string
		expect bool
	}{
		{`{"code":401,"msg":"unauthorized"}`, true},
		{`{"code":403,"msg":"forbidden"}`, true},
		{`{"error":"token expired"}`, true},
		{`{"error":"invalid token"}`, true},
		{`refresh failed: 400`, true},
		{`no refresh token`, true},
		// 以下不应误判为 auth error
		{`{"code":14018,"msg":"额度已用尽"}`, false},   // 积分耗尽
		{`{"port":4011,"status":"ok"}`, false},          // 端口号含 401
		{`requestId: f85b880a-0ce6-4d65-9472-1f533de99893`, false},
		{`forbidden zone access denied`, false},         // 不再匹配裸 "forbidden"
		{`HTTP 4011 gateway`, false},                     // 不再匹配裸 "401"
	}
	for _, tt := range tests {
		got := isAuthError(tt.msg)
		if got != tt.expect {
			t.Errorf("isAuthError(%q) = %v, want %v", tt.msg, got, tt.expect)
		}
	}
}

// ---- isQuotaError 不误判 rate-limit ----

func TestIsQuotaError(t *testing.T) {
	tests := []struct {
		msg    string
		expect bool
	}{
		{`{"code":11217,"msg":"余额不足"}`, true},
		{`insufficient credit`, true},
		{`quota exceeded`, true},
		{`credit limit reached`, true},
		{`no credit left`, true},
		{`rate limit exceeded`, false},
		{`frequency limit`, false},
		{`{"code":14018,"msg":"额度已用尽"}`, false}, // 不在关键词列表
	}
	for _, tt := range tests {
		got := isQuotaError(tt.msg)
		if got != tt.expect {
			t.Errorf("isQuotaError(%q) = %v, want %v", tt.msg, got, tt.expect)
		}
	}
}

// ---- Release 标记 exhausted / dead ----

func TestRelease_QuotaError(t *testing.T) {
	pool := newTestPool()
	a := &Account{Phone: "+8613800000010", AccessToken: "tok", CreditsTotal: 1000}
	pool.InjectAccount(a)
	a.inUse = true

	pool.Release(a, `{"code":11217,"msg":"余额不足"}`)
	if !a.exhausted {
		t.Fatal("should be marked exhausted after quota error")
	}
	if a.dead {
		t.Fatal("should not be dead after quota error")
	}
}

func TestRelease_AuthError(t *testing.T) {
	pool := newTestPool()
	a := &Account{Phone: "+8613800000011", AccessToken: "tok", CreditsTotal: 1000}
	pool.InjectAccount(a)
	a.inUse = true

	pool.Release(a, `{"code":401,"msg":"unauthorized"}`)
	if !a.dead {
		t.Fatal("should be marked dead after auth error")
	}
}

func TestRelease_NoError(t *testing.T) {
	pool := newTestPool()
	a := &Account{Phone: "+8613800000012", AccessToken: "tok", CreditsTotal: 1000}
	pool.InjectAccount(a)
	a.inUse = true

	pool.Release(a, "")
	if a.exhausted || a.dead {
		t.Fatal("should not be marked exhausted or dead on empty error")
	}
	if a.inUse {
		t.Fatal("inUse should be cleared")
	}
}

// ---- Account.IsAvailable / exhausted 恢复 ----

func TestIsAvailable_ExhaustedRecovery(t *testing.T) {
	a := &Account{
		Phone:        "+8613800000020",
		AccessToken:  "tok",
		CreditsTotal: 1000,
		ExpiresAt:    time.Now().Add(1 * time.Hour).UnixMilli(),
	}
	a.exhausted = true
	a.exhaustedAt = time.Now()
	if a.IsAvailable() {
		t.Fatal("should not be available immediately after exhaustion")
	}

	// 12 小时后恢复
	a.exhaustedAt = time.Now().Add(-13 * time.Hour)
	if !a.IsAvailable() {
		t.Fatal("should be available after 12h cooldown")
	}
	if a.exhausted {
		t.Fatal("exhausted flag should be cleared after recovery")
	}
}

// ---- msgCounter 并发安全 ----

func TestNextMsgID_Concurrent(t *testing.T) {
	msgCounter.Store(0)
	done := make(chan struct{})
	const n = 100
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		go func() {
			ids <- nextMsgID()
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}
	close(ids)
	seen := make(map[string]bool)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate msg ID: %s", id)
		}
		seen[id] = true
	}
	if got := msgCounter.Load(); got != int64(n) {
		t.Fatalf("msgCounter = %d, want %d", got, n)
	}
}
