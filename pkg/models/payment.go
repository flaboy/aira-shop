package models

import (
	"time"

	"github.com/flaboy/aira/aira-web/pkg/migration"
)

type PaymentRecord struct {
	ID              uint   `gorm:"primaryKey"`
	ExternalOrderID string `gorm:"size:100"` // 外部支付系统订单ID
	Channel         string `gorm:"size:50"`  // 支付渠道：paypal, stripe等
	Amount          int64  `gorm:"not null"` // 金额（分）
	Currency        string `gorm:"size:10;default:'USD'"`
	Status          string `gorm:"size:20"` // pending, completed, failed, cancelled

	// 业务上下文 - 由业务系统提供和解析
	BusinessContext string `gorm:"type:text"` // 业务上下文JSON，interface{}序列化

	// 时间戳
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

func (p *PaymentRecord) TableName() string {
	return "ar_payments"
}

func init() {
	migration.RegisterAutoMigrateModels(&PaymentRecord{})
}
