package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
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
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
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
			Error: AnthropicError{Type: "invalid_request_error", Message: fmt.Sprintf("invalid request body: %v", err)},
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
				Error: AnthropicError{Type: "api_error", Message: "no available account"},
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
				Platform:      "codeflicker-ide",
				IDEVersion:    "1.101.2",
				PluginVersion: "9.6.2511250",
			},
		}

		resp, err := h.upstream.StreamChatCompletion(account, cfReq)
		if err != nil {
			lastErr = err
			log.Printf("upstream request failed (%d/%d): %v", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.JSON(http.StatusBadGateway, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: fmt.Sprintf("upstream request failed after %d retries: %v", maxRetries, lastErr)},
			})
			return
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatusCode = resp.StatusCode
			lastBody = string(bodyBytes)

			if resp.StatusCode == http.StatusForbidden {
				h.pool.MarkAccountStatus(account.ID, "error")
			}
			if resp.StatusCode == http.StatusRequestEntityTooLarge {
				c.JSON(http.StatusRequestEntityTooLarge, AnthropicErrorResponse{
					Type:  "error",
					Error: AnthropicError{Type: "invalid_request_error", Message: "max token limit exceeded"},
				})
				return
			}

			log.Printf("upstream returned HTTP %d (%d/%d): %s", resp.StatusCode, attempt, maxRetries, lastBody)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			c.JSON(lastStatusCode, AnthropicErrorResponse{
				Type:  "error",
				Error: AnthropicError{Type: "api_error", Message: fmt.Sprintf("upstream returned error after %d retries: %s", maxRetries, lastBody)},
			})
			return
		}

		if attempt > 1 {
			log.Printf("upstream request succeeded after retry %d", attempt)
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
			Error: AnthropicError{Type: "api_error", Message: "streaming not supported"},
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

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var totalOutputTokens int
	var totalInputTokens int
	hasToolCall := false
	nextBlockIndex := 0
	currentTextBlockIndex := -1
	pendingToolCalls := map[int]*OAIStreamToolCall{}
	emittedToolArgs := map[int]string{}

	startTextBlock := func() {
		if currentTextBlockIndex >= 0 {
			return
		}
		idx := nextBlockIndex
		nextBlockIndex++
		currentTextBlockIndex = idx
		writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
			Type:         "content_block_start",
			Index:        intPtr(idx),
			ContentBlock: &AnthropicContentBlock{Type: "text", Text: ""},
		})
		flusher.Flush()
	}
	stopTextBlock := func() {
		if currentTextBlockIndex < 0 {
			return
		}
		idx := currentTextBlockIndex
		writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: intPtr(idx),
		})
		flusher.Flush()
		currentTextBlockIndex = -1
	}
	emitToolUse := func(tc OAIStreamToolCall) {
		stopTextBlock()

		idx := nextBlockIndex
		nextBlockIndex++
		writeAnthropicSSE(c.Writer, "content_block_start", AnthropicStreamEvent{
			Type:  "content_block_start",
			Index: intPtr(idx),
			ContentBlock: &AnthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage("{}"),
			},
		})
		flusher.Flush()

		if args := strings.TrimSpace(tc.Function.Arguments); args != "" {
			writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
				Type:  "content_block_delta",
				Index: intPtr(idx),
				Delta: &AnthropicDelta{
					Type:        "input_json_delta",
					PartialJSON: args,
				},
			})
			flusher.Flush()
		}

		writeAnthropicSSE(c.Writer, "content_block_stop", AnthropicStreamEvent{
			Type:  "content_block_stop",
			Index: intPtr(idx),
		})
		flusher.Flush()
		hasToolCall = true
	}

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
			stopTextBlock()
			startTextBlock()
			errText := fmt.Sprintf("[error] %s (code: %d)", event.Tip, event.Code)
			writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
				Type:  "content_block_delta",
				Index: intPtr(currentTextBlockIndex),
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
					startTextBlock()
					writeAnthropicSSE(c.Writer, "content_block_delta", AnthropicStreamEvent{
						Type:  "content_block_delta",
						Index: intPtr(currentTextBlockIndex),
						Delta: &AnthropicDelta{Type: "text_delta", Text: choice.Message.Content},
					})
					flusher.Flush()
				}

				if len(choice.Message.ToolCalls) > 0 {
					mergeAnthropicToolCallState(choice.Message.ToolCalls, pendingToolCalls)
					for _, tc := range collectAnthropicToolCalls(pendingToolCalls, emittedToolArgs, false) {
						emitToolUse(tc)
					}
				}
			}
		}
	}

done:
	for _, tc := range collectAnthropicToolCalls(pendingToolCalls, emittedToolArgs, true) {
		emitToolUse(tc)
	}
	if nextBlockIndex == 0 {
		startTextBlock()
	}
	stopTextBlock()

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
	pendingToolCalls := map[int]*OAIStreamToolCall{}
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

		if event.Type != "data" {
			continue
		}

		var chatData CFChatData
		if err := json.Unmarshal(event.Data, &chatData); err != nil {
			continue
		}

		for _, choice := range chatData.Choices {
			fullContent.WriteString(choice.Message.Content)
			if len(choice.Message.ToolCalls) > 0 {
				mergeAnthropicToolCallState(choice.Message.ToolCalls, pendingToolCalls)
			}
		}
		if chatData.Usage != nil {
			usage = AnthropicUsage{
				InputTokens:  chatData.Usage.PromptTokens,
				OutputTokens: chatData.Usage.CompletionTokens,
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

	toolCalls := collectAnthropicToolCalls(pendingToolCalls, nil, true)
	for _, tc := range toolCalls {
		contentBlocks = append(contentBlocks, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: anthropicToolCallInput(tc.Function.Arguments),
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

func mapAnthropicRole(role string) string {
	switch role {
	case "developer":
		return "system"
	case "function":
		return "tool"
	default:
		return role
	}
}

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
		mappedRole := mapAnthropicRole(msg.Role)
		cfMsg := CFMessage{
			Role:       mappedRole,
			ToolCallID: msg.ToolCallID,
		}

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
				switch mappedRole {
				case "user":
					cfMessages = appendAnthropicUserBlocks(cfMessages, blocks)
				case "assistant":
					cfMessages = appendAnthropicAssistantBlocks(cfMessages, blocks)
				default:
					parts := anthropicBlocksToTextParts(blocks, true)
					contentJSON, _ := json.Marshal(parts)
					cfMsg.Content = contentJSON
					cfMessages = append(cfMessages, cfMsg)
				}
				continue
			}

			cfMsg.Content = msg.Content
		}

		cfMessages = append(cfMessages, cfMsg)
	}

	return cfMessages
}

func mergeAnthropicToolCallState(raw json.RawMessage, pending map[int]*OAIStreamToolCall) {
	var incoming []OAIStreamToolCall
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return
	}

	for _, tc := range incoming {
		existing, ok := pending[tc.Index]
		if !ok {
			existing = &OAIStreamToolCall{Index: tc.Index}
			pending[tc.Index] = existing
		}
		if tc.ID != "" {
			existing.ID = tc.ID
		}
		if tc.Type != "" {
			existing.Type = tc.Type
		}
		if tc.Function.Name != "" {
			existing.Function.Name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			existing.Function.Arguments += tc.Function.Arguments
		}
	}
}

func collectAnthropicToolCalls(pending map[int]*OAIStreamToolCall, emitted map[int]string, includeEmpty bool) []OAIStreamToolCall {
	indexes := make([]int, 0, len(pending))
	for idx := range pending {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	out := make([]OAIStreamToolCall, 0, len(indexes))
	for _, idx := range indexes {
		tc := pending[idx]
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			if !includeEmpty || (tc.ID == "" && tc.Function.Name == "") {
				continue
			}
			if emitted != nil {
				if emitted[idx] == "{}" {
					continue
				}
				emitted[idx] = "{}"
			}
			out = append(out, *tc)
			continue
		}
		if !json.Valid([]byte(args)) {
			continue
		}
		if emitted != nil && emitted[idx] == args {
			continue
		}
		out = append(out, *tc)
		if emitted != nil {
			emitted[idx] = args
		}
	}
	return out
}

func anthropicToolCallInput(arguments string) json.RawMessage {
	args := strings.TrimSpace(arguments)
	if args == "" || !json.Valid([]byte(args)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(args)
}

func appendAnthropicUserBlocks(cfMessages []CFMessage, blocks []AnthropicContentBlock) []CFMessage {
	var textParts []CFContentPart
	added := false

	flushText := func(force bool) {
		if len(textParts) == 0 && !force {
			return
		}
		if len(textParts) == 0 {
			textParts = []CFContentPart{{Type: "text", Text: ""}}
		}
		contentJSON, _ := json.Marshal(textParts)
		cfMessages = append(cfMessages, CFMessage{
			Role:    "user",
			Content: contentJSON,
		})
		textParts = nil
		added = true
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, CFContentPart{Type: "text", Text: b.Text})
			}
		case "image":
			textParts = append(textParts, CFContentPart{Type: "text", Text: "[image omitted]"})
		case "tool_result":
			flushText(false)
			resultText := extractToolResultContent(b)
			contentJSON, _ := json.Marshal([]CFContentPart{{Type: "text", Text: resultText}})
			cfMessages = append(cfMessages, CFMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    contentJSON,
			})
			added = true
		}
	}

	flushText(!added)
	return cfMessages
}

func appendAnthropicAssistantBlocks(cfMessages []CFMessage, blocks []AnthropicContentBlock) []CFMessage {
	cfMsg := CFMessage{Role: "assistant"}
	textParts := anthropicBlocksToTextParts(blocks, true)
	var oaiToolCalls []OAIToolCall

	for _, b := range blocks {
		if b.Type != "tool_use" {
			continue
		}
		oaiToolCalls = append(oaiToolCalls, OAIToolCall{
			ID:   b.ID,
			Type: "function",
			Function: OAIFunctionCall{
				Name:      b.Name,
				Arguments: string(anthropicToolCallInput(string(b.Input))),
			},
		})
	}

	contentJSON, _ := json.Marshal(textParts)
	cfMsg.Content = contentJSON
	if len(oaiToolCalls) > 0 {
		toolCallsJSON, _ := json.Marshal(oaiToolCalls)
		cfMsg.ToolCalls = toolCallsJSON
	}
	return append(cfMessages, cfMsg)
}

func anthropicBlocksToTextParts(blocks []AnthropicContentBlock, includeEmpty bool) []CFContentPart {
	var parts []CFContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, CFContentPart{Type: "text", Text: b.Text})
			}
		case "image":
			parts = append(parts, CFContentPart{Type: "text", Text: "[image omitted]"})
		}
	}
	if len(parts) == 0 && includeEmpty {
		return []CFContentPart{{Type: "text", Text: ""}}
	}
	return parts
}

func extractSystemText(system json.RawMessage) string {
	var systemStr string
	if err := json.Unmarshal(system, &systemStr); err == nil {
		return systemStr
	}

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

func extractToolResultContent(block AnthropicContentBlock) string {
	if len(block.Content) == 0 {
		return ""
	}

	var contentStr string
	if err := json.Unmarshal(block.Content, &contentStr); err == nil {
		return contentStr
	}

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
			_ = json.Unmarshal(nameRaw, &fn.Name)
		}
		if descRaw, ok := tool["description"]; ok {
			_ = json.Unmarshal(descRaw, &fn.Description)
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

func writeAnthropicSSE(w io.Writer, eventType string, data interface{}) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
}

func intPtr(i int) *int { return &i }
