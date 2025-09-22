package utils

import (
	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/models"
)

func GetShopProduct(platform string, outerID string) (*models.ShopProduct, bool, error) {
	product := &models.ShopProduct{}
	result := database.Database().Where("platform = ? AND outer_id = ?", platform, outerID).First(product)
	if result.Error != nil {
		if result.Error.Error() == "record not found" {
			return nil, false, nil
		}
		return nil, false, result.Error
	}
	return product, true, nil
}

func CreateShopProduct(product *models.ShopProduct) error {
	return database.Database().Create(product).Error
}

func UpdateShopProduct(product *models.ShopProduct) error {
	return database.Database().Save(product).Error
}
