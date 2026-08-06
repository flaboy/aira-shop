package shopify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

func TestShopifyProductSetUsesGraphQL202607AndParsesCost(t *testing.T) {
	doer := recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "https://verified-shop.myshopify.com/admin/api/2026-07/graphql.json", request.URL.String())
		require.Equal(t, "access-token", request.Header.Get("X-Shopify-Access-Token"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.Unmarshal(body, &payload))
		if !strings.Contains(payload.Query, "productSet") {
			t.Fatal("GraphQL 请求必须调用 productSet")
		}
		require.Equal(t, true, payload.Variables["synchronous"])
		return jsonResponse(http.StatusOK, `{
  "data":{"productSet":{"product":{"id":"gid://shopify/Product/81","legacyResourceId":"81","title":"GraphQL product","descriptionHtml":"<p>Body</p>","status":"ACTIVE","tags":["one"],"options":[{"name":"Title","position":1,"values":["Default Title"]}],"variants":{"nodes":[{"id":"gid://shopify/ProductVariant/91","legacyResourceId":"91","title":"Default Title","sku":"SKU-1","price":"19.00","compareAtPrice":null,"selectedOptions":[{"name":"Title","value":"Default Title"}]}]},"metafields":{"nodes":[{"id":"gid://shopify/Metafield/1","namespace":"effiprint","key":"publish_operation_id","type":"single_line_text_field","value":"operation-1"}]}},"userErrors":[]}},
  "extensions":{"cost":{"requestedQueryCost":14,"actualQueryCost":12,"throttleStatus":{"maximumAvailable":2000,"currentlyAvailable":1988,"restoreRate":100}}}
}`), nil
	})
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", doer)
	require.NoError(t, err)

	product, metafields, cost, err := client.setProduct(context.Background(), 0, map[string]any{"title": "GraphQL product"})
	require.NoError(t, err)
	require.Equal(t, uint64(81), product.Id)
	require.Equal(t, uint64(91), product.Variants[0].Id)
	require.Equal(t, "operation-1", metafields[0].Value)
	require.Equal(t, 12, cost.ActualQueryCost)
	require.Equal(t, float64(1988), cost.ThrottleStatus.CurrentlyAvailable)
}

func TestShopifyProductSelectiveVariantUpdatePreservesMerchantVariant(t *testing.T) {
	price := decimal.NewFromInt(25)
	product := &types.ProductData{
		UpdateFields: []string{types.ProductUpdateFieldVariantsPrices},
		Variants:     []types.ProductVariant{{ID: 701, Price: &price, Option1: "M"}},
	}
	converted, err := (&Shopify{}).toShopifyProduct(product)
	require.NoError(t, err)
	remote := goshopify.Product{
		Options: []goshopify.ProductOption{{Name: "Size", Values: []string{"M", "Merchant"}}},
		Variants: []goshopify.Variant{
			{Id: 901, Option1: "M"},
			{Id: 999, Option1: "Merchant"},
		},
	}
	input, err := shopifyProductSelectiveSetInput(product, converted, remote, []models.ShopifyVariantMapping{{LocalVariantID: 701, ShopifyVariantID: 901}})
	require.NoError(t, err)
	variants := input["variants"].([]map[string]any)
	if len(variants) != 2 {
		t.Fatalf("商家额外 Variant 必须保留，实际数量为 %d", len(variants))
	}
	require.Equal(t, "25", variants[0]["price"])
	require.Equal(t, shopifyVariantGID(999), variants[1]["id"])
	_, merchantPriceChanged := variants[1]["price"]
	if merchantPriceChanged {
		t.Fatal("商家额外 Variant 的价格不得被 EffiPrint 覆盖")
	}
}

func TestApplyShopifyProductSetVariantIDsPreservesRemoteIdentity(t *testing.T) {
	input := map[string]any{"variants": []map[string]any{{"sku": "SKU-1"}, {"sku": "SKU-2"}}}
	local := []types.ProductVariant{{ID: 701}, {ID: 702}}
	mappings := []models.ShopifyVariantMapping{
		{LocalVariantID: 701, ShopifyVariantID: 901},
		{LocalVariantID: 702, ShopifyVariantID: 902},
	}
	require.NoError(t, applyShopifyProductSetVariantIDs(input, local, mappings))
	variants := input["variants"].([]map[string]any)
	require.Equal(t, shopifyVariantGID(901), variants[0]["id"])
	require.Equal(t, shopifyVariantGID(902), variants[1]["id"])
}

func TestShopifyVariantSetInputUsesInventoryMeasurementForWeight(t *testing.T) {
	weight := decimal.NewFromFloat(0.45)
	input, err := shopifyVariantSetInput(types.ProductVariant{ID: 701, Weight: &weight, WeightUnit: "kg"}, []string{"Title"})
	require.NoError(t, err)
	inventoryItem := input["inventoryItem"].(map[string]any)
	measurement := inventoryItem["measurement"].(map[string]any)
	weightInput := measurement["weight"].(map[string]any)
	require.Equal(t, "KILOGRAMS", weightInput["unit"])
	require.Equal(t, 0.45, weightInput["value"])
}

func TestShopifyProductGraphQLRejectsUserErrorsWithoutReturningPayload(t *testing.T) {
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"productSet":{"product":null,"userErrors":[{"field":["input","title"],"message":"Title is required","code":"INVALID"}]}}}`))}, nil
	}))
	require.NoError(t, err)

	_, _, _, err = client.setProduct(context.Background(), 0, map[string]any{})
	require.Error(t, err)
	if !strings.Contains(err.Error(), "Title is required") {
		t.Fatalf("必须返回 Shopify user error，实际为 %v", err)
	}
}
