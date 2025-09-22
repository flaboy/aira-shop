package utils

import (
	"encoding/json"

	"github.com/flaboy/aira/aira-core/pkg/hashid"
	"github.com/flaboy/aira/aira-web/pkg/helper"
)

var (
	HashIDTypeShop        = hashid.NewType("shop-", "shop", 6)
	HashIDTypeShopProduct = hashid.NewType("sp-", "shop_product", 6)
)

func DecodeShopHashID(hashID string) (uint, error) {
	return hashid.Decode(HashIDTypeShop, hashID)
}

func EncodeShopID(id uint) string {
	return hashid.Encode(HashIDTypeShop, id)
}

func DecodeShopProductHashID(hashID string) (uint, error) {
	return hashid.Decode(HashIDTypeShopProduct, hashID)
}

func EncodeShopProductID(id uint) string {
	return hashid.Encode(HashIDTypeShopProduct, id)
}

func GetConnectUrl(platform, path string) string {
	return helper.BuildUrl("/connect/" + platform + "/" + path)
}

func SerializeCredential(cred interface{}) ([]byte, error) {
	return json.Marshal(cred)
}

func DeserializeCredential(data []byte, target interface{}) error {
	return json.Unmarshal(data, target)
}
