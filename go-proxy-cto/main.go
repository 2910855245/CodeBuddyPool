package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var log *zap.Logger

// engineRef 包级 engine 引用, main() 启动时赋值, 供注入路由的注册自动签到用
var engineRef *engineClient

var checkinMu sync.Mutex

// 手动单号操作(签到/刷新)的按号冷却表, 防面板连点直接打账单接口触发风控
var manualOpTimes sync.Map // phone -> time.Time(上次操作时间)

// allowManualOp 同一号 ManualOpCooldown 秒内只允许一次手动签到/刷新
func allowManualOp(phone string) bool {
	now := time.Now()
	if v, ok := manualOpTimes.Load(phone); ok && now.Sub(v.(time.Time)) < ManualOpCooldown*time.Second {
		return false
	}
	manualOpTimes.Store(phone, now)
	return true
}

func shortUUID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:20]
}

var (
	keyMgr    *KeyManager
	rateLim   *RateLimiter
	usageTrk  *UsageTracker
	adminKey  string
)

func main() {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{"stdout"}
	l, err := cfg.Build()
	if err != nil {
		panic(err)
	}
	log = zap.New(ringCore{l.Core()})
	defer func() { _ = log.Sync() }()

	baseDir := os.Getenv("CODEBUDDY_BASE_DIR")
	if baseDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			baseDir = cwd
		}
	}
	for _, d := range []string{baseDir, filepath.Dir(baseDir)} {
		if _, err := os.Stat(filepath.Join(d, "accounts.jsonl")); err == nil {
			baseDir = d
			break
		}
	}
	initFileLog(baseDir)

	adminKey = os.Getenv("ADMIN_API_KEY")
	if adminKey == "" {
		adminKey = APIKey
	}

	keyMgr = NewKeyManager(filepath.Join(baseDir, "api_keys.jsonl"))
	keyMgr.Load()
	rateLim = NewRateLimiter()
	usageTrk = NewUsageTracker()

	log.Info("codebuddy-pool starting",
		zap.String("baseDir", baseDir),
		zap.String("listen", ListenAddr),
		zap.Bool("checkin", EnableCheckin))

	pool := NewAccountPool(baseDir)
	pool.Load()

	stats := pool.Stats()
	log.Info("pool ready",
		zap.Any("total", stats["total"]),
		zap.Any("available", stats["available"]))

	if stats["total"].(int) == 0 {
		log.Warn("accounts.jsonl is empty; run codebuddy_register.py to add accounts")
	}

	engine := newEngineClient()
	engineRef = engine

	go reloadLoop(pool)
	go persistLoop(pool)
	go checkinLoop(pool, engine)
	go tokenKeepAliveLoop(pool, engine)
	go keyReloadLoop()
	go creditPollLoop(pool, engine)

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	startTime := time.Now()

	r.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "CodeBuddy Pool - OK")
	})

	r.GET("/stats", func(c *gin.Context) {
		s := pool.Stats()
		s["uptime_sec"] = int(time.Since(startTime).Seconds())
		c.JSON(http.StatusOK, s)
	})

	r.GET("/v1/models", func(c *gin.Context) {
		var models []map[string]string
		seen := map[string]bool{}
		for name, mapped := range ModelMap {
			if seen[name] {
				continue
			}
			seen[name] = true
			models = append(models, map[string]string{
				"id": name, "object": "model", "owned_by": "codebuddy",
				"codebuddy_model": mapped,
			})
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": models})
	})

	// Claude Code 启动时会校验单个模型是否存在
	r.GET("/v1/models/:id", func(c *gin.Context) {
		id := c.Param("id")
		if mapped, ok := ModelMap[id]; ok {
			c.JSON(http.StatusOK, map[string]string{
				"id": id, "object": "model", "owned_by": "codebuddy",
				"codebuddy_model": mapped,
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "model not found"})
	})

	r.POST("/v1/chat/completions", apiKeyAuth, rateLimitMW, func(c *gin.Context) {
		key := c.GetString("api_key")
		usageTrk.RecordRequest(key)
		handleChatCompletions(c, pool, engine)
	})
	r.POST("/v1/messages", apiKeyAuth, rateLimitMW, func(c *gin.Context) {
		key := c.GetString("api_key")
		usageTrk.RecordRequest(key)
		handleAnthropicMessages(c, pool, engine)
	})

	admin := r.Group("/api/admin")
	admin.Use(adminKeyAuth)
	{
		admin.GET("/verify", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"valid": true})
		})
		admin.GET("/stats", func(c *gin.Context) {
			s := pool.Stats()
			s["uptime_sec"] = int(time.Since(startTime).Seconds())
			c.JSON(http.StatusOK, s)
		})
		admin.GET("/accounts", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"stats": pool.Stats(), "accounts": pool.AccountList()})
		})
		admin.POST("/reload", func(c *gin.Context) {
			before := len(pool.Snapshot())
			pool.Load()
			c.JSON(http.StatusOK, gin.H{"before": before, "after": len(pool.Snapshot())})
		})
		admin.POST("/checkin", func(c *gin.Context) {
			if !checkinMu.TryLock() {
				c.JSON(http.StatusConflict, gin.H{"error": "checkin already running"})
				return
			}
			go func() {
				defer checkinMu.Unlock()
				runCheckinOnce(pool, engine, time.FixedZone("CST", 8*3600))
			}()
			c.JSON(http.StatusOK, gin.H{"status": "checkin triggered"})
		})
		admin.GET("/logs", func(c *gin.Context) {
			n := 100
			if q := c.Query("n"); q != "" {
				if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 500 {
					n = v
				}
			}
			c.JSON(http.StatusOK, gin.H{"logs": globalLog.Recent(n)})
		})

		admin.GET("/keys", func(c *gin.Context) {
			keys := keyMgr.AllKeys()
			type keyInfo struct {
				Key       string    `json:"key"`
				Name      string    `json:"name"`
				Enabled   bool      `json:"enabled"`
				RateLimit int       `json:"rate_limit"`
				CreatedAt string    `json:"created_at"`
				LastUsed  string    `json:"last_used"`
				Usage     *KeyUsage `json:"usage"`
			}
			list := make([]keyInfo, 0, len(keys))
			for _, k := range keys {
				masked := k.Key[:12] + "..." + k.Key[len(k.Key)-4:]
				list = append(list, keyInfo{
					Key:       masked,
					Name:      k.Name,
					Enabled:   k.Enabled,
					RateLimit: k.RateLimit,
					CreatedAt: k.CreatedAt,
					LastUsed:  k.LastUsed,
					Usage:     usageTrk.GetStats(k.Key),
				})
			}
			c.JSON(http.StatusOK, gin.H{"keys": list})
		})
		admin.POST("/keys", func(c *gin.Context) {
			var body struct{ Name string `json:"name"` }
			c.ShouldBindJSON(&body)
			k := keyMgr.CreateKey(body.Name)
			c.JSON(http.StatusCreated, gin.H{
				"key": k.Key, "name": k.Name, "enabled": k.Enabled,
				"rate_limit": k.RateLimit, "created_at": k.CreatedAt,
			})
		})
		admin.DELETE("/keys/:key", func(c *gin.Context) {
			fullKey := keyMgr.FindByMasked(c.Param("key"))
			ok := keyMgr.DeleteKey(fullKey)
			if ok { c.JSON(http.StatusOK, gin.H{"deleted": true}) } else { c.JSON(http.StatusNotFound, gin.H{"error": "key not found"}) }
		})
		admin.PUT("/keys/:key/toggle", func(c *gin.Context) {
			fullKey := keyMgr.FindByMasked(c.Param("key"))
			k := keyMgr.ToggleKey(fullKey)
			if k == nil { c.JSON(http.StatusNotFound, gin.H{"error": "key not found"}); return }
			c.JSON(http.StatusOK, gin.H{"key": k.Key, "enabled": k.Enabled})
		})
		admin.POST("/accounts/:phone/checkin", func(c *gin.Context) {
			phone := c.Param("phone")
			if !allowManualOp(phone) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "操作过于频繁，请30秒后再试"})
				return
			}
			for _, a := range pool.Snapshot() {
				if a.Phone == phone {
					bonus, err := engine.dailyCheckin(a)
					if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
					c.JSON(http.StatusOK, gin.H{"phone": phone, "bonus": bonus})
					return
				}
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "phone not found"})
		})
		admin.POST("/accounts/:phone/refresh", func(c *gin.Context) {
			phone := c.Param("phone")
			if !allowManualOp(phone) {
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "操作过于频繁，请30秒后再试"})
				return
			}
			for _, a := range pool.Snapshot() {
				if a.Phone == phone {
					if err := engine.healthCheck(a); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()}); return }
					a.mu.Lock()
					a.CreditsTotal = a.creditsLeft
					a.markDirty()
					a.mu.Unlock()
					pool.PersistDirty()
					c.JSON(http.StatusOK, gin.H{"phone": phone, "credits": a.creditsLeft})
					return
				}
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "phone not found"})
		})
		admin.DELETE("/accounts/:phone", func(c *gin.Context) {
			phone := c.Param("phone")
			pool.mu.Lock()
			for i, a := range pool.accounts {
				if a.Phone == phone {
					pool.accounts = append(pool.accounts[:i], pool.accounts[i+1:]...)
					delete(pool.known, phone)
					pool.mu.Unlock()
					pool.PersistDirty()
					c.JSON(http.StatusOK, gin.H{"deleted": phone})
					return
				}
			}
			pool.mu.Unlock()
			c.JSON(http.StatusNotFound, gin.H{"error": "phone not found"})
		})
		// 手动复活号: 清除 exhausted/dead 标记 (误判恢复用)
		admin.POST("/accounts/:phone/revive", func(c *gin.Context) {
			phone := c.Param("phone")
			for _, a := range pool.Snapshot() {
				if a.Phone == phone {
					a.mu.Lock()
					a.exhausted = false
					a.exhaustedAt = time.Time{}
					a.dead = false
					a.errorCount = 0
					a.consecutiveErrors = 0
					a.cooldownUntil = time.Time{}
					a.mu.Unlock()
					c.JSON(http.StatusOK, gin.H{"phone": phone, "revived": true})
					return
				}
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "phone not found"})
		})

		admin.GET("/usage/:key", func(c *gin.Context) {
			fullKey := c.Param("key")
			u := usageTrk.GetStats(fullKey)
			c.JSON(http.StatusOK, u)
		})
		admin.GET("/usage", func(c *gin.Context) {
			c.JSON(http.StatusOK, usageTrk.AllStats())
		})
	}

	// 注册页面 API (公开, 反向代理到 Dashboard 9100)
	registerProxy := func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		dashPath := c.Request.URL.Path
		dashPath = strings.Replace(dashPath, "/api/register/", "/api/register-page/", 1)
		req, _ := http.NewRequest(c.Request.Method, "http://127.0.0.1:9100"+dashPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "dashboard unreachable"})
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		c.Data(resp.StatusCode, "application/json", respBody)
	}
	r.POST("/api/register/start", registerProxy)
	r.POST("/api/register/submit", registerProxy)
	r.POST("/api/admin/login", registerProxy)
	r.POST("/api/admin/verify", registerProxy)
	r.POST("/api/admin/logout", registerProxy)

	// 注册流程直接注入号池 (公开路由, 与 registerProxy 一致)
	r.POST("/api/register/inject", func(c *gin.Context) {
		var a Account
		if err := c.ShouldBindJSON(&a); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if a.Phone == "" || a.AccessToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "phone and access_token required"})
			return
		}
		pool.InjectAccount(&a)
		pool.PersistDirty()
		// 新号注册后立即自动签到领分 (异步, 不阻塞注入响应; 跳过 healthCheck 防风控)
		if EnableCheckin && engineRef != nil {
			acc := pool.GetAccount(a.Phone)
			if acc != nil {
				go func(acc *Account) {
					loc := time.FixedZone("CST", 8*3600)
					day := time.Now().In(loc).Format("2006-01-02")
					if !acc.NeedsCheckin(day) {
						return
					}
					bonus, err := engineRef.dailyCheckin(acc)
					if err != nil {
						log.Warn("register checkin: failed", zap.String("phone", acc.Phone), zap.Error(err))
						return
					}
					acc.MarkCheckin(day)
					if bonus > 0 {
						log.Info("register checkin: bonus", zap.String("phone", acc.Phone), zap.Int("credit", bonus))
					}
				}(acc)
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "phone": a.Phone})
	})

	srv := &http.Server{Addr: ListenAddr, Handler: r}
	go func() {
		fmt.Printf("\n+--------------------------------------------+\n")
		fmt.Printf("|  CodeBuddy Pool Proxy (Go)                  |\n")
		fmt.Printf("|  OpenAI:    POST /v1/chat/completions       |\n")
		fmt.Printf("|  Anthropic: POST /v1/messages               |\n")
		fmt.Printf("|  Models:    kimi-k3, deepseek-v4-pro, ...   |\n")
		fmt.Printf("|  Listen:    %-36s|\n", ListenAddr)
		fmt.Printf("+--------------------------------------------+\n\n")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down...")
	if n := pool.PersistDirty(); n > 0 {
		log.Info("shutdown: persisted dirty accounts", zap.Int("n", n))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Info("stopped")
}

func apiKeyAuth(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if len(auth) >= 7 && auth[:7] == "Bearer " {
		auth = auth[7:]
	}
	got := auth
	if got == "" {
		got = c.GetHeader("x-api-key")
	}
	if got == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
		return
	}

	k, ok := keyMgr.Validate(got)
	if !ok {
		usageTrk.RecordFailure(got)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}
	c.Set("api_key", k.Key)
	c.Set("key_name", k.Name)
	c.Set("key_rate_limit", k.RateLimit)
	c.Next()
}

func rateLimitMW(c *gin.Context) {
	key := c.GetString("api_key")
	rpm := c.GetInt("key_rate_limit")
	if !rateLim.Allow(key, rpm) {
		usageTrk.RecordRateLimited(key)
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": fmt.Sprintf("rate limit exceeded (%d rpm)", rpm),
		})
		return
	}
	c.Next()
}

func adminKeyAuth(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if len(auth) >= 7 && auth[:7] == "Bearer " {
		auth = auth[7:]
	}
	if auth == "" {
		auth = c.GetHeader("x-api-key")
	}
	if subtle.ConstantTimeCompare([]byte(auth), []byte(adminKey)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "admin key required"})
		return
	}
	c.Next()
}

func reloadLoop(pool *AccountPool) {
	for {
		time.Sleep(time.Duration(ReloadInterval) * time.Second)
		pool.ReloadIfNeeded()
	}
}

func persistLoop(pool *AccountPool) {
	for {
		time.Sleep(10 * time.Second)
		pool.PersistDirty()
	}
}

func keyReloadLoop() {
	for {
		time.Sleep(15 * time.Second)
		keyMgr.ReloadIfNeeded()
	}
}

// creditPollLoop 积分轮询：每隔 CreditRefreshInterval 秒查 1 个号的账单接口,
// 轮转覆盖全池 (20 号约 40min 一圈)。把积分刷新压力摊平, 既保证面板积分
// 自动更新, 又避免集中打账单接口触发风控。写回 accounts.jsonl 供面板读取。
func creditPollLoop(pool *AccountPool, engine *engineClient) {
	if SkipHealthCheck {
		log.Info("credit poll: disabled by SKIP_HEALTH_CHECK")
		return
	}
	idx := 0
	for {
		time.Sleep(time.Duration(CreditRefreshInterval) * time.Second)
		accs := pool.Snapshot()
		if len(accs) == 0 {
			continue
		}
		if idx >= len(accs) {
			idx = 0
		}
		a := accs[idx]
		idx++
		if a.dead {
			continue
		}
		if err := engine.healthCheck(a); err != nil {
			log.Warn("credit poll: failed", zap.String("phone", a.Phone), zap.Error(err))
			continue
		}
		a.mu.Lock()
		a.CreditsTotal = a.creditsLeft
		a.markDirty()
		a.mu.Unlock()
		pool.PersistDirty()
	}
}


