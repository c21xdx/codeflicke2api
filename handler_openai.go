package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OpenAI 兼容 API 请求/响应结构体

type OAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OAIMessage    `json:"messages"`
	Stream      *bool           `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
}

type OAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type OAIModelList struct {
	Object string     `json:"object"`
	Data   []OAIModel `json:"data"`
}

type OAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type OAIChatResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   *OAIUsage   `json:"usage,omitempty"`
}

type OAIChoice struct {
	Index        int             `json:"index"`
	Message      *OAIRespMessage `json:"message,omitempty"`
	Delta        *OAIRespMessage `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

type OAIRespMessage struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

type OAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OAIStreamChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
}

type OpenAIHandler struct {
	pool     *AccountPool
	upstream *UpstreamClient
	cfg      *AppConfig
	db       *gorm.DB
}
func NewOpenAIHandler(pool *AccountPool, upstream *UpstreamClient, cfg *AppConfig, db *gorm.DB) *OpenAIHandler {
	return &OpenAIHandler{pool: pool, upstream: upstream, cfg: cfg, db: db}
}

// 上游模型标识 ↔ 用户友好名称的双向映射
var modelNameMapping = map[string]string{
	"GLM_5_TOC":              "glm-5",
	"GLM_4_7_TOC":            "glm-4.7",
	"GPT_5_2_TOC":            "gpt-5.2",
	"GPT_5_3_CODEX_TOC":      "gpt-5.3-codex",
	"GPT_5_4_TOC":            "gpt-5.4",
	"GEMINI_PRO_3_1_TOC":     "gemini-3.1-pro",
	"KIMI_K2_5_TOC":          "kimi-k2.5",
	"DEEPSEEK_V3_2_TOC":      "deepseek-v3.2",
	"kat_coder_TOC":          "kat-coder-pro-v1",
	"kat_coder_pro_v2_TOC":   "kat-coder-pro-v2",
	"MINIMAX_M2_5_TOC":       "minimax-m2.5",
	"MINIMAX_M2_7_TOC":       "minimax-m2.7",
}

var reverseModelMapping = func() map[string]string {
	m := make(map[string]string, len(modelNameMapping))
	for upstream, friendly := range modelNameMapping {
		m[friendly] = upstream
	}
	return m
}()

func mapModelName(upstreamName string) (string, bool) {
	friendly, ok := modelNameMapping[upstreamName]
	return friendly, ok
}

func resolveModelName(userModel string) string {
	if upstream, ok := reverseModelMapping[userModel]; ok {
		return upstream
	}
	return userModel
}

// builtinModels 上游不可用时的降级模型列表
var builtinModels = []OAIModel{
	{ID: "glm-5", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "glm-4.7", Object: "model", Created: 1700000000, OwnedBy: "zhipu"},
	{ID: "gpt-5.2", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gpt-5.4", Object: "model", Created: 1700000000, OwnedBy: "openai"},
	{ID: "gemini-3.1-pro", Object: "model", Created: 1700000000, OwnedBy: "google"},
	{ID: "kimi-k2.5", Object: "model", Created: 1700000000, OwnedBy: "moonshot"},
	{ID: "deepseek-v3.2", Object: "model", Created: 1700000000, OwnedBy: "deepseek"},
	{ID: "kat-coder-pro-v1", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "kat-coder-pro-v2", Object: "model", Created: 1700000000, OwnedBy: "codeflicker"},
	{ID: "minimax-m2.5", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
	{ID: "minimax-m2.7", Object: "model", Created: 1700000000, OwnedBy: "minimax"},
}

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

func (h *OpenAIHandler) HandleChatCompletions(c *gin.Context) {
	var req OAIChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"message": fmt.Sprintf("请求格式错误: %v", err), "type": "invalid_request_error"},
		})
		return
	}

	maxRetries := h.cfg.ChatRetries
	if maxRetries < 1 {
		maxRetries = 1
	}

	cfMessages := convertMessages(req.Messages)
	upstreamModel := resolveModelName(req.Model)

	var lastErr error
	var lastStatusCode int
	var lastBody string

	for attempt := 1; attempt <= maxRetries; attempt++ {
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
			Messages:  cfMessages,
			Tools:     req.Tools,
			Model:     upstreamModel,
			DeviceInfo: CFDeviceInfo{
				Platform: "codeflicker-ide", IDEVersion: "1.101.2", PluginVersion: "9.6.2601080",
			},
		}

		resp, err := h.upstream.StreamChatCompletion(account, cfReq)
		if err != nil {
			lastErr = err
			log.Printf("上游请求失败 (第 %d/%d 次): %v", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.JSON(http.StatusBadGateway, gin.H{
				"error": gin.H{"message": fmt.Sprintf("上游请求失败（已重试 %d 次）: %v", maxRetries, lastErr), "type": "upstream_error"},
			})
			return
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatusCode = resp.StatusCode
			lastBody = string(bodyBytes)

			if resp.StatusCode == 403 {
				h.pool.MarkAccountStatus(account.ID, "error")
			}

			if resp.StatusCode == 413 {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": gin.H{"message": "超过最大 token 限制", "type": "invalid_request_error"},
				})
				return
			}

			log.Printf("上游返回 HTTP %d (第 %d/%d 次): %s", resp.StatusCode, attempt, maxRetries, lastBody)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.JSON(lastStatusCode, gin.H{
				"error": gin.H{"message": fmt.Sprintf("上游返回错误（已重试 %d 次）: %s", maxRetries, lastBody), "type": "upstream_error"},
			})
			return
		}


		if attempt > 1 {
			log.Printf("上游请求在第 %d 次重试后成功", attempt)
		}

		isStream := req.Stream != nil && *req.Stream
		if isStream {
			h.handleStreamResponse(c, resp.Body, req.Model, account)
		} else {
			h.handleNonStreamResponse(c, resp.Body, req.Model, account)
		}
		resp.Body.Close()
		return
	}
}

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

	var streamInputTokens, streamOutputTokens int

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
			if chatData.Usage != nil {
				streamInputTokens = chatData.Usage.PromptTokens
				streamOutputTokens = chatData.Usage.CompletionTokens
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
	h.recordUsage(model, streamInputTokens, streamOutputTokens)
}

func (h *OpenAIHandler) recordUsage(model string, inputTokens, outputTokens int) {
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	h.db.Create(&UsageLog{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        model,
		APIType:      "openai",
	})
}

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
	if usage != nil {
		h.recordUsage(model, usage.PromptTokens, usage.CompletionTokens)
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

// markAccountByError 根据上游错误码标记账号状态（reply=15/61 → 限流，403/802 → 封禁）
func (h *OpenAIHandler) markAccountByError(event CFSSEEvent, account *Account) {
	switch {
	case event.Reply == "15" || event.Reply == "61":
		h.pool.MarkAccountStatus(account.ID, "rate_limited")
	case event.Code == 403 || event.Reply == "802":
		h.pool.MarkAccountStatus(account.ID, "error")
	}
}

// convertMessages 将 OpenAI 消息转换为 CodeFlicker 格式（角色映射 + content 归一化）
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
