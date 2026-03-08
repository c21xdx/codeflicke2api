package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// APIKeyAuth API Key 鉴权中间件
func APIKeyAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "缺少 Authorization 头",
					"type":    "invalid_request_error",
					"code":    "missing_api_key",
				},
			})
			c.Abort()
			return
		}

		// 提取 Bearer token
		key := strings.TrimPrefix(auth, "Bearer ")
		if key == auth {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "Authorization 格式错误，应为 Bearer sk-xxx",
					"type":    "invalid_request_error",
					"code":    "invalid_api_key",
				},
			})
			c.Abort()
			return
		}

		// 查找 API Key
		var apiKey APIKey
		result := db.Where("key = ? AND is_active = ?", key, true).First(&apiKey)
		if result.Error != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "无效的 API Key",
					"type":    "invalid_request_error",
					"code":    "invalid_api_key",
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// AdminAuth 管理面板 token 鉴权中间件（使用 cfg 指针以支持动态更新）
func AdminAuth(cfg *AppConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("X-Admin-Token")
		if token == "" {
			token = c.Query("token")
		}
		if token != cfg.AdminToken {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未授权访问"})
			c.Abort()
			return
		}
		c.Next()
	}
}
