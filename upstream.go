package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type UpstreamClient struct {
	baseURL    string
	proxyURL   string
	httpClient *http.Client
}

func NewUpstreamClient(baseURL, proxyURL string) *UpstreamClient {
	transport := buildTransport(proxyURL)
	return &UpstreamClient{
		baseURL:  baseURL,
		proxyURL: proxyURL,
		httpClient: &http.Client{
			Timeout:   120 * time.Second,
			Transport: transport,
		},
	}
}

func buildTransport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return &http.Transport{}
	}
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return &http.Transport{}
	}
	return &http.Transport{
		Proxy: http.ProxyURL(proxyParsed),
	}
}

func (u *UpstreamClient) UpdateProxy(proxyURL string) {
	u.proxyURL = proxyURL
	u.httpClient.Transport = buildTransport(proxyURL)
}

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

// CodeFlicker Composer V2 请求/响应结构体

type CFChatRequest struct {
	SessionID           string          `json:"sessionId"`
	ChatID              string          `json:"chatId"`
	Mode                string          `json:"mode"`
	Round               int             `json:"round"`
	Messages            []CFMessage     `json:"messages"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	Model               string          `json:"model"`
	DeviceInfo          CFDeviceInfo    `json:"deviceInfo"`
	Rules               []string        `json:"rules,omitempty"`
	ProjectInfo         *CFProjectInfo  `json:"projectInfo,omitempty"`
	Environment         string          `json:"environment,omitempty"`
	SystemPromptVersion string          `json:"systemPromptVersion,omitempty"`
}

type CFProjectInfo struct {
	GitURL   string `json:"gitUrl,omitempty"`
	RepoName string `json:"repoName,omitempty"`
}

type CFMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ChatID     string          `json:"chatId,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type CFContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type CFDeviceInfo struct {
	Platform      string `json:"platform"`
	IDEVersion    string `json:"ideVersion"`
	PluginVersion string `json:"pluginVersion"`
}

type CFSSEEvent struct {
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data,omitempty"`
	Code    int             `json:"code,omitempty"`
	Tip     string          `json:"tip,omitempty"`
	TraceID string          `json:"traceId,omitempty"`
	Reply   string          `json:"reply,omitempty"`
}

type CFChatData struct {
	ID      string     `json:"id"`
	Object  string     `json:"object"`
	Created int64      `json:"created"`
	Model   string     `json:"model"`
	Choices []CFChoice `json:"choices"`
	Usage   *CFUsage   `json:"usage,omitempty"`
}

type CFChoice struct {
	Message      CFChoiceMessage `json:"message"`
	FinishReason *string         `json:"finish_reason"`
}

type CFChoiceMessage struct {
	Content   string          `json:"content"`
	Role      string          `json:"role"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

type CFUsage struct {
	CompletionTokens int `json:"completion_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type CFModelResponse struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type CFModelItem struct {
	ModelType    string `json:"modelType"`
	Name         string `json:"name"`
	Desc         string `json:"desc"`
	MaxLength    string `json:"maxLength"`
	SupportAgent bool   `json:"supportAgent"`
	SupportImage bool   `json:"supportImage"`
}

func (u *UpstreamClient) GetModels(account *Account) ([]CFModelItem, error) {
	agentURL := fmt.Sprintf("%s/api/proxy/eapi/kwaipilot/model/list?feature=agent", u.baseURL)
	agentModels, err := u.fetchModels(agentURL, account)
	if err != nil {
		return nil, fmt.Errorf("获取 Agent 模型失败: %w", err)
	}

	seen := make(map[string]bool)
	var models []CFModelItem
	for _, m := range agentModels {
		if !seen[m.ModelType] {
			seen[m.ModelType] = true
			models = append(models, m)
		}
	}

	return models, nil
}

func (u *UpstreamClient) fetchModels(url string, account *Account) ([]CFModelItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	for k, v := range u.buildHeaders(account) {
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
		return nil, fmt.Errorf("响应解析失败: %w, body: %s", err, string(body))
	}

	if cfResp.Status != 200 {
		return nil, fmt.Errorf("上游错误: status=%d, message=%s", cfResp.Status, cfResp.Message)
	}

	var models []CFModelItem
	if err := json.Unmarshal(cfResp.Data, &models); err != nil {
		return nil, fmt.Errorf("模型列表解析失败: %w", err)
	}

	return models, nil
}

// StreamChatCompletion 向上游发送聊天请求并返回 SSE 流式响应（使用无超时 Client）
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

	for k, v := range u.buildHeaders(account) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0, Transport: buildTransport(u.proxyURL)}
	return client.Do(req)
}

// ParseSSEStream 解析 CodeFlicker SSE 流，遇到 [DONE] 或流结束时关闭 channel
func ParseSSEStream(reader io.Reader) <-chan CFSSEEvent {
	ch := make(chan CFSSEEvent, 64)

	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(reader)
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

			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

			if data == "[DONE]" {
				return
			}

			var event CFSSEEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			ch <- event
		}
	}()

	return ch
}
