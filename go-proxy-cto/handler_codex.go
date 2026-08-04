package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ---------------- Codex (OpenAI Responses API) 兼容 ----------------
//
// Codex CLI 走 OpenAI Responses API (POST /v1/responses)，与旧 chat.completions 差异:
//   - 请求: instructions + input[] (type:message, role, content[] 为 input_text 块)
//   - 流式响应: 事件序列 response.created / response.output_text.delta / ... / response.completed
//     而非 choices[].delta，也没有 data: [DONE]
//
// 本 handler 把 Responses 请求转成 CodeBuddy 的 OpenAI chat.completions 格式，
// 再把上游 SSE 逐 chunk 转成 Responses 事件流转发给客户端。

// buildCodexRequest 把 Responses API 请求体转成 OpenAI chat.completions 请求体
func buildCodexRequest(body map[string]interface{}, model string) map[string]interface{} {
	oai := map[string]interface{}{
		"model":  model,
		"stream": true,
	}

	var oaiMsgs []map[string]interface{}

	// instructions → system
	if inst, ok := body["instructions"].(string); ok && inst != "" {
		oaiMsgs = append(oaiMsgs, map[string]interface{}{
			"role": "system", "content": sanitizeIdentity(inst),
		})
	}

	// input → messages
	// Responses API 允许 input 为纯字符串（等价于单条 user 消息）或 item 数组
	if s, ok := body["input"].(string); ok && s != "" {
		oaiMsgs = append(oaiMsgs, map[string]interface{}{"role": "user", "content": s})
	}
	if input, ok := body["input"].([]interface{}); ok {
		for _, item := range input {
			im, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := im["role"].(string)
			if role != "user" && role != "assistant" && role != "system" {
				continue
			}

			switch c := im["content"].(type) {
			case string:
				oaiMsgs = append(oaiMsgs, map[string]interface{}{"role": role, "content": c})
			case []interface{}:
				var textParts []string
				for _, block := range c {
					bm, _ := block.(map[string]interface{})
					bt, _ := bm["type"].(string)
					switch bt {
					case "input_text", "output_text", "text":
						if txt, _ := bm["text"].(string); txt != "" {
							textParts = append(textParts, txt)
						}
					}
				}
				if len(textParts) > 0 {
					oaiMsgs = append(oaiMsgs, map[string]interface{}{
						"role": role, "content": strings.Join(textParts, ""),
					})
				}
			}
		}
	}

	oai["messages"] = oaiMsgs

	// max_output_tokens → max_tokens
	if mt, ok := body["max_output_tokens"].(float64); ok && mt > 0 {
		oai["max_tokens"] = int(mt)
	}

	// tools: Responses 格式 {"type":"function","name":...,"parameters":...} → OpenAI {"type":"function","function":{...}}
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		var oaiTools []map[string]interface{}
		for _, t := range tools {
			tm, _ := t.(map[string]interface{})
			if tm == nil {
				continue
			}
			if fn, ok := tm["function"].(map[string]interface{}); ok {
				// 已是 OpenAI 嵌套格式，原样透传
				oaiTools = append(oaiTools, map[string]interface{}{"type": "function", "function": fn})
				continue
			}
			oaiTools = append(oaiTools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tm["name"],
					"description": tm["description"],
					"parameters":  tm["parameters"],
				},
			})
		}
		oai["tools"] = oaiTools
	}

	return oai
}

// codexTextMessage 把 Responses output 数组里的 text 消息拼成纯文本
func codexTextMessage(text string) map[string]interface{} {
	return map[string]interface{}{
		"id":     "msg_" + shortUUID(),
		"type":   "message",
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]interface{}{
			{"type": "output_text", "text": text, "annotations": []interface{}{}},
		},
	}
}

// codexToolCallItem 把聚合的 tool_call 转成 Responses function_call output item
func codexToolCallItem(tc map[string]interface{}) map[string]interface{} {
	fn, _ := tc["function"].(map[string]interface{})
	id, _ := tc["id"].(string)
	return map[string]interface{}{
		"id":        "fc_" + shortUUID(),
		"type":      "function_call",
		"status":    "completed",
		"call_id":   id,
		"name":      fn["name"],
		"arguments": fn["arguments"],
	}
}

// codexResponseObject 构造 response 对象 (created / completed 事件共用)
func codexResponseObject(respID, model, status string, output []interface{}) map[string]interface{} {
	obj := map[string]interface{}{
		"id":         respID,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     status,
		"model":      model,
		"output":     output,
		"usage":      map[string]int{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
	}
	return obj
}

func handleCodexResponses(c *gin.Context, pool *AccountPool, engine *engineClient) {
	start := time.Now()
	var rawBody map[string]interface{}
	if err := c.ShouldBindJSON(&rawBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json: " + err.Error()})
		return
	}

	modelName, _ := rawBody["model"].(string)
	model := ResolveModel(modelName)
	isStream, _ := rawBody["stream"].(bool)

	if _, ok := rawBody["input"]; !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "input is required"})
		return
	}

	log.Info("codex: request",
		zap.String("model", modelName),
		zap.String("mapped", model),
		zap.Bool("stream", isStream))

	oaiReq := buildCodexRequest(rawBody, model)

	// 切号重试: 最多切 3 个号 (ctx 生命周期外层管理, 同 handleChatCompletions)
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
			"error":    relErr,
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
		streamCodex(c, resp.Body, modelName)
	} else {
		c.JSON(http.StatusOK, aggregateCodex(resp.Body, modelName))
	}

	log.Info("codex: done",
		zap.String("model", modelName),
		zap.Bool("stream", isStream),
		zap.Duration("duration", time.Since(start)))
}

// aggregateCodex 聚合上游 SSE → 完整 Responses 对象 (非流式)
func aggregateCodex(r io.Reader, model string) map[string]interface{} {
	text, toolCalls, _, _ := collectSSEWithToolCalls(r)

	var output []interface{}
	if text != "" {
		output = append(output, codexTextMessage(text))
	}
	for _, tc := range toolCalls {
		output = append(output, codexToolCallItem(tc))
	}
	if output == nil {
		output = []interface{}{}
	}

	return codexResponseObject("resp_"+shortUUID(), model, "completed", output)
}

// streamCodex 上游 OpenAI SSE → Responses API 事件序列
func streamCodex(c *gin.Context, r io.Reader, model string) {
	setSSEHeaders(c)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()

	seq := 0
	send := func(event string, payload map[string]interface{}) {
		payload["sequence_number"] = seq
		seq++
		b, _ := json.Marshal(payload)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		c.Writer.Flush()
	}

	respID := "resp_" + shortUUID()
	itemID := "msg_" + shortUUID()
	send("response.created", map[string]interface{}{
		"type":     "response.created",
		"response": codexResponseObject(respID, model, "in_progress", []interface{}{}),
	})
	send("response.in_progress", map[string]interface{}{
		"type":     "response.in_progress",
		"response": codexResponseObject(respID, model, "in_progress", []interface{}{}),
	})

	textStarted := false
	var fullText strings.Builder
	finishReason := ""

	reader := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(trimmed, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if data != "[DONE]" {
					var chunk sseChunk
					if json.Unmarshal([]byte(data), &chunk) == nil {
						for _, ch := range chunk.Choices {
							if ch.FinishReason != "" {
								finishReason = ch.FinishReason
							}
							if ch.Delta.Content != "" {
								if !textStarted {
									textStarted = true
									send("response.output_item.added", map[string]interface{}{
										"type":         "response.output_item.added",
										"output_index": 0,
										"item": map[string]interface{}{
											"id":      itemID,
											"type":    "message",
											"status":  "in_progress",
											"role":    "assistant",
											"content": []interface{}{},
										},
									})
									send("response.content_part.added", map[string]interface{}{
										"type":         "response.content_part.added",
										"item_id":      itemID,
										"output_index": 0,
										"content_index": 0,
										"part": map[string]interface{}{
											"type":        "output_text",
											"text":        "",
											"annotations": []interface{}{},
										},
									})
								}
								fullText.WriteString(ch.Delta.Content)
								send("response.output_text.delta", map[string]interface{}{
									"type":         "response.output_text.delta",
									"item_id":      itemID,
									"output_index": 0,
									"content_index": 0,
									"delta":        ch.Delta.Content,
								})
							}
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	// 收尾事件
	if textStarted {
		finalText := fullText.String()
		send("response.output_text.done", map[string]interface{}{
			"type":         "response.output_text.done",
			"item_id":      itemID,
			"output_index": 0,
			"content_index": 0,
			"text":         finalText,
		})
		send("response.content_part.done", map[string]interface{}{
			"type":         "response.content_part.done",
			"item_id":      itemID,
			"output_index": 0,
			"content_index": 0,
			"part": map[string]interface{}{
				"type":        "output_text",
				"text":        finalText,
				"annotations": []interface{}{},
			},
		})
		send("response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item":         codexTextMessage(finalText),
		})
	}

	var output []interface{}
	if textStarted {
		output = append(output, codexTextMessage(fullText.String()))
	} else {
		output = []interface{}{}
	}

	send("response.completed", map[string]interface{}{
		"type":     "response.completed",
		"response": codexResponseObject(respID, model, "completed", output),
	})

	_ = finishReason // 目前仅文本场景, tool_calls 流式暂不支持, 预留
}
