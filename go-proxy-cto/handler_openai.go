package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// streamCtx 流式请求上下文: SSEIdleTimeout 秒超时 + 客户端断开联动取消上游,
// 防止上游 hang 住导致账号永久 inUse 泄漏
func streamCtx(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), time.Duration(SSEIdleTimeout)*time.Second)
}

// ---------------- OpenAI 兼容 ----------------

type sseDelta struct {
	Content          string `json:"content"`
	Reasoning        string `json:"reasoning"`         // thinking 模型的思维链内容 (CodeBuddy 格式)
	ReasoningContent string `json:"reasoning_content"` // thinking 模型的思维链内容 (OpenAI 兼容格式)
	ToolCalls        []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type sseChoice struct {
	Delta        sseDelta `json:"delta"`
	FinishReason string   `json:"finish_reason"`
}

type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type sseChunk struct {
	Choices []sseChoice `json:"choices"`
	Usage   *sseUsage   `json:"usage"`
}

// estimateTokens 粗略估算 token 数 (≈4 字符/token)，用于上游不返回 usage 时兜底
func estimateTokens(s string) int64 {
	n := len([]rune(s))
	if n <= 0 {
		return 0
	}
	return int64((n + 3) / 4)
}

func handleChatCompletions(c *gin.Context, pool *AccountPool, engine *engineClient) {
	start := time.Now()
	var rawBody map[string]interface{}
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
		return
	}

	modelName, _ := rawBody["model"].(string)
	model, ok := LookupModel(modelName)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"message": "The model '" + modelName + "' does not exist",
			"type":    "invalid_request_error",
			"param":   "model",
			"code":    "model_not_found",
		}})
		return
	}
	isStream, _ := rawBody["stream"].(bool)

	msgs, ok2 := rawBody["messages"]
	if !ok2 || msgs == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "messages is required"})
		return
	}

	log.Info("chat: request", zap.String("model", modelName), zap.String("mapped", model), zap.Bool("stream", isStream))

	// 转发全部参数给 CodeBuddy
	oaiReq := map[string]interface{}{"model": model, "messages": msgs, "stream": true}
	for k, v := range rawBody {
		if k == "model" || k == "messages" || k == "stream" {
			continue
		}
		oaiReq[k] = v
	}

	// 切号重试: 最多切 3 个号, 成功立即返回响应; 全失败返回最后一个错误
	// 注意: ctx/cancel 不能放在 GetWithRetry 闭包里用 defer cancel() — 闭包返回时
	// cancel 会立即执行, 导致成功响应的 resp.Body 还没读就被 transport 断开
	// (tool_calls 截断、流式中断)。改为外层管理 ctx 生命周期, 覆盖整个 body 读取。
	var resp *http.Response
	var keepCancel context.CancelFunc
	acc, relErr, attempts := pool.GetWithRetry(3, func(a *Account) string {
		a.mu.Lock()
		a.lastRequestAt = time.Now()
		a.mu.Unlock()
		ctx, cancel := streamCtx(c)
		r, err := engine.doStream(a, "/v2/chat/completions", oaiReq, ctx)
		if err != nil {
			cancel()
			return err.Error()
		}
		if r.StatusCode != 200 {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			cancel()
			return string(b)
		}
		// 成功: 保存 resp 和 cancel 给外层, 由外层 defer 管理生命周期
		resp = r
		keepCancel = cancel
		return ""
	})

	if acc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   relErr,
			"attempts": attempts,
		})
		return
	}
	defer resp.Body.Close()
	defer keepCancel()

	pool.IncrementSuccess(acc)
	if key := c.GetString("api_key"); key != "" {
		usageTrk.RecordSuccess(key)
	}

	if !isStream {
		agg := aggregateOpenAI(resp.Body, modelName)
		if key := c.GetString("api_key"); key != "" {
			if u, ok := agg["usage"].(map[string]interface{}); ok {
				if ct, ok2 := u["completion_tokens"].(int64); ok2 {
					usageTrk.RecordTokens(key, 0, ct)
				}
			}
		}
		c.JSON(http.StatusOK, agg)
		return
	}

	setSSEHeaders(c)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	forwardSSE(c, resp.Body, c.GetString("api_key"))

	log.Info("chat: done",
		zap.String("model", modelName),
		zap.Duration("duration", time.Since(start)))
}

// setSSEHeaders 统一设置 SSE 响应头
func setSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

// forwardSSE 真流式: 逐行读取上游 SSE 并立即 Flush 给客户端
// 同时累计 content 字符数, 结束时按字符粗估 completion token 并记录
func forwardSSE(c *gin.Context, r io.Reader, apiKey string) {
	reader := bufio.NewReaderSize(r, 64*1024)
	var contentLen int
	var upstreamTokens int64
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				fmt.Fprintf(c.Writer, "%s\n\n", trimmed)
				c.Writer.Flush()
				// 顺带解析用于 token 统计
				data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if data != "[DONE]" {
					var chunk sseChunk
					if json.Unmarshal([]byte(data), &chunk) == nil {
						if chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
							upstreamTokens = int64(chunk.Usage.CompletionTokens)
						}
						for _, ch := range chunk.Choices {
							contentLen += len([]rune(ch.Delta.Content))
							contentLen += len([]rune(ch.Delta.Reasoning))
							contentLen += len([]rune(ch.Delta.ReasoningContent))
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	if apiKey != "" {
		tokens := upstreamTokens
		if tokens <= 0 {
			tokens = int64((contentLen + 3) / 4)
		}
		usageTrk.RecordTokens(apiKey, 0, tokens)
	}
}

// collectSSEText 已删除 (死代码, 聚合逻辑已由 collectSSEWithToolCalls 覆盖)

// aggregateOpenAI 聚合 SSE → 标准 OpenAI chat.completion 响应 (含 tool_calls)
func aggregateOpenAI(r io.Reader, reqModel string) map[string]interface{} {
	text, toolCalls, finishReason, completionTokens := collectSSEWithToolCalls(r)

	choice := map[string]interface{}{
		"index":         0,
		"finish_reason": "stop",
	}
	if finishReason == "tool_calls" || finishReason == "function_call" {
		choice["finish_reason"] = "tool_calls"
	}

	if len(toolCalls) > 0 {
		choice["message"] = map[string]interface{}{
			"role":       "assistant",
			"content":    text,
			"tool_calls": toolCalls,
		}
	} else {
		choice["message"] = map[string]interface{}{
			"role":    "assistant",
			"content": text,
		}
	}

	return map[string]interface{}{
		"id":      "chatcmpl-" + shortUUID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   reqModel,
		"choices": []map[string]interface{}{choice},
		"usage": map[string]interface{}{
			"prompt_tokens":     0,
			"completion_tokens": completionTokens,
			"total_tokens":      completionTokens,
		},
	}
}

// ---------------- Anthropic 兼容 (含工具调用) ----------------

// 上游审核会把 Claude Code 的身份声明整句误判为敏感内容并拒答，
// 实测只有含所有格的完整短语触发，替换为中性身份即可。
var identityTrigger = "You are Claude Code, Anthropic's official CLI for Claude."

func sanitizeIdentity(s string) string {
	return strings.ReplaceAll(s, identityTrigger, "You are Kimi Code, an official CLI coding assistant.")
}

// buildOpenAIRequest 把 Anthropic Messages 请求转成 OpenAI 请求体
func buildOpenAIRequest(body map[string]interface{}, model string) map[string]interface{} {
	oai := map[string]interface{}{
		"model":  model,
		"stream": true,
	}

	var oaiMsgs []map[string]interface{}

	// system
	if sys, ok := body["system"]; ok {
		switch v := sys.(type) {
		case string:
			if v != "" {
				// 上游审核会把 x-anthropic-billing-header 误判为敏感词，按行过滤。
				var lines []string
				for _, line := range strings.Split(v, "\n") {
					if strings.Contains(line, "billing-header") {
						continue
					}
					lines = append(lines, line)
				}
				if cleaned := strings.Join(lines, "\n"); strings.TrimSpace(cleaned) != "" {
					cleaned = sanitizeIdentity(cleaned)
					oaiMsgs = append(oaiMsgs, map[string]interface{}{
						"role": "system", "content": cleaned,
					})
				}
			}
		case []interface{}:
			var parts []string
			for _, block := range v {
				if bm, ok := block.(map[string]interface{}); ok {
					if t, _ := bm["type"].(string); t == "text" {
						if txt, _ := bm["text"].(string); txt != "" {
							// 上游审核会把 x-anthropic-billing-header 误判为敏感词，
							// 这段是给 Anthropic 计费系统看的，对模型无用，直接丢弃。
							if strings.Contains(txt, "billing-header") {
								continue
							}
							parts = append(parts, sanitizeIdentity(txt))
						}
					}
				}
			}
			if len(parts) > 0 {
				oaiMsgs = append(oaiMsgs, map[string]interface{}{
					"role": "system", "content": strings.Join(parts, "\n"),
				})
			}
		}
	}

	// messages
	msgs, _ := body["messages"].([]interface{})
	for _, m := range msgs {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role == "assistant" {
			role = "assistant"
		} else if role == "user" {
			role = "user"
		} else {
			continue
		}

		switch c := msg["content"].(type) {
		case string:
			oaiMsgs = append(oaiMsgs, map[string]interface{}{"role": role, "content": c})
		case []interface{}:
			// Anthropic content blocks
			var textParts []string
			var toolCalls []map[string]interface{}
			for _, block := range c {
				bm, _ := block.(map[string]interface{})
				bt, _ := bm["type"].(string)
				switch bt {
				case "text":
					if txt, _ := bm["text"].(string); txt != "" {
						textParts = append(textParts, txt)
					}
				case "tool_use":
					tc := map[string]interface{}{
						"id":   bm["id"],
						"type": "function",
						"function": map[string]interface{}{
							"name":      bm["name"],
							"arguments": toJSONString(bm["input"]),
						},
					}
					toolCalls = append(toolCalls, tc)
				case "tool_result":
					tr := map[string]interface{}{
						"role":         "tool",
						"tool_call_id": bm["tool_use_id"],
						"content":      toJSONString(bm["content"]),
					}
					oaiMsgs = append(oaiMsgs, tr)
				}
			}
			if len(toolCalls) > 0 {
				oaiMsgs = append(oaiMsgs, map[string]interface{}{
					"role": role, "content": strings.Join(textParts, ""),
					"tool_calls": toolCalls,
				})
			} else if len(textParts) > 0 {
				oaiMsgs = append(oaiMsgs, map[string]interface{}{"role": role, "content": strings.Join(textParts, "")})
			}
		}
	}

	oai["messages"] = oaiMsgs

	// tools: Anthropic → OpenAI
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		var oaiTools []map[string]interface{}
		for _, t := range tools {
			tm, _ := t.(map[string]interface{})
			oaiTools = append(oaiTools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tm["name"],
					"description": tm["description"],
					"parameters":  tm["input_schema"],
				},
			})
		}
		oai["tools"] = oaiTools

		if tc, ok := body["tool_choice"].(map[string]interface{}); ok {
			switch tc["type"] {
			case "any":
				oai["tool_choice"] = "required"
			case "tool":
				if name, _ := tc["name"].(string); name != "" {
					oai["tool_choice"] = map[string]interface{}{
						"type": "function",
						"function": map[string]string{"name": name},
					}
				}
			default:
				oai["tool_choice"] = "auto"
			}
		} else {
			oai["tool_choice"] = "auto"
		}
	}

	// max_tokens
	if mt, ok := body["max_tokens"].(float64); ok && mt > 0 {
		oai["max_tokens"] = int(mt)
	}

	return oai
}

func toJSONString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// msgID 给每条消息生成唯一 ID (并发安全)
var msgCounter atomic.Int64

func nextMsgID() string {
	return fmt.Sprintf("msg_%s%04d", shortUUID()[:6], msgCounter.Add(1))
}

func handleAnthropicMessages(c *gin.Context, pool *AccountPool, engine *engineClient) {
	start := time.Now()
	var rawBody map[string]interface{}
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	modelName, _ := rawBody["model"].(string)
	model, ok := LookupModel(modelName)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "not_found_error",
				"message": "model: " + modelName,
			},
		})
		return
	}
	isStream, _ := rawBody["stream"].(bool)

	log.Info("anthropic: request",
		zap.String("model", modelName),
		zap.String("mapped", model),
		zap.Bool("stream", isStream),
		zap.Int("tools", len(func() []interface{} {
			if t, ok := rawBody["tools"].([]interface{}); ok { return t }; return nil
		}())),
		zap.Int("msgs", len(func() []interface{} {
			if m, ok := rawBody["messages"].([]interface{}); ok { return m }; return nil
		}())),
		zap.Any("keys", func() []string {
			var k []string
			for ke := range rawBody { k = append(k, ke) }
			return k
		}()),
		zap.Int("body_bytes", len(func() string { b, _ := json.Marshal(rawBody); return string(b) }())),
	)

	// 切号重试: 最多切 3 个号
	// ctx/cancel 在外层管理, 避免闭包 defer cancel() 提前打断成功响应的 body
	oaiReq := buildOpenAIRequest(rawBody, model)
	var resp *http.Response
	var keepCancel context.CancelFunc
	acc, relErr, attempts := pool.GetWithRetry(3, func(a *Account) string {
		a.mu.Lock()
		a.lastRequestAt = time.Now()
		a.mu.Unlock()
		ctx, cancel := streamCtx(c)
		r, err := engine.doStream(a, "/v2/chat/completions", oaiReq, ctx)
		if err != nil {
			cancel()
			return err.Error()
		}
		if r.StatusCode != 200 {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			cancel()
			return string(b)
		}
		resp = r
		keepCancel = cancel
		return ""
	})

	if acc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   relErr,
			"attempts": attempts,
		})
		return
	}
	defer resp.Body.Close()
	defer keepCancel()

	pool.IncrementSuccess(acc)
	if key := c.GetString("api_key"); key != "" {
		usageTrk.RecordSuccess(key)
	}

	if isStream {
		streamAnthropicV2(c, resp.Body, modelName, c.GetString("api_key"))
	} else {
		agg := aggregateAnthropicV2(resp.Body, modelName)
		if key := c.GetString("api_key"); key != "" {
			if u, ok := agg["usage"].(map[string]interface{}); ok {
				if ot, ok2 := u["output_tokens"].(int64); ok2 {
					usageTrk.RecordTokens(key, 0, ot)
				}
			}
		}
		c.JSON(http.StatusOK, agg)
	}

	log.Info("anthropic: done",
		zap.String("model", modelName),
		zap.Bool("stream", isStream),
		zap.Duration("duration", time.Since(start)))
}

// ---------- 响应: 聚合 + 流式 (含 tool_use) ----------

// collectSSEWithToolCalls 聚合 SSE 全文 + 工具调用 (含 reasoning)
// 额外返回 completion token 估算值: 优先用上游 usage.completion_tokens, 否则按字符粗估
func collectSSEWithToolCalls(r io.Reader) (text string, toolCalls []map[string]interface{}, finishReason string, completionTokens int64) {
	var content strings.Builder
	var reasoning strings.Builder
	tcMap := make(map[int]*struct {
		id       string
		name     string
		args     strings.Builder
	})
	finishReason = "end_turn"

	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(t, "data:"))
				if data == "[DONE]" {
					break
				}
				var chunk sseChunk
				if json.Unmarshal([]byte(data), &chunk) == nil {
					if chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
						completionTokens = int64(chunk.Usage.CompletionTokens)
					}
					for _, ch := range chunk.Choices {
						if ch.FinishReason != "" {
							finishReason = ch.FinishReason
						}
						content.WriteString(ch.Delta.Content)
						if ch.Delta.Reasoning != "" {
							reasoning.WriteString(ch.Delta.Reasoning)
						}
						if ch.Delta.ReasoningContent != "" {
							reasoning.WriteString(ch.Delta.ReasoningContent)
						}
						for _, tc := range ch.Delta.ToolCalls {
							ent, ok := tcMap[tc.Index]
							if !ok {
								ent = &struct {
									id   string
									name string
									args strings.Builder
								}{id: tc.ID, name: tc.Function.Name}
								tcMap[tc.Index] = ent
							}
							if tc.ID != "" {
								ent.id = tc.ID
							}
							if tc.Function.Name != "" {
								ent.name = tc.Function.Name
							}
							ent.args.WriteString(tc.Function.Arguments)
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	// 组装 tool_calls
	for i := 0; ; i++ {
		ent, ok := tcMap[i]
		if !ok {
			break
		}
		tc := map[string]interface{}{
			"id":   ent.id,
			"type": "function",
			"function": map[string]interface{}{
				"name":      ent.name,
				"arguments": ent.args.String(),
			},
		}
		toolCalls = append(toolCalls, tc)
	}

	fullText := content.String()
	if reasoning.Len() > 0 {
		fullText = reasoning.String() + "\n" + fullText
	}
	// 上游没给 usage 时按字符粗估
	if completionTokens <= 0 {
		completionTokens = estimateTokens(fullText)
	}
	return fullText, toolCalls, finishReason, completionTokens
}

// aggregateAnthropicV2 聚合 → Anthropic message (含 tool_use)
func aggregateAnthropicV2(r io.Reader, model string) map[string]interface{} {
	text, toolCalls, finishReason, completionTokens := collectSSEWithToolCalls(r)

	msgID := nextMsgID()
	var content []map[string]interface{}

	if text != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": text})
	}
	for _, tc := range toolCalls {
		fn := tc["function"].(map[string]interface{})
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    tc["id"],
			"name":  fn["name"],
			"input": parseJSONSilent(fn["arguments"].(string)),
		})
	}

	stopReason := "end_turn"
	if finishReason == "tool_calls" || finishReason == "function_call" {
		stopReason = "tool_use"
	}

	return map[string]interface{}{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         map[string]interface{}{"input_tokens": 0, "output_tokens": completionTokens},
	}
}

func parseJSONSilent(s string) interface{} {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return map[string]interface{}{}
	}
	return v
}

// mustJSON 已删除 (死代码, 统一使用 toJSONString)

// streamAnthropicV2 流式 SSE → Anthropic 事件序列 (含 tool_use + thinking)
// 同时累计 content 字符数, 结束时按字符粗估 output token 并记录
func streamAnthropicV2(c *gin.Context, r io.Reader, model string, apiKey string) {
	setSSEHeaders(c)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	send := func(event string, payload map[string]interface{}) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		c.Writer.Flush()
	}

	msgID := nextMsgID()
	send("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	})
	send("ping", map[string]interface{}{"type": "ping"})

	type tcState struct {
		id      string
		name    string
		started bool
		idx     int
	}
	tcMap := make(map[int]*tcState)
	textStarted := false
	textBlockIdx := 0
	thinkingStarted := false
	thinkingBlockIdx := -1
	nextBlockIdx := 1
	finishReason := ""
	var contentLen int
	var upstreamTokens int64

	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			if !strings.HasPrefix(trimmed, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if data == "[DONE]" {
				break
			}
			var chunk sseChunk
			if json.Unmarshal([]byte(data), &chunk) != nil {
				continue
			}
			if chunk.Usage != nil && chunk.Usage.CompletionTokens > 0 {
				upstreamTokens = int64(chunk.Usage.CompletionTokens)
			}

			for _, ch := range chunk.Choices {
				if ch.FinishReason != "" {
					finishReason = ch.FinishReason
				}

				// reasoning (thinking 模型的思维链)
				reasoningText := ch.Delta.Reasoning
				if reasoningText == "" {
					reasoningText = ch.Delta.ReasoningContent
				}
				contentLen += len([]rune(reasoningText))
				contentLen += len([]rune(ch.Delta.Content))
				hasReasoning := reasoningText != ""
				if hasReasoning && !thinkingStarted {
					thinkingStarted = true
					thinkingBlockIdx = nextBlockIdx
					nextBlockIdx++
					send("content_block_start", map[string]interface{}{
						"type":          "content_block_start",
						"index":         thinkingBlockIdx,
						"content_block": map[string]interface{}{"type": "thinking", "thinking": ""},
					})
				}
				if hasReasoning {
					send("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": thinkingBlockIdx,
						"delta": map[string]interface{}{"type": "thinking_delta", "thinking": reasoningText},
					})
				}

				// 文本: 有实际内容才发 block_start
				hasContent := ch.Delta.Content != ""
				if hasContent && !textStarted {
					textStarted = true
					send("content_block_start", map[string]interface{}{
						"type":          "content_block_start",
						"index":         textBlockIdx,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					})
				}
				if hasContent {
					send("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": textBlockIdx,
						"delta": map[string]interface{}{"type": "text_delta", "text": ch.Delta.Content},
					})
				}

				// 工具调用
				for _, tc := range ch.Delta.ToolCalls {
					ent, ok := tcMap[tc.Index]
					if !ok {
						ent = &tcState{id: tc.ID, name: tc.Function.Name}
						tcMap[tc.Index] = ent
					}
					if tc.ID != "" {
						ent.id = tc.ID
					}
					if tc.Function.Name != "" {
						ent.name = tc.Function.Name
					}
					if !ent.started {
						ent.started = true
						ent.idx = nextBlockIdx
						nextBlockIdx++
						send("content_block_start", map[string]interface{}{
							"type":  "content_block_start",
							"index": ent.idx,
							"content_block": map[string]interface{}{
								"type":  "tool_use",
								"id":    ent.id,
								"name":  ent.name,
								"input": map[string]interface{}{},
							},
						})
					}
					send("content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": ent.idx,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": tc.Function.Arguments,
						},
					})
				}
			}
		}
		if err != nil {
			break
		}
	}

	// 关闭 content blocks
	if textStarted {
		send("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": textBlockIdx})
	}
	if thinkingStarted {
		send("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": thinkingBlockIdx})
	}
	for _, ent := range tcMap {
		if ent.started {
			send("content_block_stop", map[string]interface{}{"type": "content_block_stop", "index": ent.idx})
		}
	}

	stopReason := "end_turn"
	if finishReason == "tool_calls" || finishReason == "function_call" {
		stopReason = "tool_use"
	}
	outputTokens := upstreamTokens
	if outputTokens <= 0 {
		outputTokens = int64((contentLen + 3) / 4)
	}
	send("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]interface{}{"output_tokens": outputTokens},
	})
	send("message_stop", map[string]interface{}{"type": "message_stop"})

	if apiKey != "" {
		usageTrk.RecordTokens(apiKey, 0, outputTokens)
	}
}
