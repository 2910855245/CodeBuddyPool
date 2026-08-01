package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type AccountPool struct {
	mu           sync.RWMutex
	accounts     []*Account
	known        map[string]bool
	baseDir      string
	accountsFile string
	lastReload   time.Time
	totalSuccess int64
}

func NewAccountPool(baseDir string) *AccountPool {
	return &AccountPool{
		known:        make(map[string]bool),
		baseDir:      baseDir,
		accountsFile: filepath.Join(baseDir, "accounts.jsonl"),
	}
}

func (p *AccountPool) Load() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loadLocked()
}

func (p *AccountPool) loadLocked() {
	f, err := os.Open(p.accountsFile)
	if err != nil {
		return
	}
	defer f.Close()

	loaded, skipped := 0, 0
	filePhones := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var a Account
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			skipped++
			continue
		}
		if a.Phone == "" {
			continue
		}
		filePhones[a.Phone] = true
		if p.known[a.Phone] {
			continue
		}
		if a.AccessToken == "" {
			log.Warn("pool: skip account missing access_token", zap.String("phone", a.Phone))
			skipped++
			continue
		}
		// 初始化运行时积分：用文件中的 credits_total 作为起点，
		// 避免 healthCheck 被风控返回 0 时显示 0 积分
		if a.creditsLeft == 0 && a.CreditsTotal > 0 {
			a.creditsLeft = a.CreditsTotal
		}
		p.accounts = append(p.accounts, &a)
		p.known[a.Phone] = true
		loaded++
	}

	// 同步删除: 文件中已移除的账号从内存剔除 (如 Dashboard 删号),
	// 同时清掉 known 标记, 同号码重新注册后可再次入池
	removed := 0
	kept := p.accounts[:0]
	for _, a := range p.accounts {
		if filePhones[a.Phone] {
			kept = append(kept, a)
		} else {
			delete(p.known, a.Phone)
			removed++
		}
	}
	p.accounts = kept

	p.lastReload = time.Now()
	if loaded > 0 || skipped > 0 || removed > 0 {
		log.Info("pool: load done",
			zap.Int("loaded", loaded), zap.Int("skipped", skipped),
			zap.Int("removed", removed), zap.Int("total", len(p.accounts)))
	}
}

func (p *AccountPool) ReloadIfNeeded() {
	p.mu.RLock()
	stale := time.Since(p.lastReload) > time.Duration(ReloadInterval)*time.Second
	p.mu.RUnlock()
	if !stale {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	before := len(p.accounts)
	p.loadLocked()
	if delta := len(p.accounts) - before; delta > 0 {
		log.Info("pool: hot added", zap.Int("n", delta))
	}
}

// Get 取一个可用账号。excludeSet 为本轮已失败的黑名单 (phone->true)，避免切号重试时撞回同一个号。
// 调度策略 (学 new-api): 按 lastUsed 分两档——久未使用的优先，同档内按 successCount 加权随机,
// 避免绝对均匀轮转把请求打到响应慢/积分少的号上。
func (p *AccountPool) Get(excludeSet ...map[string]bool) *Account {
	p.mu.Lock()
	defer p.mu.Unlock()
	excluded := map[string]bool{}
	if len(excludeSet) > 0 {
		excluded = excludeSet[0]
	}
	cand := []*Account{}
	for _, a := range p.accounts {
		if excluded[a.Phone] {
			continue
		}
		if a.IsAvailable() {
			cand = append(cand, a)
		}
	}
	if len(cand) == 0 {
		return nil
	}

	// 分两档: idle 超 5 分钟为 "冷号" (优先), 否则 "热号"
	// 冷号内按 successCount 加权随机; 全是热号时也在热号内加权随机
	idleThreshold := time.Now().Add(-5 * time.Minute)
	var cold, hot []*Account
	for _, a := range cand {
		if a.lastUsed.Before(idleThreshold) {
			cold = append(cold, a)
		} else {
			hot = append(hot, a)
		}
	}
	pool := cold
	if len(pool) == 0 {
		pool = hot
	}

	// 权重 = successCount + 1 (新号也有基础权重 1), 加权随机
	totalWeight := 0
	weights := make([]int, len(pool))
	for i, a := range pool {
		w := int(a.successCount) + 1
		// errorCount 高的降权
		if a.errorCount > 0 {
			w = w / (int(a.errorCount) + 1)
			if w < 1 {
				w = 1
			}
		}
		weights[i] = w
		totalWeight += w
	}

	// 加权随机选择
	r := rand.Intn(totalWeight)
	cumulative := 0
	var acc *Account
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			acc = pool[i]
			break
		}
	}
	if acc == nil {
		acc = pool[0] // 兜底
	}
	acc.inUse = true
	acc.lastUsed = time.Now()
	return acc
}

func (p *AccountPool) Release(a *Account, errMsg string) {
	if a == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a.inUse = false
	if errMsg == "" {
		// 成功: 连续失败计数归零 (学 new-api)
		a.consecutiveErrors = 0
		return
	}
	low := strings.ToLower(errMsg)

	// 限流类错误 (429/rate-limit): 进入短暂冷却而非杀号, 到期自动恢复 (学 new-api 四状态机)
	if isRateLimitError(low) {
		a.cooldownUntil = time.Now().Add(time.Duration(CooldownSeconds) * time.Second)
		log.Info("release: rate-limit hit, cooldown",
			zap.String("phone", a.Phone),
			zap.Int("cooldown_sec", CooldownSeconds))
		return
	}

	a.errorCount++
	a.consecutiveErrors++
	// 连续失败超阈值: 进冷却, 防止坏号反复被选拖慢整体 (学 new-api 自动禁用)
	if a.consecutiveErrors >= MaxConsecutiveErrors {
		a.cooldownUntil = time.Now().Add(time.Duration(CooldownSeconds) * time.Second)
		log.Warn("release: consecutive errors exceeded, cooldown",
			zap.String("phone", a.Phone),
			zap.Int("consecutive", a.consecutiveErrors),
			zap.Int("cooldown_sec", CooldownSeconds))
	}
	if isQuotaError(low) {
		a.exhausted = true
		a.exhaustedAt = time.Now()
	}
	if isAuthError(low) {
		a.dead = true
	}
}

func (p *AccountPool) IncrementSuccess(a *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a != nil {
		a.successCount++
		a.consecutiveErrors = 0 // 成功: 连续失败归零
	}
	p.totalSuccess++
}

func (p *AccountPool) Stats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var avail, dead, exhausted, inUse, cooldown int
	var credits float64
	now := time.Now()
	for _, a := range p.accounts {
		if a.dead {
			dead++
		} else if a.exhausted {
			exhausted++
		} else if !a.cooldownUntil.IsZero() && now.Before(a.cooldownUntil) {
			cooldown++
		} else if a.inUse {
			inUse++
		} else {
			avail++
		}
		credits += a.CreditsTotal
	}
	return map[string]interface{}{
		"status": "ok", "total": len(p.accounts), "available": avail,
		"exhausted": exhausted, "dead": dead, "in_use": inUse, "cooldown": cooldown,
		"total_success": p.totalSuccess, "credits_total": credits,
		"checkin_enabled": EnableCheckin,
		"low_available":   avail < LowPoolWaterMark,
	}
}

func (p *AccountPool) AccountList() []map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]map[string]interface{}, 0, len(p.accounts))
	for _, a := range p.accounts {
		// 显示积分: 优先用 creditsLeft (healthCheck 更新的实时值),
		// 为 0 时 fallback 到 CreditsTotal (文件持久化值),
		// 避免新加载账号在 healthCheck 被风控前显示 0 积分
		credits := a.creditsLeft
		if credits == 0 && a.CreditsTotal > 0 {
			credits = a.CreditsTotal
		}
		cooldown := ""
		if !a.cooldownUntil.IsZero() && time.Now().Before(a.cooldownUntil) {
			cooldown = fmt.Sprintf("%.0fs", time.Until(a.cooldownUntil).Seconds())
		}
		out = append(out, map[string]interface{}{
			"phone": a.Phone, "uid": a.UID, "in_use": a.inUse,
			"exhausted": a.exhausted, "dead": a.dead,
			"cooldown": cooldown, "consecutive_errors": a.consecutiveErrors,
			"success": a.successCount, "errors": a.errorCount, "credits": credits,
		})
	}
	return out
}

// Snapshot 返回账号切片副本 (供签到巡检等遍历, 不持锁)
func (p *AccountPool) Snapshot() []*Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Account, len(p.accounts))
	copy(out, p.accounts)
	return out
}

// GetAccount 按手机号取池内账号指针, 不存在返回 nil (供注册后自动签到用)
func (p *AccountPool) GetAccount(phone string) *Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, a := range p.accounts {
		if a.Phone == phone {
			return a
		}
	}
	return nil
}

// InjectAccount 运行时注入新账号 (不从文件加载), 用于注册流程直接入池。
// 已知号码直接跳过, 避免重复。新号 creditsLeft 用 CreditsTotal 初始化。
func (p *AccountPool) InjectAccount(a *Account) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.known[a.Phone] {
		return
	}
	if a.creditsLeft == 0 && a.CreditsTotal > 0 {
		a.creditsLeft = a.CreditsTotal
	}
	p.accounts = append(p.accounts, a)
	p.known[a.Phone] = true
	log.Info("pool: account injected", zap.String("phone", a.Phone))
}

func (p *AccountPool) PersistDirty() int {
	p.mu.Lock()
	dirty := []*Account{}
	for _, a := range p.accounts {
		a.mu.Lock()
		if a.IsDirty() {
			dirty = append(dirty, a)
		}
		a.mu.Unlock()
	}
	if len(dirty) == 0 {
		p.mu.Unlock()
		return 0
	}

	latest := make(map[string][]byte, len(dirty))
	for _, a := range dirty {
		a.mu.Lock()
		b, err := json.Marshal(a)
		a.mu.Unlock()
		if err == nil {
			latest[a.Phone] = b
		}
	}
	p.mu.Unlock()

	src, err := os.Open(p.accountsFile)
	if err != nil {
		return 0
	}
	tmp, err := os.CreateTemp(p.baseDir, ".accounts.jsonl.tmp-*")
	if err != nil {
		src.Close()
		return 0
	}

	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	writer := bufio.NewWriter(tmp)
	replaced := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row struct {
			Phone string `json:"phone"`
		}
		if err := json.Unmarshal(line, &row); err == nil {
			if newB, ok := latest[row.Phone]; ok {
				writer.Write(newB)
				writer.WriteByte('\n')
				replaced++
				delete(latest, row.Phone)
				continue
			}
		}
		writer.Write(line)
		writer.WriteByte('\n')
	}
	for _, b := range latest {
		writer.Write(b)
		writer.WriteByte('\n')
		replaced++
	}
	writer.Flush()
	src.Close()
	tmp.Close()

	if err := os.Rename(tmp.Name(), p.accountsFile); err != nil {
		os.Remove(tmp.Name())
		return 0
	}
	p.mu.Lock()
	for _, a := range dirty {
		a.mu.Lock()
		a.clearDirty()
		a.mu.Unlock()
	}
	p.mu.Unlock()
	log.Info("persist: accounts written back", zap.Int("count", replaced))
	return replaced
}

// tokenKeepAliveLoop 后台保活：定期对所有号调 ensureFreshToken，
// 在 token 临近过期前主动刷新，避免请求时才触发刷新导致延迟/失败。
// dead 号也尝试刷新——若 refreshToken 成功说明 token 仍有效, 自动复活 (学 new-api)。
func tokenKeepAliveLoop(pool *AccountPool, engine *engineClient) {
	time.Sleep(120 * time.Second)
	for {
		for _, a := range pool.Snapshot() {
			wasDead := a.dead
			if err := engine.refreshToken(a, false); err != nil {
				if wasDead {
					log.Warn("keepalive: dead account refresh still failing",
						zap.String("phone", a.Phone), zap.Error(err))
				} else {
					log.Warn("keepalive: refresh failed", zap.String("phone", a.Phone), zap.Error(err))
				}
				continue
			}
			// refreshToken 成功: dead 号自动复活
			if wasDead {
				a.mu.Lock()
				a.dead = false
				a.errorCount = 0
				a.mu.Unlock()
				log.Info("keepalive: dead account revived", zap.String("phone", a.Phone))
			}
			// 刷新间隔: 避免连续请求触发风控
			time.Sleep(2 * time.Second)
		}
		pool.PersistDirty()
		time.Sleep(TokenRefreshTTL * time.Second / 2)
	}
}

// checkinLoop 每日凌晨定时签到: 每天 CheckinHour:CheckinMinute (UTC+8) 批量跑一轮,
// 号与号之间隔 CheckinDelay 秒, 模拟正常用户分散操作, 避免集中打签到/账单接口触发风控。
// 错过当天的号(如重启后注入的新号)靠注册时自动签到补, 或等次日。
func checkinLoop(pool *AccountPool, engine *engineClient) {
	loc := time.FixedZone("CST", 8*3600)
	for {
		now := time.Now().In(loc)
		// 下一个签到时刻
		next := time.Date(now.Year(), now.Month(), now.Day(), CheckinHour, CheckinMinute, 0, 0, loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		log.Info("checkin: next scheduled", zap.String("at", next.Format("2006-01-02 15:04")))
		time.Sleep(time.Until(next))
		if EnableCheckin {
			runCheckinOnce(pool, engine, loc)
		}
	}
}

func runCheckinOnce(pool *AccountPool, engine *engineClient, loc *time.Location) {
	day := time.Now().In(loc).Format("2006-01-02")
	first := true
	for _, a := range pool.Snapshot() {
		if a.dead || !a.NeedsCheckin(day) {
			continue
		}
		// 签到间隔延迟, 避免瞬间打多发票账接口触发风控
		if !first {
			time.Sleep(time.Duration(CheckinDelay) * time.Second)
		}
		first = false
		bonus, err := engine.dailyCheckin(a)
		if err != nil {
			log.Warn("checkin: failed", zap.String("phone", a.Phone), zap.Error(err))
			continue
		}
		a.MarkCheckin(day)
		if bonus > 0 {
			log.Info("checkin: bonus", zap.String("phone", a.Phone), zap.Int("credit", bonus))
		}
		// 签到后刷新积分并持久化 (服务器可跳过, 避免账单接口风控)
		if !SkipHealthCheck {
			if err := engine.healthCheck(a); err == nil {
				a.mu.Lock()
				a.CreditsTotal = a.creditsLeft
				a.markDirty()
				a.mu.Unlock()
			}
		}
	}
}

func isQuotaError(msg string) bool {
	for _, kw := range QuotaErrorKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// isAuthError: 只对 401 类认证失败判 dead (new-api 机制)。
// 403 不再杀号——CodeBuddy 的 403 多为临时风控/权限波动, 杀号会导致误判大面积掉号。
func isAuthError(msg string) bool {
	for _, kw := range []string{`"code":401`, "unauthorized", "token expired", "invalid token", "refresh failed", "no refresh token"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// isRateLimitError: 429/限流类错误, 号本身有效, 只是暂时被打频控。
// 学 new-api: 限流不杀号不冷却, 只记录, 下次请求照常可用。
func isRateLimitError(msg string) bool {
	for _, kw := range []string{`"code":429`, "rate limit", "frequency limit", "too many requests", "请求过于频繁", "请求频率"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// GetWithRetry 切号重试 (学 new-api 多渠道轮转)。
// fn 为单次请求逻辑: 传入账号, 返回响应体错误 (空串=成功)。
// 失败 → Release 当前号 → 加入本轮黑名单 → 退避 → Get 新号重试, 最多切 maxSwitches 个号。
// 限流类错误照常切号 (该号本轮不再选, 但不杀号); quota/auth 类错误 Release 里已标记状态。
func (p *AccountPool) GetWithRetry(maxSwitches int, fn func(a *Account) string) (acc *Account, relErr string, attempts []string) {
	blacklist := map[string]bool{}
	relErr = ""
	for i := 0; i <= maxSwitches; i++ {
		acc = p.Get(blacklist)
		if acc == nil {
			relErr = "no available account in pool"
			return nil, relErr, attempts
		}
		phone := acc.Phone
		relErr = fn(acc)
		if relErr == "" {
			p.Release(acc, "")
			return acc, "", attempts
		}
		// 失败: 先 Release (内部按错误类型标记 exhausted/dead/限流), 再加黑名单
		p.Release(acc, relErr)
		blacklist[phone] = true
		attempts = append(attempts, phone+": "+truncate(relErr, 80))
		log.Warn("retry: switch account",
			zap.Int("attempt", i+1),
			zap.String("phone", phone),
			zap.String("err", truncate(relErr, 100)))
		// 指数退避: 1s, 2s, 4s... (最多 8s)
		if i < maxSwitches {
			backoff := time.Duration(1<<uint(i)) * time.Second
			if backoff > 8*time.Second {
				backoff = 8 * time.Second
			}
			time.Sleep(backoff)
		}
	}
	return nil, relErr, attempts
}
