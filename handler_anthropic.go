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

type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	MaxTokens     int                `json:"max_tokens"`
	Stream        *bool              `json:"stream,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	System        json.RawMessage    `json:"system,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Metadata      json.RawMessage    `json:"metadata,omitempty"`
	Tools         json.RawMessage    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
}

type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`


	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   *string                 `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        AnthropicUsage          `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type AnthropicStreamEvent struct {
	Type         string                 `json:"type"`
	Index        *int                   `json:"index,omitempty"`
	Message      *AnthropicResponse     `json:"message,omitempty"`
	ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"`
	Delta        *AnthropicDelta        `json:"delta,omitempty"`
	Usage        *AnthropicUsage        `json:"usage,omitempty"`
}

type AnthropicDelta struct {
	Type         string          `json:"type,omitempty"`
	Text         string          `json:"text,omitempty"`
	StopReason   *string         `json:"stop_reason,omitempty"`
	StopSequence *string         `json:"stop_sequence,omitempty"`
	PartialJSON  string          `json:"partial_json,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
}

type AnthropicErrorResponse struct {
	Type  string         `json:"type"`
	Error AnthropicError `json:"error"`
}

type AnthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type AnthropicHandler struct {
	pool     *AccountPool
	upstream *UpstreamClient
	cfg      *AppConfig
	db       *gorm.DB
}

func NewAnthropicHandler(pool *AccountPool, upstream *UpstreamClient, cfg *AppConfig, db *gorm.DB) *AnthropicHandler {
	return &AnthropicHandler{pool: pool, upstream: upstream, cfg: cfg, db: db}
}

func (h *AnthropicHandler) HandleMessages(c *gin.Context) {
	var req AnthropicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, AnthropicErrorResponse{
			Type:  "error",
			Error: AnthropicError{Type: "invalid_request_error", Message: fmt.Sprintf("请求格式错误: %v", err)},
		})
		return
	}

	if req.MaxTokens <= 0 {
		req.MaxTokens = 64000
	}

	maxRetries := h.cfg.ChatRetries
	if maxRetries < 1 {
		maxRetries = 1
	}

	cfMessages := convertAnthropicMessages(req.Messages, req.System)
	upstreamModel := resolveModelName(req.Model)

	var cfTools json.RawMessage
	if len(req.Tools) > 0 {
		cfTools = convertAnthropicTools(req.Tools)
	}

	var lastErr error
	var lastStatusCode int
	var lastBody string

	for attempt := 1; attempt <= maxRetries; attempt++ {
		account, err := h.pool.GetNextAccount()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: "没有可用的账号"},
			})
			return
		}

		cfReq := &CFChatRequest{
			SessionID: uuid.New().String(),
			ChatID:    uuid.New().String(),
			Mode:      "agent",
			Messages:  cfMessages,
			Tools:     cfTools,
			Model:     upstreamModel,
			DeviceInfo: CFDeviceInfo{
				Platform: "codeflicker-ide", IDEVersion: "1.101.2", PluginVersion: "9.6.2511250",
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
			c.JSON(http.StatusBadGateway, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: fmt.Sprintf("上游请求失败（已重试 %d 次）: %v", maxRetries, lastErr)},
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
				c.JSON(http.StatusRequestEntityTooLarge, AnthropicErrorResponse{
					Type:  "error",
					Error: AnthropicError{Type: "invalid_request_error", Message: "超过最大 token 限制"},
				})
				return
			}

			log.Printf("上游返回 HTTP %d (第 %d/%d 次): %s", resp.StatusCode, attempt, maxRetries, lastBody)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.JSON(lastStatusCode, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: fmt.Sprintf("上游返回错误（已重试 %d 次）: %s", maxRetries, lastBody)},
			})
			return
		}


		if attempt > 1 {
			log.Printf("上游请求在第 %d 次重试后成功", attempt)
		}

		isStream := req.Stream != nil && *req.Stream
		if isStream {
			h.handleAnthropicStreamResponse(c, resp.Body, req.Model, account)
		} else {
			h.handleAnthropicNonStreamResponse(c, resp.Body, req.Model, account)
		}
		resp.Body.Close()
		return
	}
}

func (h *AnthropicHandler) handleAnthropicStreamResponse(c *gin.Context, body io.Reader, model string, account *Account) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, AnthropicErrorResponse{
			Type:  "error",
			Error: AnthropicError{Type: "api_error", Message: "流式传输不支持"},
		})
		return
	}

	respID := "msg_" + uuid.New().String()[:24]


	msgStartResp := &AnthropicResponse{
		ID:      respID,
		Type:    "message",
		Role:    "assistant",
		Model:   model,
		Content: []AnthropicContentBlock{},
		Usage:   AnthropicUsage{InputTokens: 0, OutputTokens: 0},
	}
	writeAnthropicSSE(c.Writer, "message_start", AnthropicStreamEvent{
		Type:    "message_start",
		Message: msgStartResp,
	})
	flusher.Flush()


	contentBlockIdx := 0
	writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
		Type:         "content_block_start",
		Index:        intPtr(contentBlockIdx),
		ContentBlock: &AnthropicContentBlock{Type: "text", Text: ""},
	})
	flusher.Flush()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var totalOutputTokens int
	var totalInputTokens int
	hasToolCall := false

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
			h.markAnthropicAccountByError(event, account)
			writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
				Type:  "content_block_stop",
				Index: intPtr(contentBlockIdx),
			})
			flusher.Flush()
			contentBlockIdx++
			writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
				Type:         "content_block_start",
				Index:        intPtr(contentBlockIdx),
				ContentBlock: &AnthropicContentBlock{Type: "text", Text: ""},
			})
			flusher.Flush()
			errText := fmt.Sprintf("[错误] %s (code: %d)", event.Tip, event.Code)
			writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
				Type:  "content_block_delta",
				Index: intPtr(contentBlockIdx),
				Delta: &AnthropicDelta{Type: "text_delta", Text: errText},
			})
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
				totalInputTokens = chatData.Usage.PromptTokens
				totalOutputTokens = chatData.Usage.CompletionTokens
			}

			for _, choice := range chatData.Choices {

				if choice.Message.Content != "" {
					writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: intPtr(contentBlockIdx),
						Delta: &AnthropicDelta{Type: "text_delta", Text: choice.Message.Content},
					})
					flusher.Flush()
				}


				if len(choice.Message.ToolCalls) > 0 {
					hasToolCall = true

					writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
						Type:  "content_block_stop",
						Index: intPtr(contentBlockIdx),
					})
					flusher.Flush()


					var toolCalls []OAIToolCall
					if err := json.Unmarshal(choice.Message.ToolCalls, &toolCalls); err == nil {
						for _, tc := range toolCalls {
							contentBlockIdx++

							writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
								Type:  "content_block_start",
								Index: intPtr(contentBlockIdx),
								ContentBlock: &AnthropicContentBlock{
									Type:  "tool_use",
									ID:    tc.ID,
									Name:  tc.Function.Name,
									Input: json.RawMessage("{}"),
								},
							})
							flusher.Flush()


							if len(tc.Function.Arguments) > 0 {
								writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
									Type:  "content_block_delta",
									Index: intPtr(contentBlockIdx),
									Delta: &AnthropicDelta{
										Type:        "input_json_delta",
										PartialJSON: tc.Function.Arguments,
									},
								})
								flusher.Flush()
							}


							writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
								Type:  "content_block_stop",
								Index: intPtr(contentBlockIdx),
							})
							flusher.Flush()
						}
					}


					contentBlockIdx++
					writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
						Type:         "content_block_start",
						Index:        intPtr(contentBlockIdx),
						ContentBlock: &AnthropicContentBlock{Type: "text", Text: ""},
					})
					flusher.Flush()
				}
			}
		}
	}

done:
	writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: intPtr(contentBlockIdx),
	})
	flusher.Flush()

	stopReason := "end_turn"
	if hasToolCall {
		stopReason = "tool_use"
	}
	writeAnthropicSSE(c.Writer, "message_delta", AnthropicStreamEvent{
		Type:  "message_delta",
		Delta: &AnthropicDelta{StopReason: &stopReason},
		Usage: &AnthropicUsage{InputTokens: totalInputTokens, OutputTokens: totalOutputTokens},
	})
	flusher.Flush()

	writeAnthropicSSE(c.Writer, "message_stop", AnthropicStreamEvent{
		Type: "message_stop",
	})
	flusher.Flush()

	h.recordAnthropicUsage(model, totalInputTokens, totalOutputTokens)
}

func (h *AnthropicHandler) handleAnthropicNonStreamResponse(c *gin.Context, body io.Reader, model string, account *Account) {
	respID := "msg_" + uuid.New().String()[:24]
	var fullContent strings.Builder
	var toolCalls []OAIToolCall
	var usage AnthropicUsage

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
			h.markAnthropicAccountByError(event, account)
			c.JSON(http.StatusBadGateway, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: event.Tip},
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
					var tcs []OAIToolCall
					if err := json.Unmarshal(choice.Message.ToolCalls, &tcs); err == nil {
						toolCalls = append(toolCalls, tcs...)
					}
				}
			}
			if chatData.Usage != nil {
				usage = AnthropicUsage{
					InputTokens:  chatData.Usage.PromptTokens,
					OutputTokens: chatData.Usage.CompletionTokens,
				}
			}
		}
	}

	var contentBlocks []AnthropicContentBlock

	text := fullContent.String()
	if text != "" {
		contentBlocks = append(contentBlocks, AnthropicContentBlock{
			Type: "text",
			Text: text,
		})
	}

	for _, tc := range toolCalls {
		var inputJSON json.RawMessage
		if tc.Function.Arguments != "" {
			inputJSON = json.RawMessage(tc.Function.Arguments)
		} else {
			inputJSON = json.RawMessage("{}")
		}
		contentBlocks = append(contentBlocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: inputJSON,
		})
	}

	if len(contentBlocks) == 0 {
		contentBlocks = []AnthropicContentBlock{{Type: "text", Text: ""}}
	}

	stopReason := "end_turn"
	if len(toolCalls) > 0 {
		stopReason = "tool_use"
	}

	h.recordAnthropicUsage(model, usage.InputTokens, usage.OutputTokens)

	c.JSON(http.StatusOK, AnthropicResponse{
		ID:         respID,
		Type:       "message",
		Role:       "assistant",
		Content:    contentBlocks,
		Model:      model,
		StopReason: &stopReason,
		Usage:      usage,
	})
}

func (h *AnthropicHandler) recordAnthropicUsage(model string, inputTokens, outputTokens int) {
	if inputTokens == 0 && outputTokens == 0 {
		return
	}
	h.db.Create(&UsageLog{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Model:        model,
		APIType:      "anthropic",
	})
}

func (h *AnthropicHandler) markAnthropicAccountByError(event CFSSEEvent, account *Account) {
	switch {
	case event.Reply == "15" || event.Reply == "61":
		h.pool.MarkAccountStatus(account.ID, "rate_limited")
	case event.Code == 403 || event.Reply == "802":
		h.pool.MarkAccountStatus(account.ID, "error")
	}
}


type OAIToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function OAIFunctionCall `json:"function"`
}

type OAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// convertAnthropicMessages 将 Anthropic 消息格式转换为 CodeFlicker 格式
func convertAnthropicMessages(messages []AnthropicMessage, system json.RawMessage) []CFMessage {
	cfMessages := make([]CFMessage, 0, len(messages)+1)

	if len(system) > 0 {
		systemText := extractSystemText(system)
		if systemText != "" {
			parts := []CFContentPart{{Type: "text", Text: systemText}}
			contentJSON, _ := json.Marshal(parts)
			cfMessages = append(cfMessages, CFMessage{
				Role:    "system",
				Content: contentJSON,
			})
		}
	}

	for _, msg := range messages {
		cfMsg := CFMessage{Role: msg.Role}

		if len(msg.Content) > 0 {
			var contentStr string
			if err := json.Unmarshal(msg.Content, &contentStr); err == nil {
				parts := []CFContentPart{{Type: "text", Text: contentStr}}
				contentJSON, _ := json.Marshal(parts)
				cfMsg.Content = contentJSON
				cfMessages = append(cfMessages, cfMsg)
				continue
			}

			var blocks []AnthropicContentBlock
			if err := json.Unmarshal(msg.Content, &blocks); err == nil {
				hasToolResult := false
				hasToolUse := false
				for _, b := range blocks {
					if b.Type == "tool_result" {
						hasToolResult = true
					}
					if b.Type == "tool_use" {
						hasToolUse = true
					}
				}

				if hasToolResult {
					for _, b := range blocks {
						if b.Type == "tool_result" {
							toolMsg := CFMessage{
								Role:       "tool",
								ToolCallID: b.ToolUseID,
							}
							resultText := extractToolResultContent(b)
							parts := []CFContentPart{{Type: "text", Text: resultText}}
							contentJSON, _ := json.Marshal(parts)
							toolMsg.Content = contentJSON
							cfMessages = append(cfMessages, toolMsg)
						}
					}
					continue
				}

				if hasToolUse && msg.Role == "assistant" {
					var textParts []CFContentPart
					var oaiToolCalls []OAIToolCall
					for _, b := range blocks {
						switch b.Type {
						case "text":
							if b.Text != "" {
								textParts = append(textParts, CFContentPart{Type: "text", Text: b.Text})
							}
						case "tool_use":
							oaiToolCalls = append(oaiToolCalls, OAIToolCall{
								ID:   b.ID,
								Type: "function",
								Function: OAIFunctionCall{
									Name:      b.Name,
									Arguments: string(b.Input),
								},
							})
						}
					}
					if len(textParts) == 0 {
						textParts = []CFContentPart{{Type: "text", Text: ""}}
					}
					contentJSON, _ := json.Marshal(textParts)
					cfMsg.Content = contentJSON
					if len(oaiToolCalls) > 0 {
						toolCallsJSON, _ := json.Marshal(oaiToolCalls)
						cfMsg.ToolCalls = toolCallsJSON
					}
					cfMessages = append(cfMessages, cfMsg)
					continue
				}

				// 普通文本消息
				var parts []CFContentPart
				for _, b := range blocks {
					if b.Type == "text" {
						parts = append(parts, CFContentPart{Type: "text", Text: b.Text})
					} else if b.Type == "image" {
						parts = append(parts, CFContentPart{Type: "text", Text: "[图片内容已省略]"})
					}
				}
				if len(parts) == 0 {
					parts = []CFContentPart{{Type: "text", Text: ""}}
				}
				contentJSON, _ := json.Marshal(parts)
				cfMsg.Content = contentJSON
				cfMessages = append(cfMessages, cfMsg)
				continue
			}

			cfMsg.Content = msg.Content
		}

		cfMessages = append(cfMessages, cfMsg)
	}

	return cfMessages
}

// extractSystemText 从 Anthropic system 字段中提取文本
// system 可以是字符串或 content block 数组
func extractSystemText(system json.RawMessage) string {
	// 尝试解析为字符串
	var systemStr string
	if err := json.Unmarshal(system, &systemStr); err == nil {
		return systemStr
	}

	// 尝试解析为 content block 数组
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(system, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

// extractToolResultContent 从 Anthropic tool_result 块中提取文本内容
func extractToolResultContent(block AnthropicContentBlock) string {
	if len(block.Content) == 0 {
		return ""
	}

	// 尝试解析为字符串
	var contentStr string
	if err := json.Unmarshal(block.Content, &contentStr); err == nil {
		return contentStr
	}

	// 尝试解析为 content block 数组
	var contentBlocks []AnthropicContentBlock
	if err := json.Unmarshal(block.Content, &contentBlocks); err == nil {
		var parts []string
		for _, cb := range contentBlocks {
			if cb.Type == "text" {
				parts = append(parts, cb.Text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return string(block.Content)
}

// convertAnthropicTools 将 Anthropic tools 格式转换为 OpenAI/CF tools 格式
// Anthropic: [{name, description, input_schema}] → OpenAI: [{type: "function", function: {name, description, parameters}}]
func convertAnthropicTools(anthropicTools json.RawMessage) json.RawMessage {
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(anthropicTools, &tools); err != nil {
		return anthropicTools
	}

	type oaiFunction struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	}
	type oaiTool struct {
		Type     string      `json:"type"`
		Function oaiFunction `json:"function"`
	}

	var oaiTools []oaiTool
	for _, tool := range tools {
		fn := oaiFunction{}

		if nameRaw, ok := tool["name"]; ok {
			json.Unmarshal(nameRaw, &fn.Name)
		}
		if descRaw, ok := tool["description"]; ok {
			json.Unmarshal(descRaw, &fn.Description)
		}
		if schemaRaw, ok := tool["input_schema"]; ok {
			fn.Parameters = schemaRaw
		}

		oaiTools = append(oaiTools, oaiTool{Type: "function", Function: fn})
	}

	result, err := json.Marshal(oaiTools)
	if err != nil {
		return anthropicTools
	}
	return result
}

// writeAnthropicSSE 写入 Anthropic 格式的 SSE 事件
func writeAnthropicSSE(w io.Writer, eventType string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
}

// intPtr 返回整数指针
func intPtr(i int) *int { return &i }
