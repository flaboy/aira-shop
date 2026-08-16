package shopify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/flaboy/aira-shop/pkg/types"
)

func TestShopifyFulfillmentUsesGraphQL202607AndNotifiesCustomer(t *testing.T) {
	requestCount := 0
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		requestCount++
		require.Equal(t, "https://verified-shop.myshopify.com/admin/api/2026-07/graphql.json", request.URL.String())
		require.Equal(t, "access-token", request.Header.Get("X-Shopify-Access-Token"))
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		if requestCount == 1 {
			if !strings.Contains(payload.Query, "fulfillmentOrders") {
				t.Fatal("GraphQL 请求必须查询 fulfillmentOrders")
			}
			require.Equal(t, "gid://shopify/Order/1001", payload.Variables["id"])
			return jsonResponse(http.StatusOK, `{"data":{"order":{"fulfillments":[],"fulfillmentOrders":{"nodes":[{"id":"gid://shopify/FulfillmentOrder/81","status":"OPEN"}],"pageInfo":{"hasNextPage":false}}}}}`), nil
		}
		if !strings.Contains(payload.Query, "fulfillmentCreate") {
			t.Fatal("GraphQL 请求必须调用 fulfillmentCreate")
		}
		input := payload.Variables["fulfillment"].(map[string]any)
		require.Equal(t, true, input["notifyCustomer"])
		tracking := input["trackingInfo"].(map[string]any)
		require.Equal(t, "UPS", tracking["company"])
		require.Equal(t, "1Z999", tracking["number"])
		fulfillmentOrders := input["lineItemsByFulfillmentOrder"].([]any)
		require.Equal(t, "gid://shopify/FulfillmentOrder/81", fulfillmentOrders[0].(map[string]any)["fulfillmentOrderId"])
		return jsonResponse(http.StatusOK, `{"data":{"fulfillmentCreate":{"fulfillment":{"id":"gid://shopify/Fulfillment/91","status":"SUCCESS","trackingInfo":[{"number":"1Z999","company":"UPS","url":null}]},"userErrors":[]}}}`), nil
	}))
	require.NoError(t, err)

	result, err := client.syncFulfillment(context.Background(), types.FulfillmentData{
		OrderID: "1001", Carrier: "UPS", TrackingNumber: "1Z999", NotifyCustomer: true,
	})
	require.NoError(t, err)
	require.Equal(t, "gid://shopify/Fulfillment/91", result.ID)
	require.Equal(t, 2, requestCount)
}

func TestShopifyFulfillmentOmitsEmptyCarrier(t *testing.T) {
	requestCount := 0
	platform := &Shopify{httpClient: &http.Client{Transport: orderPullRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount == 1 {
			return jsonResponse(http.StatusOK, `{"data":{"order":{"fulfillments":[],"fulfillmentOrders":{"nodes":[{"id":"gid://shopify/FulfillmentOrder/81","status":"OPEN"}],"pageInfo":{"hasNextPage":false}}}}}`), nil
		}
		var payload struct {
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		tracking := payload.Variables["fulfillment"].(map[string]any)["trackingInfo"].(map[string]any)
		require.Equal(t, "1Z999", tracking["number"])
		_, hasCompany := tracking["company"]
		if hasCompany {
			t.Fatal("空承运商不应写入 Shopify trackingInfo.company")
		}
		return jsonResponse(http.StatusOK, `{"data":{"fulfillmentCreate":{"fulfillment":{"id":"gid://shopify/Fulfillment/91"},"userErrors":[]}}}`), nil
	})}}

	result, err := platform.SyncFulfillment(context.Background(), &types.ShopCredential{Data: map[string]any{
		"Url": "verified-shop.myshopify.com", "AccessToken": "access-token",
	}}, types.FulfillmentData{OrderID: "1001", TrackingNumber: "1Z999"})
	require.NoError(t, err)
	require.Equal(t, "gid://shopify/Fulfillment/91", result.ID)
}

func TestShopifyFulfillmentConvergesFromExistingTrackingNumber(t *testing.T) {
	requestCount := 0
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
		requestCount++
		return jsonResponse(http.StatusOK, `{"data":{"order":{"fulfillments":[{"id":"gid://shopify/Fulfillment/91","status":"SUCCESS","trackingInfo":[{"number":"1Z999","company":"UPS","url":null}]}],"fulfillmentOrders":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`), nil
	}))
	require.NoError(t, err)

	result, err := client.syncFulfillment(context.Background(), types.FulfillmentData{OrderID: "1001", Carrier: "UPS", TrackingNumber: "1Z999"})
	require.NoError(t, err)
	require.Equal(t, "gid://shopify/Fulfillment/91", result.ID)
	require.Equal(t, 1, requestCount)
}

func TestShopifyFulfillmentRejectsMultipleOpenFulfillmentOrders(t *testing.T) {
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"data":{"order":{"fulfillments":[],"fulfillmentOrders":{"nodes":[{"id":"gid://shopify/FulfillmentOrder/81","status":"OPEN"},{"id":"gid://shopify/FulfillmentOrder/82","status":"OPEN"}],"pageInfo":{"hasNextPage":false}}}}}`), nil
	}))
	require.NoError(t, err)

	_, err = client.syncFulfillment(context.Background(), types.FulfillmentData{OrderID: "1001", Carrier: "UPS", TrackingNumber: "1Z999"})
	require.Error(t, err)
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("错误必须说明 fulfillment order 唯一性：%v", err)
	}
}

func TestShopifyFulfillmentRejectsGraphQLUserErrors(t *testing.T) {
	responses := []string{
		`{"data":{"order":{"fulfillments":[],"fulfillmentOrders":{"nodes":[{"id":"gid://shopify/FulfillmentOrder/81","status":"OPEN"}],"pageInfo":{"hasNextPage":false}}}}}`,
		`{"data":{"fulfillmentCreate":{"fulfillment":null,"userErrors":[{"field":["fulfillment"],"message":"Permission denied"}]}}}`,
	}
	client, err := newShopifyGraphQLClient("verified-shop.myshopify.com", "access-token", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
		response := responses[0]
		responses = responses[1:]
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	require.NoError(t, err)

	_, err = client.syncFulfillment(context.Background(), types.FulfillmentData{OrderID: "1001", Carrier: "UPS", TrackingNumber: "1Z999"})
	require.Error(t, err)
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("错误必须保留 Shopify userError：%v", err)
	}
}
