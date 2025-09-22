package shoplink

import (
	"encoding/json"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/shopify"
	"github.com/flaboy/aira-shop/pkg/models"
	"gorm.io/gorm"
)

var platforms map[string]ShopPlatform

func Init() error {
	platforms = make(map[string]ShopPlatform)

	// 注册Shopify平台
	shopifyPlatform := &shopify.Shopify{}
	if err := shopifyPlatform.Init(); err != nil {
		return err
	}
	platforms[shopifyPlatform.GetPlatformName()] = shopifyPlatform

	return nil
}

func Get(platformName string) ShopPlatform {
	return platforms[platformName]
}

func GetSupportedPlatforms() []string {
	var names []string
	for name := range platforms {
		names = append(names, name)
	}
	return names
}

// 新增函数
func CreateShop(platform, name, url string, credentials json.RawMessage) (*models.ShopLink, error) {
	shopLink := &models.ShopLink{
		Platform:    platform,
		Name:        name,
		Url:         url,
		Credentials: credentials,
	}

	db := database.Database()

	// 检查是否已存在
	var existing models.ShopLink
	err := db.Where("name = ? AND platform = ?", name, platform).First(&existing).Error
	if err == nil {
		// 更新现有记录
		existing.Credentials = credentials
		existing.Url = url
		return &existing, db.Save(&existing).Error
	}

	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// 创建新记录
	if err := db.Create(shopLink).Error; err != nil {
		return nil, err
	}

	return shopLink, nil
}
