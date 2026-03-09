package main

import (
	"log"
	"strconv"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Account CodeFlicker 上游账号模型
type Account struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `json:"name"`                          // 备注名
	UserID    string     `json:"user_id"`                       // 上游用户标识（kwaipilot-username）
	JWTToken  string     `json:"jwt_token"`                     // 上游 Bearer Token
	Email     string     `json:"email"`                         // 登录邮箱
	Password  string     `json:"password"`                      // 登录密码
	IsActive  bool       `gorm:"default:true" json:"is_active"` // 是否启用
	Status    string     `gorm:"default:normal" json:"status"`  // 状态: normal / error / rate_limited
	LastUsed  *time.Time `json:"last_used"`                     // 最近一次使用时间
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// APIKey 外部调用方的 API 鉴权密钥
type APIKey struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"uniqueIndex" json:"key"` // 密钥值（sk-xxx 格式）
	Name      string    `json:"name"`                   // 备注名
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// SystemSetting 系统配置持久化存储（键值对）
type SystemSetting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
}

// UsageLog API 调用用量记录
type UsageLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	InputTokens  int       `json:"input_tokens"`  // 输入 Token 数
	OutputTokens int       `json:"output_tokens"` // 输出 Token 数
	Model        string    `json:"model"`         // 使用的模型
	APIType      string    `json:"api_type"`      // 接口类型: openai / anthropic
	CreatedAt    time.Time `json:"created_at"`
}

// InitDB 初始化 SQLite 连接并执行表结构自动迁移
func InitDB(dbPath string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	if err := db.AutoMigrate(&Account{}, &APIKey{}, &SystemSetting{}, &UsageLog{}); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	return db
}

// LoadSettingsIntoCfg 从数据库加载持久化设置，覆盖内存中的运行时配置
func LoadSettingsIntoCfg(db *gorm.DB, cfg *AppConfig) {
	var settings []SystemSetting
	db.Find(&settings)

	for _, s := range settings {
		switch s.Key {
		case "admin_token":
			if s.Value != "" {
				cfg.AdminToken = s.Value
			}
		case "default_api_key":
			if s.Value != "" {
				cfg.DefaultAPIKey = s.Value
			}
		case "refresh_concurrency":
			if v, err := strconv.Atoi(s.Value); err == nil && v > 0 {
				cfg.RefreshConcurrency = v
			}
		case "refresh_retries":
			if v, err := strconv.Atoi(s.Value); err == nil && v > 0 {
				cfg.RefreshRetries = v
			}
		case "chat_retries":
			if v, err := strconv.Atoi(s.Value); err == nil && v > 0 {
				cfg.ChatRetries = v
			}
		case "proxy_url":
			cfg.ProxyURL = s.Value
		}
	}
}

// EnsureDefaultAPIKey 检查并创建默认 API Key（首次启动时）
func EnsureDefaultAPIKey(db *gorm.DB, defaultKey string) {
	var count int64
	db.Model(&APIKey{}).Where("key = ?", defaultKey).Count(&count)
	if count == 0 {
		db.Create(&APIKey{
			Key:      defaultKey,
			Name:     "默认 Key",
			IsActive: true,
		})
		log.Printf("已创建默认 API Key: %s", defaultKey)
	}
}
