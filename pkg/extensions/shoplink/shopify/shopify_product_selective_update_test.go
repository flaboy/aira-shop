package shopify

import (
	"testing"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

func TestSelectShopifyProductUpdateOnlyIncludesSelectedFields(t *testing.T) {
	price := decimal.NewFromInt(19)
	product := goshopify.Product{
		Id:       81,
		Title:    "Source title",
		BodyHTML: "<p>Source description</p>",
		Images:   []goshopify.Image{{Src: "https://example.com/image.png"}},
		Options:  []goshopify.ProductOption{{Name: "Size", Values: []string{"M"}}},
		Variants: []goshopify.Variant{{Sku: "SKU-1", Price: &price}},
		Tags:     "must-not-change",
	}

	selected := selectShopifyProductUpdate(product, productUpdateFieldSet([]string{
		types.ProductUpdateFieldTitle,
		types.ProductUpdateFieldImages,
	}))

	if selected.Title != product.Title || len(selected.Images) != 1 {
		t.Fatalf("选择字段未进入 Shopify 更新请求: %+v", selected)
	}
	if selected.BodyHTML != "" || len(selected.Options) != 0 || len(selected.Variants) != 0 || selected.Tags != "" {
		t.Fatalf("未选择字段不得进入 Shopify 更新请求: %+v", selected)
	}
}

func TestApplyShopifyVariantMappingsUsesPersistedIdentity(t *testing.T) {
	source := []goshopify.Variant{{
		Sku: "SKU-1", Option1: "M",
		Metafields: []goshopify.Metafield{{Namespace: "aira-shop", Key: "origin"}},
	}}
	local := []types.ProductVariant{{ID: 701, Sku: "SKU-1", Option1: "M"}}
	mappings := []models.ShopifyVariantMapping{{LocalVariantID: 701, ShopifyVariantID: 901}}

	matched, err := applyShopifyVariantMappings(source, local, mappings, 81)
	if err != nil {
		t.Fatal(err)
	}

	if matched[0].Id != 901 || matched[0].ProductId != 81 {
		t.Fatalf("Shopify variant ID 映射失败: %+v", matched[0])
	}
	if len(matched[0].Metafields) != 0 {
		t.Fatalf("选择性更新不得重复创建 variant metafield: %+v", matched[0].Metafields)
	}
}

func TestApplyShopifyVariantMappingsRejectsMissingMapping(t *testing.T) {
	source := []goshopify.Variant{{Sku: "SKU-1"}}
	local := []types.ProductVariant{{ID: 701, Sku: "SKU-1"}}

	if _, err := applyShopifyVariantMappings(source, local, nil, 81); err == nil {
		t.Fatal("缺少持久化 Shopify Variant 映射时必须阻断更新")
	}
}

func TestValidateShopifyProductIdentityAcceptsEffiPrintPublication(t *testing.T) {
	product := &types.ProductData{
		OriginProductID:    "701",
		PublishOperationID: "e2e-publish-801",
		ExternalShopID:     "shop-901",
	}
	metafields := []goshopify.Metafield{
		{Namespace: "effiprint", Key: "origin_product_id", Value: "701"},
		{Namespace: "effiprint", Key: "publish_operation_id", Value: "e2e-publish-801"},
		{Namespace: "effiprint", Key: "external_shop_id", Value: "shop-901"},
		{Namespace: "merchant", Key: "custom", Value: "preserve"},
	}

	if err := validateShopifyProductIdentity(metafields, product); err != nil {
		t.Fatalf("EffiPrint 商品身份应通过校验: %v", err)
	}
}

func TestValidateShopifyProductIdentityRejectsNativeShopifyProduct(t *testing.T) {
	product := &types.ProductData{
		OriginProductID:    "701",
		PublishOperationID: "e2e-publish-801",
		ExternalShopID:     "shop-901",
	}

	if err := validateShopifyProductIdentity(nil, product); err == nil {
		t.Fatal("缺少 EffiPrint Metafield 的 Shopify 原生商品必须拒绝同步")
	}
}

func TestValidateShopifyProductIdentityRejectsAnotherPublication(t *testing.T) {
	product := &types.ProductData{
		OriginProductID:    "701",
		PublishOperationID: "e2e-publish-801",
		ExternalShopID:     "shop-901",
	}
	metafields := []goshopify.Metafield{
		{Namespace: "effiprint", Key: "origin_product_id", Value: "701"},
		{Namespace: "effiprint", Key: "publish_operation_id", Value: "another-publication"},
		{Namespace: "effiprint", Key: "external_shop_id", Value: "shop-901"},
	}

	if err := validateShopifyProductIdentity(metafields, product); err == nil {
		t.Fatal("其他发布记录的 Shopify 商品必须拒绝同步")
	}
}

func TestValidateShopifyVariantMappingsPreservesMerchantExtraVariants(t *testing.T) {
	remote := []goshopify.Variant{{Id: 901}, {Id: 999}}
	local := []types.ProductVariant{{ID: 701}}
	mappings := []models.ShopifyVariantMapping{{LocalVariantID: 701, ShopifyVariantID: 901}}

	if err := validateShopifyVariantMappings(remote, local, mappings); err != nil {
		t.Fatalf("Keep 模式应保留商家额外 Variant: %v", err)
	}
}

func TestValidateShopifyVariantMappingsRejectsMissingRemoteVariant(t *testing.T) {
	remote := []goshopify.Variant{{Id: 999}}
	local := []types.ProductVariant{{ID: 701}}
	mappings := []models.ShopifyVariantMapping{{LocalVariantID: 701, ShopifyVariantID: 901}}

	if err := validateShopifyVariantMappings(remote, local, mappings); err == nil {
		t.Fatal("EffiPrint Variant 已从 Shopify 删除时必须报告映射失败")
	}
}
