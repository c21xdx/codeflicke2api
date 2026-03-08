package main

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

// AccountPool 账号轮询池
type AccountPool struct {
	db    *gorm.DB
	mu    sync.Mutex
	index int // 当前轮询索引
}

// NewAccountPool 创建账号池
func NewAccountPool(db *gorm.DB) *AccountPool {
	return &AccountPool{db: db, index: 0}
}

// GetNextAccount 轮询获取下一个可用账号
func (p *AccountPool) GetNextAccount() (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 自动恢复：将超过5分钟的 rate_limited 账号恢复为 normal
	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	p.db.Model(&Account{}).
		Where("status = ? AND updated_at < ?", "rate_limited", fiveMinAgo).
		Update("status", "normal")

	// 获取所有可用账号
	var accounts []Account
	result := p.db.Where("is_active = ? AND status = ?", true, "normal").
		Order("id ASC").Find(&accounts)
	if result.Error != nil {
		return nil, result.Error
	}
	if len(accounts) == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	// Round-Robin 轮询
	if p.index >= len(accounts) {
		p.index = 0
	}
	account := accounts[p.index]
	p.index = (p.index + 1) % len(accounts)

	// 更新最后使用时间
	now := time.Now()
	p.db.Model(&account).Update("last_used", now)

	return &account, nil
}

// MarkAccountStatus 标记账号状态
func (p *AccountPool) MarkAccountStatus(accountID uint, status string) {
	p.db.Model(&Account{}).Where("id = ?", accountID).Update("status", status)
}
