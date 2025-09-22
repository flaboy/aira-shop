package models

import (
	"encoding/json"
	"time"

	"github.com/flaboy/aira-web/pkg/migration"
)

type ShopLink struct {
	ID          uint            `gorm:"primaryKey"`
	Name        string          `gorm:"size:255"`
	Url         string          `gorm:"size:255"`
	Platform    string          `gorm:"size:50;index"`
	Credentials json.RawMessage `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *ShopLink) TableName() string {
	return "ar_shoplinks"
}

func init() {
	migration.RegisterAutoMigrateModels(&ShopLink{})
}
