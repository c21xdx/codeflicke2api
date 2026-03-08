package main

import (
	"os"
)

// AppConfig 全局配置
type AppConfig struct {
	Port               string // 监听端口
	AdminToken         string // 管理面板登录 token
	DefaultAPIKey      string // 默认 API Key
	CodeFlickerBaseURL string // CodeFlicker 上游地址
	DBPath             string // 数据库文件路径
	RefreshConcurrency int    // 刷新 token 并发数
	RefreshRetries     int    // 刷新 token 重试次数
}

// LoadConfig 加载配置，优先使用环境变量
func LoadConfig() *AppConfig {
	cfg := &AppConfig{
		Port:               getEnv("PORT", "8080"),
		AdminToken:         getEnv("ADMIN_TOKEN", "123456"),
		DefaultAPIKey:      getEnv("DEFAULT_API_KEY", "sk-123456"),
		CodeFlickerBaseURL: getEnv("CODEFLICKER_BASE_URL", "https://www.codeflicker.ai"),
		DBPath:             getEnv("DB_PATH", "codeflicke2api.db"),
		RefreshConcurrency: 10, // 默认并发 10
		RefreshRetries:     3,  // 默认重试 3 次
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
