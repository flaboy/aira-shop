package shopify

import (
	"os"
	"strings"
	"testing"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/types"
)

func TestPutProductUsesConfiguredHTTPClient(t *testing.T) {
	content, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	clientCall := "shopify.NewClient(*app, creds.Url, creds.AccessToken, shopify.WithHTTPClient(p.httpClient))"
	if !strings.Contains(string(content), clientCall) {
		t.Fatal("商品发布必须使用 Shopify 适配器统一配置的 HTTP Client")
	}
}

func TestToShopifyProductAddsSizeGuideMetafield(t *testing.T) {
	product, err := (&Shopify{}).toShopifyProduct(&types.ProductData{
		ProductName:      "Size Guide Product",
		BodyHTML:         "<p>Description</p>",
		SizeGuideEnabled: true,
		SizeGuideHTML:    "<table><tr><td>M</td></tr></table>",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(product.Metafields) != 1 {
		t.Fatalf("期望生成 1 个商品 metafield，实际为 %d 个", len(product.Metafields))
	}

	metafield := product.Metafields[0]
	if metafield.Namespace != sizeGuideMetafieldNamespace {
		t.Fatalf("期望 namespace 为 %s，实际为 %s", sizeGuideMetafieldNamespace, metafield.Namespace)
	}
	if metafield.Key != sizeGuideMetafieldKey {
		t.Fatalf("期望 key 为 %s，实际为 %s", sizeGuideMetafieldKey, metafield.Key)
	}
	if metafield.Type != goshopify.MetafieldTypeMultiLineTextField {
		t.Fatalf("期望 metafield 类型为 multi_line_text_field，实际为 %s", metafield.Type)
	}
	if metafield.Value != "<table><tr><td>M</td></tr></table>" {
		t.Fatalf("期望写入 size guide html，实际为 %v", metafield.Value)
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
