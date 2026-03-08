package main

import "os"

// AppConfig 应用全局配置
type AppConfig struct {
	Port               string // 服务监听端口
	AdminToken         string // 管理面板鉴权 Token
	DefaultAPIKey      string // 默认 API Key
	CodeFlickerBaseURL string // CodeFlicker 上游服务地址
	DBPath             string // SQLite 数据库文件路径
	RefreshConcurrency int    // Token 刷新并发数
	RefreshRetries     int    // Token 刷新重试次数
	ChatRetries        int    // 上游 Chat 接口请求重试次数
	ProxyURL           string // HTTP 代理地址（例如 http://127.0.0.1:7890）
}

// LoadConfig 从环境变量加载配置，未设置时使用默认值
func LoadConfig() *AppConfig {
	return &AppConfig{
		Port:               getEnv("PORT", "8080"),
		AdminToken:         getEnv("ADMIN_TOKEN", "123456"),
		DefaultAPIKey:      getEnv("DEFAULT_API_KEY", "sk-123456"),
		CodeFlickerBaseURL: getEnv("CODEFLICKER_BASE_URL", "https://www.codeflicker.ai"),
		DBPath:             getEnv("DB_PATH", "codeflicke2api.db"),
		RefreshConcurrency: 10,
		RefreshRetries:     3,
		ChatRetries:        3,
		ProxyURL:           getEnv("PROXY_URL", ""),
	}
}

// getEnv 读取环境变量，若未设置则返回 fallback 默认值
func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
