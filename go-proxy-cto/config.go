package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	CodeBuddyAPI     = "https://copilot.tencent.com"
	MaxRetries       = 3
	ReloadInterval   = 30   // accounts.jsonl 热加载间隔(秒)
	TokenRefreshTTL  = 3600 // Token 剩余多少秒以内去刷新
	LowPoolWaterMark = 3    // 可用账号低于该值视为号池告急

	// ---- 稳定性配置 ----
	HealthCheckInterval = 300  // dead/exhausted 账号探活间隔(秒)
	HealthCheckTimeout  = 30   // 单次探活 HTTP 超时(秒)
	MaxConcurrency      = 50   // 全局最大并发请求数
	SSEFirstByteTimeout = 120  // SSE 首字节超时(秒)
	SSEIdleTimeout      = 600  // SSE 超时(秒), 给 thinking 模型足够时间
	RetryBaseDelay      = 1    // 指数退避初始延迟(秒)
	RetryMaxDelay       = 30   // 指数退避最大延迟(秒)
	ConnMaxIdle         = 100  // 连接池最大空闲连接数
	ConnIdleTimeout     = 90   // 连接空闲超时(秒)
	ConnMaxPerHost      = 20   // 每个 host 最大连接数

	// ---- 防风控配置 ----
	CreditRefreshInterval = 45 // 积分刷新轮询间隔(秒), 每次只查1个号, 20号轮一圈~15min
	CheckinDelay          = 30 // 凌晨批量签到时号之间的延迟(秒), 20个号拉长到~10分钟, 避免集中打签到接口触发风控
	CooldownSeconds       = 20 // 限流冷却时长(秒): 429后短暂冷却, 到期自动恢复
	ManualOpCooldown      = 15 // 手动签到/刷新同一账号的最小间隔(秒), 防连点打风控

	// ---- 每日签到调度 ----
	// 每天 CheckinHour:CheckinMinute (UTC+8) 批量签到一次,
	// 与正常用户作息一致, 降低账单接口被风控概率。错过当天的号(如重启/新注入)仍会补签。
	CheckinHour   = 0  // 签到小时 (UTC+8)
	CheckinMinute = 40 // 签到分钟 (UTC+8)

	// ---- 单号保护 ----
	MaxConsecutiveErrors  = 5 // 连续失败阈值: 超过则进冷却, 防止坏号反复被选
	PerAccountMinInterval = 1 // 单号最小请求间隔(秒), 避免并发把单个号打爆触发风控
)

var (
	ListenAddr      = envOr("LISTEN_ADDR", "127.0.0.1:9091")
	APIKey          = envRequired("PROXY_API_KEY") // 必须设置，无默认值
	EnableCheckin   = envOr("ENABLE_CHECKIN", "true") == "true"
	SkipHealthCheck = envOr("SKIP_HEALTH_CHECK", "false") == "true"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envRequired(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "[FATAL] 必须设置环境变量 %s，拒绝启动\n", key)
		os.Exit(1)
	}
	return v
}

// CodeBuddy 模型 -> API model 名（基于 /v2/enterprises/personal/models 动态列表）
var ModelMap = map[string]string{
	"kimi-k3":          "kimi-k3-1",
	"kimi-k3-1":        "kimi-k3-1",
	"kimi-k2.7":        "kimi-k2.7",
	"kimi-k2.6":        "kimi-k2.6",
	"kimi-k2-thinking": "kimi-k2.6", // thinking 模式走 k2.6
	"deepseek-v4-pro":  "deepseek-v4-pro",
	"deepseek-v4-flash":"deepseek-v4-flash",
	"deepseek-v3":      "deepseek-v3-2-volc",
	"hunyuan-2.0":      "hunyuan-2.0-instruct",
	"glm-5.2":          "glm-5.2",
	"glm-5.1":          "glm-5.1",
	"glm-5.0":          "glm-5.1",
	"glm-5v-turbo":     "glm-5v-turbo",
	"minimax-m3":       "minimax-m3",
	"minimax-m2.7":     "minimax-m3",
	"hy3":              "hy3",
}

// LookupModel 解析模型名, 返回 (映射后的上游模型, 是否有效)。
// 无效模型名返回 ("", false), 由 handler 返回标准的 model_not_found 错误。
func LookupModel(name string) (string, bool) {
	if m, ok := ModelMap[name]; ok {
		return m, true
	}
	if m, ok := ModelMap[strings.ToLower(name)]; ok {
		return m, true
	}
	return "", false
}

func ResolveModel(name string) string {
	if m, ok := LookupModel(name); ok {
		return m
	}
	return "kimi-k2.6" // fallback
}

// QuotaErrorKeywords: 匹配到这些关键词的 chat 错误响应才判定为"积分耗尽"。
// 注意不能用太宽泛的词（如单独的 "limit"），否则 rate-limit / frequency-limit 等
// 限流错误会被误判为积分耗尽，导致号被错误标记 exhausted 冷却 12h。
var QuotaErrorKeywords = []string{
	"14018",               // CodeBuddy 额度已用尽
	"11217",               // CodeBuddy 积分不足错误码
	"余额不足",             // 中文积分不足
	"insufficient credit", // 英文积分不足
	"quota exceeded",      // 配额用完
	"credit limit",       // 积分上限
	"no credit",          // 无积分
}
