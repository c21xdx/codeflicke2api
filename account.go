package main

import (
	"sync"
	"time"

	"gorm.io/gorm"
)

type AccountPool struct {
	db    *gorm.DB
	mu    sync.Mutex
	index int
}

func NewAccountPool(db *gorm.DB) *AccountPool {
	return &AccountPool{db: db, index: 0}
}

func (p *AccountPool) GetNextAccount() (*Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

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

func (p *AccountPool) MarkAccountStatus(accountID uint, status string) {
	p.db.Model(&Account{}).Where("id = ?", accountID).Update("status", status)
}
