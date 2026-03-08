package main

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

// AccountPool 基于 Round-Robin 策略的账号轮询池，线程安全
type AccountPool struct {
	db    *gorm.DB
	mu    sync.Mutex
	index int
}

// NewAccountPool 创建账号轮询池实例
func NewAccountPool(db *gorm.DB) *AccountPool {
	return &AccountPool{db: db, index: 0}
}

// GetNextAccount 通过 Round-Robin 获取下一个可用账号。
// 同时会自动将超过 5 分钟的 rate_limited 账号恢复为 normal 状态。
func (p *AccountPool) GetNextAccount() (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 自动恢复超时的限流账号
	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	p.db.Model(&Account{}).
		Where("status = ? AND updated_at < ?", "rate_limited", fiveMinAgo).
		Update("status", "normal")

	var accounts []Account
	result := p.db.Where("is_active = ? AND status = ?", true, "normal").
		Order("id ASC").Find(&accounts)
	if result.Error != nil {
		return nil, result.Error
	}
	if len(accounts) == 0 {
		return nil, gorm.ErrRecordNotFound
	}

	if p.index >= len(accounts) {
		p.index = 0
	}
	account := accounts[p.index]
	p.index = (p.index + 1) % len(accounts)

	now := time.Now()
	p.db.Model(&account).Update("last_used", now)

	return &account, nil
}

// MarkAccountStatus 更新指定账号的状态（normal / error / rate_limited）
func (p *AccountPool) MarkAccountStatus(accountID uint, status string) {
	p.db.Model(&Account{}).Where("id = ?", accountID).Update("status", status)
}
