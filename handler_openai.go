package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OAIChatRequest OpenAI 格式的聊天补全请求
type OAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OAIMessage    `json:"messages"`
	Stream      *bool           `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
}

// OAIMessage OpenAI 格式消息（content 支持 string 或 array）
type OAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// OAIModelList 模型列表响应
type OAIModelList struct {
	Object string     `json:"object"`
	Data   []OAIModel `json:"data"`
}

// OAIModel 单个模型信息
type OAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// OAIChatResponse 非流式聊天补全响应
type OAIChatResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage,omitempty"`
}

// OAIChoice 补全选项
type OAIChoice struct {
	Index        int             `json:"index"`
	Message      *OAIRespMessage `json:"message,omitempty"`
	Delta        *OAIRespMessage `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

// OAIRespMessage 响应消息体
type OAIRespMessage struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// OAIUsage Token 用量统计
type OAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OAIStreamChunk 流式响应数据块
type OAIStreamChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
}

// OpenAIHandler OpenAI 兼容 API 的请求处理器
type OpenAIHandler struct {
	pool     *AccountPool
	upstream *UpstreamClient
}

// NewOpenAIHandler 创建 OpenAI 兼容处理器
func NewOpenAIHandler(pool *AccountPool, upstream *UpstreamClient) *OpenAIHandler {
	return &OpenAIHandler{pool: pool, upstream: upstream}
}

// modelNameMapping 上游模型标识 → 用户友好名称
var modelNameMapping = map[string]string{
	"GLM_5_TOC":         "glm-5",
	"MINIMAX_M2_1_TOC":  "minimax-m2.5",
	"GPT_5_2_TOC":       "gpt-5.2",
	"KIMI_K2_5_TOC":     "kimi-k2.5",
	"kat_coder_TOC":     "kat-coder-pro-v1",
	"GLM_4_7_TOC":       "glm-4.7",
	"DEEPSEEK_V3_2_TOC": "deepseek-v3.2",
	"MINIMAX_M2_5_TOC":  "minimax-m2",
}

// reverseModelMapping 用户友好名称 → 上游模型标识（启动时自动生成）
var reverseModelMapping = func() map[string]string {
	m := make(map[string]string, len(modelNameMapping))
	for upstream, friendly := range modelNameMapping {
		m[friendly] = upstream
	}
	return m
}()

// mapModelName 将上游模型标识转换为用户友好名称
func mapModelName(upstreamName string) (string, bool) {
	friendly, ok := modelNameMapping[upstreamName]
	return friendly, ok
}

// resolveModelName 将用户传入的模型名解析为上游标识，支持友好名和原始名
func resolveModelName(userModel string) string {
	if upstream, ok := reverseModelMapping[userModel]; ok {
		return upstream
	}
	return userModel
}

// builtinModels 内置模型列表，作为上游不可用时的降级方案
var builtinModels = []OAIModel{
	{ID: "glm-5", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "glm-4.7", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "gpt-5.2", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "kimi-k2.5", Object: "model", Created: 1700000000, OwnedBy: "moonshot"},
	{ID: "kat-coder-pro-v1", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "minimax-m2.5", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
	{ID: "minimax-m2", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
	{ID: "deepseek-v3.2", Object: "model", Created: 1700000000, OwnedBy: "deepseek"},
}

// HandleModels 返回可用模型列表，优先从上游获取，失败时降级为内置列表
func (h *OpenAIHandler) HandleModels(c *gin.Context) {
	account, err := h.pool.GetNextAccount()
	if err == nil {
		models, err := h.upstream.GetModels(account)
		if err == nil && len(models) > 0 {
			oaiModels := make([]OAIModel, 0, len(models))
			for _, m := range models {
				if friendlyName, ok := mapModelName(m.ModelType); ok {
					oaiModels = append(oaiModels, OAIModel{
						ID: friendlyName, Object: "model",
						Created: time.Now().Unix(), OwnedBy: "codeflicker",
					})
				}
			}
			c.JSON(http.StatusOK, OAIModelList{Object: "list", Data: oaiModels})
			return
		}
	}
	c.JSON(http.StatusOK, OAIModelList{Object: "list", Data: builtinModels})
}

// HandleChatCompletions 处理聊天补全请求，转换 OpenAI→CodeFlicker 格式并代理
func (h *OpenAIHandler) HandleChatCompletions(c *gin.Context) {
	var req OAIChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": fmt.Sprintf("请求格式错误: %v", err), "type": "invalid_request_error"},
		})
		return
	}

	account, err := h.pool.GetNextAccount()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"message": "没有可用的账号", "type": "server_error"},
		})
		return
	}

	cfReq := &CFChatRequest{
		SessionID: uuid.New().String(),
		ChatID:    uuid.New().String(),
		Mode:      "agent",
		Messages:  convertMessages(req.Messages),
		Tools:     req.Tools,
		Model:     resolveModelName(req.Model),
		DeviceInfo: CFDeviceInfo{
			Platform: "codeflicker-ide", IDEVersion: "1.101.2", PluginVersion: "9.6.2511250",
		},
	}

	resp, err := h.upstream.StreamChatCompletion(account, cfReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{"message": fmt.Sprintf("上游请求失败: %v", err), "type": "upstream_error"},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 403 {
			h.pool.MarkAccountStatus(account.ID, "error")
		}
		c.JSON(resp.StatusCode, gin.H{
			"error": gin.H{"message": fmt.Sprintf("上游返回错误: %s", string(body)), "type": "upstream_error"},
		})
		return
	}

	isStream := req.Stream != nil && *req.Stream
	if isStream {
		h.handleStreamResponse(c, resp.Body, req.Model, account)
	} else {
		h.handleNonStreamResponse(c, resp.Body, req.Model, account)
	}
}

// handleStreamResponse 将上游 SSE 流转换为 OpenAI 流式格式逐块输出
func (h *OpenAIHandler) handleStreamResponse(c *gin.Context, body io.Reader, model string, account *Account) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	respID := "chatcmpl-" + uuid.New().String()[:8]
	created := time.Now().Unix()
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event CFSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "error":
			h.markAccountByError(event, account)
			chunk := OAIStreamChunk{
				ID: respID, Object: "chat.completion.chunk", Created: created, Model: model,
				Choices: []OAIChoice{{Index: 0,
					Delta:        &OAIRespMessage{Content: fmt.Sprintf("[错误] %s (code: %d)", event.Tip, event.Code)},
					FinishReason: strPtr("stop"),
				}},
			}
			chunkJSON, _ := json.Marshal(chunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", chunkJSON)
			flusher.Flush()
			goto done
		case "ack":
			continue
		case "data":
			var chatData CFChatData
			if err := json.Unmarshal(event.Data, &chatData); err != nil {
				continue
			}
			for _, choice := range chatData.Choices {
				chunk := OAIStreamChunk{
					ID: respID, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []OAIChoice{{Index: 0,
						Delta: &OAIRespMessage{
							Role: choice.Message.Role, Content: choice.Message.Content,
							ToolCalls: choice.Message.ToolCalls,
						},
						FinishReason: choice.FinishReason,
					}},
				}
				chunkJSON, _ := json.Marshal(chunk)
				fmt.Fprintf(c.Writer, "data: %s\n\n", chunkJSON)
				flusher.Flush()
			}
		}
	}
done:
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleNonStreamResponse 累积上游 SSE 流全部数据，组装为 OpenAI 非流式响应返回
func (h *OpenAIHandler) handleNonStreamResponse(c *gin.Context, body io.Reader, model string, account *Account) {
	respID := "chatcmpl-" + uuid.New().String()[:8]
	created := time.Now().Unix()
	var fullContent strings.Builder
	var lastToolCalls json.RawMessage
	var usage *OAIUsage

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event CFSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "error" {
			h.markAccountByError(event, account)
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{"message": event.Tip, "type": "upstream_error", "code": event.Code},
			})
			return
		}

		if event.Type == "data" {
			var chatData CFChatData
			if err := json.Unmarshal(event.Data, &chatData); err != nil {
				continue
			}
			for _, choice := range chatData.Choices {
				fullContent.WriteString(choice.Message.Content)
				if len(choice.Message.ToolCalls) > 0 {
					lastToolCalls = choice.Message.ToolCalls
				}
			}
			if chatData.Usage != nil {
				usage = &OAIUsage{
					PromptTokens:     chatData.Usage.PromptTokens,
					CompletionTokens: chatData.Usage.CompletionTokens,
					TotalTokens:      chatData.Usage.TotalTokens,
				}
			}
		}
	}

	finishReason := "stop"
	if len(lastToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	c.JSON(http.StatusOK, OAIChatResponse{
		ID: respID, Object: "chat.completion", Created: created, Model: model,
		Choices: []OAIChoice{{Index: 0,
			Message:      &OAIRespMessage{Role: "assistant", Content: fullContent.String(), ToolCalls: lastToolCalls},
			FinishReason: &finishReason,
		}},
		Usage: usage,
	})
}

// markAccountByError 根据上游错误码标记账号状态
func (h *OpenAIHandler) markAccountByError(event CFSSEEvent, account *Account) {
	switch {
	case event.Reply == "15" || event.Reply == "61":
		h.pool.MarkAccountStatus(account.ID, "rate_limited")
	case event.Code == 403 || event.Reply == "802":
		h.pool.MarkAccountStatus(account.ID, "error")
	}
}

// convertMessages 将 OpenAI 消息转换为 CodeFlicker 格式。
// 执行角色映射（developer→system, function→tool）和 content 格式归一化（string→array）。
func convertMessages(messages []OAIMessage) []CFMessage {
	cfMessages := make([]CFMessage, 0, len(messages))
	for _, msg := range messages {
		cfMsg := CFMessage{Role: msg.Role, ToolCallID: msg.ToolCallID}

		if cfMsg.Role == "developer" {
			cfMsg.Role = "system"
		}
		if cfMsg.Role == "function" {
			cfMsg.Role = "tool"
		}

		if len(msg.Content) > 0 {
			var contentStr string
			if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
				parts := []CFContentPart{{Type: "text", Text: contentStr}}
				contentJSON, _ := json.Marshal(parts)
				cfMsg.Content = contentJSON
			} else {
				cfMsg.Content = msg.Content
			}
		}

		if len(msg.ToolCalls) > 0 {
			cfMsg.ToolCalls = msg.ToolCalls
		}
		cfMessages = append(cfMessages, cfMsg)
	}
	return cfMessages
}

func strPtr(s string) *string { return &s }
