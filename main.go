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
	// 加载配置
	cfg := LoadConfig()

	// 初始化数据库
	db := InitDB(cfg.DBPath)
	LoadSettingsIntoCfg(db, cfg)
	EnsureDefaultAPIKey(db, cfg.DefaultAPIKey)

	// 初始化组件
	pool := NewAccountPool(db)
	upstream := NewUpstreamClient(cfg.CodeFlickerBaseURL)
	oaiHandler := NewOpenAIHandler(pool, upstream)
	adminHandler := NewAdminHandler(db, cfg)

	// 创建 Gin 路由
	r := gin.Default()

	// CORS 中间件
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Admin-Token")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// === OpenAI 兼容端点（需要 API Key 认证） ===
	v1 := r.Group("/v1", APIKeyAuth(db))
	{
		v1.GET("/models", oaiHandler.HandleModels)
		v1.POST("/chat/completions", oaiHandler.HandleChatCompletions)
	}

	// === 管理面板 API ===
	r.POST("/admin/login", adminHandler.HandleLogin)

	admin := r.Group("/admin", AdminAuth(cfg))
	{
		// 账号管理
		admin.GET("/accounts", adminHandler.HandleListAccounts)
		admin.POST("/accounts", adminHandler.HandleCreateAccount)
		admin.POST("/accounts/batch", adminHandler.HandleBatchImport)
		admin.POST("/accounts/import", adminHandler.HandleFileImport)
		admin.POST("/accounts/refresh", adminHandler.HandleRefreshTokens)
		admin.PUT("/accounts/:id", adminHandler.HandleUpdateAccount)
		admin.DELETE("/accounts/:id", adminHandler.HandleDeleteAccount)

		// Key 管理
		admin.GET("/keys", adminHandler.HandleListKeys)
		admin.POST("/keys", adminHandler.HandleCreateKey)
		admin.DELETE("/keys/:id", adminHandler.HandleDeleteKey)
		admin.PUT("/keys/:id/toggle", adminHandler.HandleToggleKey)

		// 统计
		admin.GET("/stats", adminHandler.HandleStats)

		// 设置
		admin.GET("/settings", adminHandler.HandleGetSettings)
		admin.PUT("/settings", adminHandler.HandleUpdateSettings)
	}

	// === 管理面板静态文件 ===
	indexHTML, err := webFS.ReadFile("web/index.html")
	if err != nil {
		log.Fatalf("加载前端文件失败: %v", err)
	}
	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	// 启动服务
	log.Printf(" CodeFlicker2API 服务启动在 :%s", cfg.Port)
	log.Printf(" 管理面板: http://localhost:%s", cfg.Port)
	log.Printf(" 默认 API Key: %s", cfg.DefaultAPIKey)
	log.Printf(" 管理面板 Token: %s", cfg.AdminToken)

	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
