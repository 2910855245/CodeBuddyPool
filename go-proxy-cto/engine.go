package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// engineClient CodeBuddy API HTTP 封装
// http  用于普通请求 (带整体超时)
// stream 用于 SSE 流式请求 (无整体超时, 首字节后逐行转发)
type engineClient struct {
	http   *http.Client
	stream *http.Client
}

func newEngineClient() *engineClient {
	transport := &http.Transport{
		MaxIdleConns:        ConnMaxIdle,
		MaxIdleConnsPerHost: ConnMaxPerHost,
		IdleConnTimeout:     time.Duration(ConnIdleTimeout) * time.Second,
	}
	return &engineClient{
		http:   &http.Client{Timeout: 60 * time.Second, Transport: transport},
		stream: &http.Client{Timeout: 0, Transport: transport},
	}
}

// newRequest 构造带鉴权的请求 (ctx 为 nil 时用背景上下文)
func (e *engineClient) newRequest(a *Account, method, path string, body interface{}, ctx context.Context) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		j, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(j)
	}

	a.mu.Lock()
	token := a.AccessToken
	a.mu.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, CodeBuddyAPI+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	return req, nil
}

// do 非流式 API 调用 (自动 token 刷新 + 401 重试)
func (e *engineClient) do(a *Account, method, path string, body interface{}) (int, []byte, error) {
	if err := e.refreshToken(a, false); err != nil {
		return 0, nil, fmt.Errorf("ensure token: %w", err)
	}
	status, b, err := e.doOnce(a, method, path, body)
	if err != nil {
		return 0, nil, err
	}

	// 401/403: 强制刷新 token 后重试一次（应对 token 在请求间隙过期的竞态）
	if status == 401 || status == 403 {
		if a.RefreshToken == "" {
			a.MarkDead()
			return status, b, nil
		}
		// 强制刷新（忽略 expiresAt，直接走 refresh）
		if err := e.refreshToken(a, true); err != nil {
			a.MarkDead()
			return status, b, nil
		}
		// 用新 token 重试
		status, b, err = e.doOnce(a, method, path, body)
		if err != nil {
			return 0, nil, err
		}
		// 仍然 401 → 真的死了
		if status == 401 || status == 403 {
			a.MarkDead()
		}
	}
	return status, b, nil
}

// doOnce 单次请求（不含刷新逻辑），do 和 doStream 的公共部分
func (e *engineClient) doOnce(a *Account, method, path string, body interface{}) (int, []byte, error) {
	req, err := e.newRequest(a, method, path, body, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

// doStream 流式调用: 返回未关闭的响应体, 调用方负责 Close
// ctx 控制整个流的生命周期 (超时 / 客户端断开都会中断上游)
func (e *engineClient) doStream(a *Account, path string, body interface{}, ctx context.Context) (*http.Response, error) {
	if err := e.refreshToken(a, false); err != nil {
		return nil, fmt.Errorf("ensure token: %w", err)
	}
	req, err := e.newRequest(a, "POST", path, body, ctx)
	if err != nil {
		return nil, err
	}
	return e.stream.Do(req)
}

// refreshToken 统一 token 刷新逻辑。
// force=false 时仅当 token 临近过期才刷新 (正常请求路径);
// force=true 时忽略 expiresAt 强制刷新 (401 重试路径)。
func (e *engineClient) refreshToken(a *Account, force bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 非强制模式: Token 还够用就不刷新
	if !force && a.ExpiresAt > 0 && time.Now().UnixMilli()+TokenRefreshTTL*1000 < a.ExpiresAt {
		return nil
	}

	if a.RefreshToken == "" {
		return fmt.Errorf("no refresh token")
	}

	data := fmt.Sprintf("grant_type=refresh_token&refresh_token=%s&client_id=console", a.RefreshToken)
	resp, err := e.http.Post(
		"https://www.codebuddy.cn/auth/realms/copilot/protocol/openid-connect/token",
		"application/x-www-form-urlencoded",
		bytes.NewBufferString(data),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		// refresh_token 本身被拒 (400/401/403) → 账号彻底失效
		// 注意: 此处已持有 a.mu, 直接置位, 不能再调 MarkDead (会死锁)
		if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 {
			a.dead = true
			a.markDirty()
		}
		return fmt.Errorf("refresh failed: %d %s", resp.StatusCode, truncate(string(b), 200))
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return err
	}

	a.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		a.RefreshToken = tok.RefreshToken
	}
	a.ExpiresAt = time.Now().UnixMilli() + tok.ExpiresIn*1000
	a.markDirty()
	return nil
}
func (e *engineClient) dailyCheckin(a *Account) (int, error) {
	_, body, err := e.do(a, "POST", "/v2/billing/meter/daily-checkin", map[string]interface{}{})
	if err != nil {
		return 0, err
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Credit int `json:"credit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	if resp.Code != 0 {
		return 0, nil
	}
	return resp.Data.Credit, nil
}

// healthCheck 查询账号剩余积分 (仅信息刷新)
// 不据此标记 exhausted: 账单接口在部分 IP (如服务器机房 IP) 下会被风控返回 0,
// 真实的"耗尽"只能由 chat 调用的配额错误判定 (pool.Release 路径, 12h 冷却自动恢复)
func (e *engineClient) healthCheck(a *Account) error {
	if SkipHealthCheck {
		return nil
	}
	status, body, err := e.do(a, "POST", "/v2/billing/meter/get-user-resource", map[string]interface{}{
		"PageNumber": 1, "PageSize": 1, "ProductCode": "p_tcaca",
		"Status":                     []int{0, 3},
		"PackageStartTimeRangeBegin": "2024-12-01 21:25:00",
		"PackageStartTimeRangeEnd":   time.Now().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("billing api status %d", status)
	}

	var resp struct {
		Data struct {
			Response struct {
				Data struct {
					Accounts []struct {
						CycleCapacityRemainPrecise string `json:"CycleCapacityRemainPrecise"`
					} `json:"Accounts"`
				} `json:"Data"`
			} `json:"Response"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("billing api parse: %w", err)
	}
	if len(resp.Data.Response.Data.Accounts) == 0 {
		// 响应里没有账户数据 (可能被风控/改版), 不能据此判 exhausted
		return fmt.Errorf("billing api: empty accounts")
	}

	total := 0.0
	for _, acct := range resp.Data.Response.Data.Accounts {
		var v float64
		fmt.Sscanf(acct.CycleCapacityRemainPrecise, "%f", &v)
		total += v
	}
	// healthCheck 只刷新积分显示, 永不标记 exhausted:
	// 账单接口可能因调用方 IP 被风控而返回 0 (服务器实测),
	// "耗尽"只能由真实 chat 调用的配额错误来判定 (pool.Release)
	a.SetCredits(total)
	return nil
}
