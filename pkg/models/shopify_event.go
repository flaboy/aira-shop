package models

import (
	"encoding/json"
	"time"

	"github.com/flaboy/aira-web/pkg/migration"
)

type ShopifyEvent struct {
	ID              uint   `gorm:"primaryKey"`
	EventID         string `gorm:"size:64;uniqueIndex"`
	MessageID       string `gorm:"size:128;index"`
	Topic           string `gorm:"size:64;index"`
	ExternalShopID  string `gorm:"size:64;index"`
	ShopDomain      string `gorm:"size:255;index"`
	DeliveryMethod  string `gorm:"size:16;index"`
	PayloadHash     string `gorm:"size:64;index"`
	Status          string `gorm:"size:16;index"`
	AttemptCount    uint
	ProcessingToken string          `gorm:"size:128;index"`
	LeaseUntil      time.Time       `gorm:"index"`
	LastError       string          `gorm:"type:text"`
	Payload         json.RawMessage `gorm:"type:longblob"`
	ReceivedAt      time.Time
	LastAttemptAt   time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ShopifyEventAttempt struct {
	ID              uint   `gorm:"primaryKey"`
	EventID         string `gorm:"size:64;index:idx_shopify_event_attempt"`
	AttemptNo       uint   `gorm:"index:idx_shopify_event_attempt"`
	ProcessingToken string `gorm:"size:128;uniqueIndex"`
	Handler         string `gorm:"size:64"`
	Status          string `gorm:"size:16;index"`
	ErrorMessage    string `gorm:"type:text"`
	StartedAt       time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s *ShopifyEventAttempt) TableName() string {
	return "ar_shopify_event_attempts"
}

func (s *ShopifyEvent) TableName() string {
	return "ar_shopify_events"
}

func init() {
	migration.RegisterAutoMigrateModels(&ShopifyEvent{}, &ShopifyEventAttempt{})
}
