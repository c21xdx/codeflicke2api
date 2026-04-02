package main

import (
	"embed"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed web/*
var webFS embed.FS

func main() {
	cfg := LoadConfig()

	db := InitDB(cfg.DBPath)
	LoadSettingsIntoCfg(db, cfg)
	EnsureDefaultAPIKey(db, cfg.DefaultAPIKey)

	pool := NewAccountPool(db)
	upstream := NewUpstreamClient(cfg.CodeFlickerBaseURL, cfg.ProxyURL)
	oaiHandler := NewOpenAIHandler(pool, upstream, cfg, db)
	anthropicHandler := NewAnthropicHandler(pool, upstream, cfg, db)
	adminHandler := NewAdminHandler(db, cfg, upstream)

	r := gin.Default()

	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token, X-API-Key, Anthropic-Version")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	v1 := r.Group("/v1", APIKeyAuth(db))
	{
		v1.GET("/models", oaiHandler.HandleModels)
		v1.POST("/chat/completions", oaiHandler.HandleChatCompletions)
		v1.POST("/messages", anthropicHandler.HandleMessages)
	}

	r.POST("/admin/login", adminHandler.HandleLogin)

	admin := r.Group("/admin", AdminAuth(cfg))
	{
		admin.GET("/accounts", adminHandler.HandleListAccounts)
		admin.POST("/accounts", adminHandler.HandleCreateAccount)
		admin.POST("/accounts/batch", adminHandler.HandleBatchImport)
		admin.POST("/accounts/import", adminHandler.HandleFileImport)
		admin.POST("/accounts/refresh", adminHandler.HandleRefreshTokens)
		admin.PUT("/accounts/:id", adminHandler.HandleUpdateAccount)
		admin.DELETE("/accounts/:id", adminHandler.HandleDeleteAccount)

		admin.GET("/keys", adminHandler.HandleListKeys)
		admin.POST("/keys", adminHandler.HandleCreateKey)
		admin.DELETE("/keys/:id", adminHandler.HandleDeleteKey)
		admin.PUT("/keys/:id/toggle", adminHandler.HandleToggleKey)

		admin.GET("/stats", adminHandler.HandleStats)

		admin.GET("/settings", adminHandler.HandleGetSettings)
		admin.PUT("/settings", adminHandler.HandleUpdateSettings)
	}

	indexHTML, err := webFS.ReadFile("web/index.html")
	if err != nil {
		log.Fatalf("加载前端文件失败: %v", err)
	}
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	log.Printf(" CodeFlicker2API 服务启动在 :%s", cfg.Port)
	log.Printf(" 管理面板: http://localhost:%s", cfg.Port)
	log.Printf(" 默认 API Key: %s", cfg.DefaultAPIKey)
	log.Printf(" 管理面板 Token: %s", cfg.AdminToken)

	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
