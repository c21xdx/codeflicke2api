package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// UpstreamClient CodeFlicker 上游请求客户端
type UpstreamClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewUpstreamClient 创建上游客户端
func NewUpstreamClient(baseURL string) *UpstreamClient {
	return &UpstreamClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// buildHeaders 构建 CodeFlicker 请求头
func (u *UpstreamClient) buildHeaders(account *Account) map[string]string {
	return map[string]string{
		"Content-Type":       "application/json;charset=UTF-8",
		"Accept-Language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"Authorization":      "Bearer " + account.JWTToken,
		"kwaipilot-username": account.UserID,
		"kwaipilot-platform": "codeflicker-ide",
		"kwaipilot-version":  "9.6.2511250",
		"User-Agent":         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) CodeFlicker/1.101.2 Chrome/134.0.6998.205 Electron/35.5.1 Safari/537.36",
	}
}

// ChatCompletionRequest CodeFlicker 格式的聊天请求（按 api.md 5.1 节）
type CFChatRequest struct {
	SessionID           string          `json:"sessionId"`
	ChatID              string          `json:"chatId"`
	Mode                string          `json:"mode"`
	Round               int             `json:"round"`
	Messages            []CFMessage     `json:"messages"`
	Tools               json.RawMessage `json:"tools,omitempty"` // OpenAI tools 透传
	Model               string          `json:"model"`
	DeviceInfo          CFDeviceInfo    `json:"deviceInfo"`
	Rules               []string        `json:"rules,omitempty"`               // 规则列表
	ProjectInfo         *CFProjectInfo  `json:"projectInfo,omitempty"`         // 项目信息
	Environment         string          `json:"environment,omitempty"`         // 环境信息
	SystemPromptVersion string          `json:"systemPromptVersion,omitempty"` // 系统提示词版本号
}

// CFProjectInfo 项目信息
type CFProjectInfo struct {
	GitURL   string `json:"gitUrl,omitempty"`
	RepoName string `json:"repoName,omitempty"`
}

// CFMessage CodeFlicker 消息格式
type CFMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ChatID     string          `json:"chatId,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// CFContentPart 消息内容片段
type CFContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// CFDeviceInfo 设备信息
type CFDeviceInfo struct {
	Platform      string `json:"platform"`
	IDEVersion    string `json:"ideVersion"`
	PluginVersion string `json:"pluginVersion"`
}

// CFSSEEvent CodeFlicker SSE 事件
type CFSSEEvent struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Code    int             `json:"code,omitempty"`
	Tip     string          `json:"tip,omitempty"`
	TraceID string          `json:"traceId,omitempty"`
	Reply   string          `json:"reply,omitempty"`
}

// CFChatData SSE data 字段中的对话数据
type CFChatData struct {
	ID      string     `json:"id"`
	Object  string     `json:"object"`
	Created int64      `json:"created"`
	Model   string     `json:"model"`
	Choices []CFChoice `json:"choices"`
	Usage   *CFUsage   `json:"usage,omitempty"`
}

// CFChoice 选项
type CFChoice struct {
	Message      CFChoiceMessage `json:"message"`
	FinishReason *string         `json:"finish_reason"`
}

// CFChoiceMessage 选项消息
type CFChoiceMessage struct {
	Content   string          `json:"content"`
	Role      string          `json:"role"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// CFUsage 使用量
type CFUsage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CFModelResponse 模型列表响应
type CFModelResponse struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// CFModelItem 模型条目
type CFModelItem struct {
	ModelType    string `json:"modelType"`
	Name         string `json:"name"`
	Desc         string `json:"desc"`
	MaxLength    string `json:"maxLength"`
	SupportAgent bool   `json:"supportAgent"`
	SupportImage bool   `json:"supportImage"`
}

// GetModels 获取模型列表（仅获取 Agent 模型，聊天模型已丢弃）
func (u *UpstreamClient) GetModels(account *Account) ([]CFModelItem, error) {
	// 只获取 Agent 模型
	agentURL := fmt.Sprintf("%s/api/proxy/eapi/kwaipilot/model/list?feature=agent", u.baseURL)
	agentModels, err := u.fetchModels(agentURL, account)
	if err != nil {
		return nil, fmt.Errorf("获取 Agent 模型失败: %w", err)
	}

	// 去重
	seen := make(map[string]bool)
	var allModels []CFModelItem
	for _, m := range agentModels {
		if !seen[m.ModelType] {
			seen[m.ModelType] = true
			allModels = append(allModels, m)
		}
	}

	return allModels, nil
}

// fetchModels 从指定 URL 获取模型列表
func (u *UpstreamClient) fetchModels(url string, account *Account) ([]CFModelItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	headers := u.buildHeaders(account)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var cfResp CFModelResponse
	if err := json.Unmarshal(body, &cfResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w, body: %s", err, string(body))
	}

	if cfResp.Status != 200 {
		return nil, fmt.Errorf("上游返回错误: status=%d, message=%s", cfResp.Status, cfResp.Message)
	}

	var models []CFModelItem
	if err := json.Unmarshal(cfResp.Data, &models); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}

	return models, nil
}

// StreamChatCompletion 流式聊天补全
func (u *UpstreamClient) StreamChatCompletion(account *Account, cfReq *CFChatRequest) (*http.Response, error) {
	url := fmt.Sprintf("%s/api/proxy/sse/eapi/kwaipilot/plugin/composer/v2/chat/completions", u.baseURL)

	bodyBytes, err := json.Marshal(cfReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	headers := u.buildHeaders(account)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")

	// 使用不带超时的 client，因为 SSE 可能持续很长时间
	client := &http.Client{Timeout: 0}
	return client.Do(req)
}

// ParseSSEStream 解析 CodeFlicker SSE 流，通过 channel 返回事件
func ParseSSEStream(reader io.Reader) <-chan CFSSEEvent {
	ch := make(chan CFSSEEvent, 64)

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(reader)
		// 增加缓冲区大小以处理长行
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			// 跳过空行和注释行
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// 解析 data: 前缀
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimSpace(data)

				if data == "[DONE]" {
					return
				}

				var event CFSSEEvent
				if err := json.Unmarshal([]byte(data), &event); err != nil {
					// 可能是纯文本数据，包装为 data 事件
					continue
				}

				ch <- event
			}
		}
	}()

	return ch
}
