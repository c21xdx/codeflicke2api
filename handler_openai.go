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

// OpenAI 兼容请求/响应结构

// OAIChatRequest OpenAI 格式聊天请求
type OAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OAIMessage    `json:"messages"`
	Stream      *bool           `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"` // 透传 tools 定义
}

// OAIMessage OpenAI 消息
type OAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // 可以是 string 或 array
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// OAIModelList OpenAI 模型列表响应
type OAIModelList struct {
	Object string     `json:"object"`
	Data   []OAIModel `json:"data"`
}

// OAIModel OpenAI 模型
type OAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// OAIChatResponse OpenAI 非流式聊天响应
type OAIChatResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage,omitempty"`
}

// OAIChoice 选项
type OAIChoice struct {
	Index        int             `json:"index"`
	Message      *OAIRespMessage `json:"message,omitempty"`
	Delta        *OAIRespMessage `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

// OAIRespMessage 响应消息
type OAIRespMessage struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// OAIUsage 使用量
type OAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OAIStreamChunk 流式块
type OAIStreamChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
}

// OpenAIHandler OpenAI 兼容端点处理器
type OpenAIHandler struct {
	pool     *AccountPool
	upstream *UpstreamClient
}

// NewOpenAIHandler 创建处理器
func NewOpenAIHandler(pool *AccountPool, upstream *UpstreamClient) *OpenAIHandler {
	return &OpenAIHandler{pool: pool, upstream: upstream}
}

// 内置模型列表（基于 api.md 拓包确认的真实模型）
var builtinModels = []OAIModel{
	// === 聊天模型 ===
	{ID: "kwaipilot_40b", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "kat_coder", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "CLAUDE_4_5", Object: "model", Created: 1700000000, OwnedBy: "anthropic"},
	{ID: "GPT_5_2", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "DEEPSEEK_V3_2", Object: "model", Created: 1700000000, OwnedBy: "deepseek"},
	// === Agent 模型 (_TOC 后缀) ===
	{ID: "GLM_5_TOC", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "GLM_4_7_TOC", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "GPT_5_2_TOC", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "KIMI_K2_5_TOC", Object: "model", Created: 1700000000, OwnedBy: "moonshot"},
	{ID: "kat_coder_TOC", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "MINIMAX_M2_1_TOC", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
	{ID: "MINIMAX_M2_5_TOC", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
	{ID: "DEEPSEEK_V3_2_TOC", Object: "model", Created: 1700000000, OwnedBy: "deepseek"},
}

// HandleModels GET /v1/models
func (h *OpenAIHandler) HandleModels(c *gin.Context) {
	// 优先尝试从上游获取模型列表
	account, err := h.pool.GetNextAccount()
	if err == nil {
		models, err := h.upstream.GetModels(account)
		if err == nil && len(models) > 0 {
			oaiModels := make([]OAIModel, 0, len(models))
			for _, m := range models {
				oaiModels = append(oaiModels, OAIModel{
					ID:      m.ModelType,
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "codeflicker",
				})
			}
			c.JSON(http.StatusOK, OAIModelList{Object: "list", Data: oaiModels})
			return
		}
	}

	// 回退到内置模型列表
	c.JSON(http.StatusOK, OAIModelList{Object: "list", Data: builtinModels})
}

// HandleChatCompletions POST /v1/chat/completions
func (h *OpenAIHandler) HandleChatCompletions(c *gin.Context) {
	var req OAIChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("请求格式错误: %v", err),
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// 获取可用账号
	account, err := h.pool.GetNextAccount()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": "没有可用的账号",
				"type":    "server_error",
			},
		})
		return
	}

	// 转换消息格式
	cfMessages := convertMessages(req.Messages)

	// 构建 CodeFlicker 请求
	sessionID := uuid.New().String()
	chatID := uuid.New().String()
	cfReq := &CFChatRequest{
		SessionID: sessionID,
		ChatID:    chatID,
		Mode:      "agent",
		Round:     0,
		Messages:  cfMessages,
		Tools:     req.Tools, // 透传 tools 定义到上游
		Model:     req.Model,
		DeviceInfo: CFDeviceInfo{
			Platform:      "codeflicker-ide",
			IDEVersion:    "1.101.2",
			PluginVersion: "9.6.2511250",
		},
	}

	// 判断是否流式
	isStream := req.Stream != nil && *req.Stream

	// 发送上游请求
	resp, err := h.upstream.StreamChatCompletion(account, cfReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("上游请求失败: %v", err),
				"type":    "upstream_error",
			},
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		// 检查是否需要标记账号状态
		if resp.StatusCode == 403 {
			h.pool.MarkAccountStatus(account.ID, "error")
		}
		c.JSON(resp.StatusCode, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("上游返回错误: %s", string(body)),
				"type":    "upstream_error",
			},
		})
		return
	}

	if isStream {
		h.handleStreamResponse(c, resp.Body, req.Model, account)
	} else {
		h.handleNonStreamResponse(c, resp.Body, req.Model, account)
	}
}

// handleStreamResponse 处理流式响应
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
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)

		if data == "[DONE]" {
			break
		}

		var event CFSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// 处理错误事件
		if event.Type == "error" {
			// 检查是否需要标记账号状态
			if event.Reply == "15" || event.Reply == "61" {
				h.pool.MarkAccountStatus(account.ID, "rate_limited")
			} else if event.Code == 403 || event.Reply == "802" {
				h.pool.MarkAccountStatus(account.ID, "error")
			}
			// 发送错误信息
			errorChunk := OAIStreamChunk{
				ID:      respID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []OAIChoice{
					{
						Index: 0,
						Delta: &OAIRespMessage{
							Content: fmt.Sprintf("[错误] %s (code: %d)", event.Tip, event.Code),
						},
						FinishReason: strPtr("stop"),
					},
				},
			}
			chunkJSON, _ := json.Marshal(errorChunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", chunkJSON)
			flusher.Flush()
			break
		}

		// 处理 ack 事件
		if event.Type == "ack" {
			continue
		}

		// 处理 data 事件
		if event.Type == "data" {
			var chatData CFChatData
			if err := json.Unmarshal(event.Data, &chatData); err != nil {
				continue
			}

			for _, choice := range chatData.Choices {
				chunk := OAIStreamChunk{
					ID:      respID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []OAIChoice{
						{
							Index: 0,
							Delta: &OAIRespMessage{
								Role:      choice.Message.Role,
								Content:   choice.Message.Content,
								ToolCalls: choice.Message.ToolCalls,
							},
							FinishReason: choice.FinishReason,
						},
					},
				}

				chunkJSON, _ := json.Marshal(chunk)
				fmt.Fprintf(c.Writer, "data: %s\n\n", chunkJSON)
				flusher.Flush()
			}
		}
	}

	// 发送 [DONE]
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleNonStreamResponse 处理非流式响应
func (h *OpenAIHandler) handleNonStreamResponse(c *gin.Context, body io.Reader, model string, account *Account) {
	respID := "chatcmpl-" + uuid.New().String()[:8]
	created := time.Now().Unix()

	var fullContent strings.Builder
	var lastToolCalls json.RawMessage
	var usage *OAIUsage

	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)

		if data == "[DONE]" {
			break
		}

		var event CFSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "error" {
			if event.Reply == "15" || event.Reply == "61" {
				h.pool.MarkAccountStatus(account.ID, "rate_limited")
			} else if event.Code == 403 || event.Reply == "802" {
				h.pool.MarkAccountStatus(account.ID, "error")
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{
					"message": event.Tip,
					"type":    "upstream_error",
					"code":    event.Code,
				},
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

	// 如果返回了 tool_calls，finish_reason 应为 "tool_calls"
	finishReason := "stop"
	if len(lastToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	resp := OAIChatResponse{
		ID:      respID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []OAIChoice{
			{
				Index: 0,
				Message: &OAIRespMessage{
					Role:      "assistant",
					Content:   fullContent.String(),
					ToolCalls: lastToolCalls,
				},
				FinishReason: &finishReason,
			},
		},
		Usage: usage,
	}

	c.JSON(http.StatusOK, resp)
}

// convertMessages 将 OpenAI 消息格式转换为 CodeFlicker 格式
func convertMessages(messages []OAIMessage) []CFMessage {
	var cfMessages []CFMessage

	for _, msg := range messages {
		cfMsg := CFMessage{
			Role:       msg.Role,
			ToolCallID: msg.ToolCallID,
		}

		// developer → system 映射
		if cfMsg.Role == "developer" {
			cfMsg.Role = "system"
		}
		// function → tool 映射
		if cfMsg.Role == "function" {
			cfMsg.Role = "tool"
		}

		// 处理 content：如果是字符串，转换为 [{type: "text", text: "..."}]
		if len(msg.Content) > 0 {
			var contentStr string
			if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
				// 是字符串类型
				parts := []CFContentPart{{Type: "text", Text: contentStr}}
				contentJSON, _ := json.Marshal(parts)
				cfMsg.Content = contentJSON
			} else {
				// 已经是数组类型，直接透传
				cfMsg.Content = msg.Content
			}
		}

		// 透传 tool_calls
		if len(msg.ToolCalls) > 0 {
			cfMsg.ToolCalls = msg.ToolCalls
		}

		cfMessages = append(cfMessages, cfMsg)
	}

	return cfMessages
}

func strPtr(s string) *string {
	return &s
}
