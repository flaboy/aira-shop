package models

import (
	"encoding/json"
	"time"

	"github.com/flaboy/aira-web/pkg/migration"
)

type ShopLink struct {
	ID                    uint            `gorm:"primaryKey"`
	Name                  string          `gorm:"size:255"`
	Url                   string          `gorm:"size:255"`
	Platform              string          `gorm:"size:50;index;uniqueIndex:idx_ar_shoplinks_platform_external;uniqueIndex:idx_ar_shoplinks_platform_domain"`
	ExternalShopID        string          `gorm:"size:64;uniqueIndex:idx_ar_shoplinks_platform_external"`
	Credentials           json.RawMessage `gorm:"type:text"`
	InstallationStatus    string          `gorm:"size:32;index"`
	ShopDomain            string          `gorm:"size:255;uniqueIndex:idx_ar_shoplinks_platform_domain"`
	GrantedScopes         string          `gorm:"type:text"`
	LastAuthorizedAt      *time.Time
	UninstalledAt         *time.Time
	CredentialCiphertext  string `gorm:"type:text"`
	CredentialKeyVersion  string `gorm:"size:64"`
	AccessTokenExpiresAt  *time.Time
	RefreshTokenExpiresAt *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

const (
	ShopInstallationStatusActive               = "active"
	ShopInstallationStatusUninstalled          = "uninstalled"
	ShopInstallationStatusAuthorizationExpired = "authorization_expired"
)

func (s *ShopLink) TableName() string {
	return "ar_shoplinks"
}

func init() {
	migration.RegisterAutoMigrateModels(&ShopLink{})
}
