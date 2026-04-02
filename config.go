package main

import "os"

type AppConfig struct {
	Port               string
	AdminToken         string
	DefaultAPIKey      string
	CodeFlickerBaseURL string
	DBPath             string
	RefreshConcurrency int
	RefreshRetries     int
	ChatRetries        int
	ProxyURL           string
}

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

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
