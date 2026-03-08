package main

import (
	"log"
	"strconv"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Account CodeFlicker 账号
type Account struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Name      string     `json:"name"`                          // 备注名
	UserID    string     `json:"user_id"`                       // kwaipilot-username: main_xxx
	JWTToken  string     `json:"jwt_token"`                     // Bearer JWT token
	Email     string     `json:"email"`                         // 登录邮箱（用于刷新 token）
	Password  string     `json:"password"`                      // 登录密码（用于刷新 token）
	IsActive  bool       `gorm:"default:true" json:"is_active"` // 是否启用
	Status    string     `gorm:"default:normal" json:"status"`  // normal / error / rate_limited
	LastUsed  *time.Time `json:"last_used"`                     // 上次使用时间
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// APIKey 外部调用的 API Key
type APIKey struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Key       string    `gorm:"uniqueIndex" json:"key"` // sk-xxx 格式
	Name      string    `json:"name"`                   // 备注名
	IsActive  bool      `gorm:"default:true" json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// SystemSetting 系统配置键值对（持久化存储）
type SystemSetting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `json:"value"`
}

// InitDB 初始化数据库连接并自动迁移
func InitDB(dbPath string) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}

	// 自动迁移表结构
	if err := db.AutoMigrate(&Account{}, &APIKey{}, &SystemSetting{}); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	return db
}

// LoadSettingsIntoCfg 从数据库加载配置覆盖运行时设置
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
		}
	}
}

// EnsureDefaultAPIKey 确保默认 API Key 存在
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
