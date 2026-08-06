package shopify

import (
	"os"
	"strings"
	"testing"

	"github.com/flaboy/aira-shop/pkg/types"
)

func TestPutProductUsesConfiguredHTTPClient(t *testing.T) {
	content, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	clientCall := "newShopifyGraphQLClient(creds.Url, creds.AccessToken, p.httpClient)"
	if !strings.Contains(string(content), clientCall) {
		t.Fatal("商品发布必须使用 Shopify GraphQL 适配器统一配置的 HTTP Client")
	}
}

func TestToShopifyProductAddsSizeGuideToDescription(t *testing.T) {
	product, err := (&Shopify{}).toShopifyProduct(&types.ProductData{
		ProductName:      "Size Guide Product",
		BodyHTML:         "<p>Description</p>",
		SizeGuideEnabled: true,
		SizeGuideHTML:    "<table><tr><td>M</td></tr></table>",
	})
	if err != nil {
		t.Fatal(err)
	}

	if product.BodyHTML != "<p>Description</p><table><tr><td>M</td></tr></table>" {
		t.Fatalf("Size Guide 必须追加到商品 Description，实际为 %s", product.BodyHTML)
	}
	if len(product.Metafields) != 0 {
		t.Fatalf("Size Guide 不得再生成独立 metafield，实际为 %d 个", len(product.Metafields))
	}
}

func TestToShopifyProductDoesNotAddDisabledSizeGuide(t *testing.T) {
	product, err := (&Shopify{}).toShopifyProduct(&types.ProductData{
		BodyHTML:         "<p>Description</p>",
		SizeGuideEnabled: false,
		SizeGuideHTML:    "<table><tr><td>M</td></tr></table>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if product.BodyHTML != "<p>Description</p>" {
		t.Fatalf("未启用 Size Guide 时不得修改 Description，实际为 %s", product.BodyHTML)
	}
}

func TestToShopifyProductAddsStablePublishIdentity(t *testing.T) {
	product, err := (&Shopify{}).toShopifyProduct(&types.ProductData{
		ProductName:        "Identity Product",
		OriginProductID:    "17",
		PublishOperationID: "9f83c838-e0af-45c9-a406-3d69079fda95",
		ExternalShopID:     "69754355755",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"origin_product_id":    "17",
		"publish_operation_id": "9f83c838-e0af-45c9-a406-3d69079fda95",
		"external_shop_id":     "69754355755",
	}
	if len(product.Metafields) != len(want) {
		t.Fatalf("期望生成 %d 个发布身份 metafield，实际为 %d 个", len(want), len(product.Metafields))
	}
	for _, metafield := range product.Metafields {
		if metafield.Namespace != "effiprint" {
			t.Fatalf("发布身份 namespace 必须为 effiprint，实际为 %s", metafield.Namespace)
		}
		if want[metafield.Key] != metafield.Value {
			t.Fatalf("发布身份 %s 不匹配，实际为 %v", metafield.Key, metafield.Value)
		}
	}
}
