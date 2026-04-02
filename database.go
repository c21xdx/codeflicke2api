package main

import (
	"log"
	"strconv"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type Account struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `json:"name"`
	UserID    string     `json:"user_id"`
	JWTToken  string     `json:"jwt_token"`
	Email     string     `json:"email"`
	Password  string     `json:"password"`
	IsActive  bool       `gorm:"default:true" json:"is_active"`
	Status    string     `gorm:"default:normal" json:"status"` // normal / error / rate_limited
	LastUsed  *time.Time `json:"last_used"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type APIKey struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"uniqueIndex" json:"key"`
	Name      string    `json:"name"`
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

type SystemSetting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
}

type UsageLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	Model        string    `json:"model"`
	APIType      string    `json:"api_type"` // openai / anthropic
	CreatedAt    time.Time `json:"created_at"`
}

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
