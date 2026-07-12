package models

import (
	"time"

	"github.com/flaboy/aira-web/pkg/migration"
)

// ShopifyVariantMapping 为订单路由提供数据库级唯一的远端与本地变体身份。
type ShopifyVariantMapping struct {
	ID               uint   `gorm:"primaryKey"`
	ShopID           uint   `gorm:"uniqueIndex:idx_shopify_variant_identity,priority:1"`
	ShopProductID    uint   `gorm:"uniqueIndex:idx_shopify_product_local_variant,priority:1;index"`
	ShopifyVariantID uint64 `gorm:"uniqueIndex:idx_shopify_variant_identity,priority:2"`
	LocalVariantID   uint   `gorm:"uniqueIndex:idx_shopify_product_local_variant,priority:2;index"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s *ShopifyVariantMapping) TableName() string {
	return "shopify_variant_mappings"
}

func init() {
	migration.RegisterAutoMigrateModels(&ShopifyVariantMapping{})
}
