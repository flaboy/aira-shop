package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/flaboy/aira-shop/pkg/types"
)

const (
	shopifyFulfillmentLookupLimit = 50
	shopifyFulfillmentTimeout     = 30 * time.Second
)

type shopifyFulfillmentContext struct {
	Order *shopifyFulfillmentOrderContext `json:"order"`
}

type shopifyFulfillmentOrderContext struct {
	Fulfillments      []shopifyFulfillmentNode `json:"fulfillments"`
	FulfillmentOrders struct {
		Nodes    []shopifyFulfillmentOrderNode `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	} `json:"fulfillmentOrders"`
}

type shopifyFulfillmentNode struct {
	ID           string                `json:"id"`
	Status       string                `json:"status"`
	TrackingInfo []shopifyTrackingInfo `json:"trackingInfo"`
}

type shopifyTrackingInfo struct {
	Number  string `json:"number"`
	Company string `json:"company"`
	URL     string `json:"url"`
}

type shopifyFulfillmentOrderNode struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (p *Shopify) SyncFulfillment(ctx context.Context, credential *types.ShopCredential, fulfillment types.FulfillmentData) (*types.FulfillmentResult, error) {
	if credential == nil || fulfillment.OrderID == "" || fulfillment.Carrier == "" || fulfillment.TrackingNumber == "" {
		return nil, fmt.Errorf("Shopify fulfillment input is incomplete")
	}
	credentialData, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, err
	}
	shopifyCredential := ShopifyCredential{}
	if err := json.Unmarshal(credentialData, &shopifyCredential); err != nil {
		return nil, err
	}
	client, err := newShopifyGraphQLClient(shopifyCredential.Url, shopifyCredential.AccessToken, p.httpClient)
	if err != nil {
		return nil, err
	}
	requestContext, cancel := context.WithTimeout(ctx, shopifyFulfillmentTimeout)
	defer cancel()
	return client.syncFulfillment(requestContext, fulfillment)
}

func (c *shopifyGraphQLClient) syncFulfillment(ctx context.Context, fulfillment types.FulfillmentData) (*types.FulfillmentResult, error) {
	orderID, err := strconv.ParseUint(fulfillment.OrderID, 10, 64)
	if err != nil || orderID == 0 {
		return nil, fmt.Errorf("Shopify order ID is invalid")
	}
	orderContext, err := c.fulfillmentContext(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if orderContext.Order == nil {
		return nil, fmt.Errorf("Shopify order was not found")
	}
	if existingID := matchingShopifyFulfillmentID(*orderContext.Order, fulfillment.TrackingNumber); existingID != "" {
		return &types.FulfillmentResult{ID: existingID}, nil
	}
	if orderContext.Order.FulfillmentOrders.PageInfo.HasNextPage {
		return nil, fmt.Errorf("Shopify order has more than %d fulfillment orders", shopifyFulfillmentLookupLimit)
	}
	fulfillmentOrderIDs := openShopifyFulfillmentOrderIDs(orderContext.Order.FulfillmentOrders.Nodes)
	if len(fulfillmentOrderIDs) != 1 {
		return nil, fmt.Errorf("Shopify order must have exactly one fulfillable fulfillment order")
	}
	return c.createFulfillment(ctx, fulfillmentOrderIDs[0], fulfillment)
}

func (c *shopifyGraphQLClient) fulfillmentContext(ctx context.Context, orderID uint64) (shopifyFulfillmentContext, error) {
	query := `query EffiPrintFulfillmentContext($id: ID!, $limit: Int!) {
  order(id: $id) {
    fulfillments(first: $limit) { id status trackingInfo { number company url } }
    fulfillmentOrders(first: $limit) {
      nodes { id status }
      pageInfo { hasNextPage }
    }
  }
}`
	data := shopifyFulfillmentContext{}
	_, err := c.execute(ctx, "fulfillmentContext", query, map[string]any{
		"id": shopifyOrderGID(orderID), "limit": shopifyFulfillmentLookupLimit,
	}, &data)
	return data, err
}

func (c *shopifyGraphQLClient) createFulfillment(ctx context.Context, fulfillmentOrderID string, fulfillment types.FulfillmentData) (*types.FulfillmentResult, error) {
	query := `mutation EffiPrintFulfillmentCreate($fulfillment: FulfillmentInput!) {
  fulfillmentCreate(fulfillment: $fulfillment) {
    fulfillment { id status trackingInfo { number company url } }
    userErrors { field message }
  }
}`
	trackingInfo := map[string]any{"company": fulfillment.Carrier, "number": fulfillment.TrackingNumber}
	if fulfillment.TrackingURL != "" {
		trackingInfo["url"] = fulfillment.TrackingURL
	}
	input := map[string]any{
		"lineItemsByFulfillmentOrder": []map[string]any{{"fulfillmentOrderId": fulfillmentOrderID}},
		"notifyCustomer":              fulfillment.NotifyCustomer,
		"trackingInfo":                trackingInfo,
	}
	var data struct {
		FulfillmentCreate struct {
			Fulfillment *struct {
				ID string `json:"id"`
			} `json:"fulfillment"`
			UserErrors []shopifyGraphQLUserError `json:"userErrors"`
		} `json:"fulfillmentCreate"`
	}
	if _, err := c.execute(ctx, "fulfillmentCreate", query, map[string]any{"fulfillment": input}, &data); err != nil {
		return nil, err
	}
	if err := shopifyUserErrors("fulfillmentCreate", data.FulfillmentCreate.UserErrors); err != nil {
		return nil, err
	}
	if data.FulfillmentCreate.Fulfillment == nil || data.FulfillmentCreate.Fulfillment.ID == "" {
		return nil, fmt.Errorf("Shopify fulfillmentCreate returned no fulfillment")
	}
	return &types.FulfillmentResult{ID: data.FulfillmentCreate.Fulfillment.ID}, nil
}

func matchingShopifyFulfillmentID(order shopifyFulfillmentOrderContext, trackingNumber string) string {
	for _, fulfillment := range order.Fulfillments {
		if strings.EqualFold(fulfillment.Status, "CANCELLED") {
			continue
		}
		for _, tracking := range fulfillment.TrackingInfo {
			if tracking.Number == trackingNumber {
				return fulfillment.ID
			}
		}
	}
	return ""
}

func openShopifyFulfillmentOrderIDs(nodes []shopifyFulfillmentOrderNode) []string {
	result := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.ID != "" && (node.Status == "OPEN" || node.Status == "IN_PROGRESS") {
			result = append(result, node.ID)
		}
	}
	return result
}

func shopifyOrderGID(orderID uint64) string {
	return "gid://shopify/Order/" + strconv.FormatUint(orderID, 10)
}
