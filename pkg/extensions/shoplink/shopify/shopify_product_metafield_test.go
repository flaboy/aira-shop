package shopify

import (
	"testing"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/types"
)

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
