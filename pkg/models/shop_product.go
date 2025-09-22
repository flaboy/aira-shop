package models

import (
	"encoding/json"
	"time"

	"github.com/flaboy/aira/aira-web/pkg/migration"
)

type ShopProduct struct {
	ID         uint            `gorm:"primaryKey"`
	ShopID     uint            `gorm:"index"`
	OuterID    string          `gorm:"size:255;index"`
	Status     string          `gorm:"size:50;default:'pending'"`
	Url        string          `gorm:"size:500"`
	Name       string          `gorm:"size:255"`
	Platform   string          `gorm:"size:50;index"`
	Data       json.RawMessage `gorm:"type:text"`
	RemoteData json.RawMessage `gorm:"type:text"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *ShopProduct) TableName() string {
	return "ar_shoplink_products"
}

func init() {
	migration.RegisterAutoMigrateModels(&ShopProduct{})
}
