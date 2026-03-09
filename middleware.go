package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// APIKeyAuth 验证请求中的 API Key 是否有效且已启用。
// 支持两种认证方式：
//   - OpenAI 风格: Authorization: Bearer sk-xxx
//   - Anthropic 风格: x-api-key: sk-xxx
func APIKeyAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var key string

		// 优先从 Authorization 头提取 Bearer Token
		auth := c.GetHeader("Authorization")
		if auth != "" {
			key = strings.TrimPrefix(auth, "Bearer ")
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
		}

		// 回退到 x-api-key 头
		if key == "" {
			key = c.GetHeader("x-api-key")
		}

		if key == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "缺少 API Key（请通过 Authorization 或 x-api-key 头提供）",
					"type":    "invalid_request_error",
					"code":    "missing_api_key",
				},
			})
			c.Abort()
			return
		}

		var apiKey APIKey
		if result := db.Where("key = ? AND is_active = ?", key, true).First(&apiKey); result.Error != nil {
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
