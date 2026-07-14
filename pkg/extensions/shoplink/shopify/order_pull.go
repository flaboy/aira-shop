package shopify

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

const shopifyPaidOrdersQuery = `query PullPaidOrders($first: Int!, $after: String, $query: String!) {
  orders(first: $first, after: $after, query: $query, sortKey: CREATED_AT) {
    edges {
      cursor
      node {
        legacyResourceId
        name
        email
        phone
        displayFinancialStatus
        displayFulfillmentStatus
        createdAt
        updatedAt
        currentTotalPriceSet { shopMoney { amount currencyCode } }
        currentSubtotalPriceSet { shopMoney { amount currencyCode } }
        currentTotalTaxSet { shopMoney { amount currencyCode } }
        totalShippingPriceSet { shopMoney { amount currencyCode } }
        shippingAddress { firstName lastName address1 address2 city province provinceCode country countryCodeV2 zip phone company }
        billingAddress { firstName lastName address1 address2 city province provinceCode country countryCodeV2 zip phone company }
        lineItems(first: 250) {
          nodes {
            id
            name
            sku
            quantity
            variantTitle
            originalUnitPriceSet { shopMoney { amount currencyCode } }
            product { legacyResourceId }
            variant { legacyResourceId }
            customAttributes { key value }
          }
          pageInfo { hasNextPage }
        }
        shippingLines(first: 50) {
          nodes { code title source originalPriceSet { shopMoney { amount currencyCode } } }
          pageInfo { hasNextPage }
        }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

const (
	shopifyOrderPullAPIVersion = "2026-04"
	shopifyPaidOrdersPageSize  = 1
	shopifyLineItemGIDPrefix   = "gid://shopify/LineItem/"
)

type orderPullGraphQL interface {
	Query(context.Context, string, interface{}, interface{}) error
}

type shopifyOrdersResponse struct {
	Orders shopifyOrderConnection `json:"orders"`
}

type shopifyOrderConnection struct {
	Edges    []shopifyOrderEdge `json:"edges"`
	PageInfo shopifyPageInfo    `json:"pageInfo"`
}

type shopifyOrderEdge struct {
	Cursor string           `json:"cursor"`
	Node   shopifyOrderNode `json:"node"`
}

type shopifyPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type shopifyMoney struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

type shopifyMoneySet struct {
	ShopMoney shopifyMoney `json:"shopMoney"`
}

type shopifyOrderAddress struct {
	FirstName    string `json:"firstName"`
	LastName     string `json:"lastName"`
	Address1     string `json:"address1"`
	Address2     string `json:"address2"`
	City         string `json:"city"`
	Province     string `json:"province"`
	ProvinceCode string `json:"provinceCode"`
	Country      string `json:"country"`
	CountryCode  string `json:"countryCodeV2"`
	Zip          string `json:"zip"`
	Phone        string `json:"phone"`
	Company      string `json:"company"`
}

type shopifyOrderLineNode struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	SKU                  string                   `json:"sku"`
	Quantity             int                      `json:"quantity"`
	VariantTitle         string                   `json:"variantTitle"`
	OriginalUnitPriceSet shopifyMoneySet          `json:"originalUnitPriceSet"`
	Product              shopifyOrderResource     `json:"product"`
	Variant              shopifyOrderResource     `json:"variant"`
	CustomAttributes     []shopifyCustomAttribute `json:"customAttributes"`
}

type shopifyOrderResource struct {
	LegacyResourceID string `json:"legacyResourceId"`
}

type shopifyCustomAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type shopifyShippingLineNode struct {
	Code             string          `json:"code"`
	Title            string          `json:"title"`
	Source           string          `json:"source"`
	OriginalPriceSet shopifyMoneySet `json:"originalPriceSet"`
}

type shopifyOrderNode struct {
	LegacyResourceID         string               `json:"legacyResourceId"`
	Name                     string               `json:"name"`
	Email                    string               `json:"email"`
	Phone                    string               `json:"phone"`
	DisplayFinancialStatus   string               `json:"displayFinancialStatus"`
	DisplayFulfillmentStatus string               `json:"displayFulfillmentStatus"`
	CreatedAt                time.Time            `json:"createdAt"`
	UpdatedAt                time.Time            `json:"updatedAt"`
	CurrentTotalPriceSet     shopifyMoneySet      `json:"currentTotalPriceSet"`
	CurrentSubtotalPriceSet  shopifyMoneySet      `json:"currentSubtotalPriceSet"`
	CurrentTotalTaxSet       shopifyMoneySet      `json:"currentTotalTaxSet"`
	TotalShippingPriceSet    shopifyMoneySet      `json:"totalShippingPriceSet"`
	ShippingAddress          *shopifyOrderAddress `json:"shippingAddress"`
	BillingAddress           *shopifyOrderAddress `json:"billingAddress"`
	LineItems                struct {
		Nodes    []shopifyOrderLineNode `json:"nodes"`
		PageInfo shopifyPageInfo        `json:"pageInfo"`
	} `json:"lineItems"`
	ShippingLines struct {
		Nodes    []shopifyShippingLineNode `json:"nodes"`
		PageInfo shopifyPageInfo           `json:"pageInfo"`
	} `json:"shippingLines"`
}

func shopifyOrderSearchQuery(request types.OrderPullRequest) string {
	return fmt.Sprintf("financial_status:paid created_at:>='%s' created_at:<='%s'", request.Since.UTC().Format(time.RFC3339), request.Until.UTC().Format(time.RFC3339))
}

func fetchShopifyPaidOrderNodes(ctx context.Context, graphQL orderPullGraphQL, request types.OrderPullRequest) ([]shopifyOrderNode, error) {
	nodes := []shopifyOrderNode{}
	var after any
	for {
		response := shopifyOrdersResponse{}
		// 每页只读取一个订单，避免 lineItems 与 shippingLines 嵌套连接放大 GraphQL 请求成本。
		variables := map[string]any{"first": shopifyPaidOrdersPageSize, "after": after, "query": shopifyOrderSearchQuery(request)}
		if err := graphQL.Query(ctx, shopifyPaidOrdersQuery, variables, &response); err != nil {
			return nil, err
		}
		for _, edge := range response.Orders.Edges {
			nodes = append(nodes, edge.Node)
		}
		if !response.Orders.PageInfo.HasNextPage {
			return nodes, nil
		}
		if response.Orders.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("Shopify orders page is missing end cursor")
		}
		after = response.Orders.PageInfo.EndCursor
	}
}

func (p *Shopify) PullOrders(ctx context.Context, credential *types.ShopCredential, request types.OrderPullRequest) (*types.OrderPullResult, error) {
	credentialJSON, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, err
	}
	creds := ShopifyCredential{}
	if err := json.Unmarshal(credentialJSON, &creds); err != nil {
		return nil, err
	}
	client, err := goshopify.NewClient(*app, creds.Url, creds.AccessToken, goshopify.WithVersion(shopifyOrderPullAPIVersion), goshopify.WithHTTPClient(p.httpClient))
	if err != nil {
		return nil, err
	}
	nodes, err := fetchShopifyPaidOrderNodes(ctx, client.GraphQL, request)
	if err != nil {
		return nil, err
	}
	result := &types.OrderPullResult{Orders: []types.OrderData{}, Failures: []types.OrderPullFailure{}}
	for _, node := range nodes {
		// GraphQL 查询已限制 Paid，这里再次校验响应，避免非 Paid 数据进入本地订单链路。
		if !strings.EqualFold(node.DisplayFinancialStatus, "PAID") {
			continue
		}
		result.Total++
		orderData, convertErr := convertShopifyGraphQLOrder(node, request.ShopLinkID)
		if convertErr != nil {
			result.Failures = append(result.Failures, types.OrderPullFailure{OrderID: node.LegacyResourceID, OrderName: node.Name, Reason: convertErr.Error()})
			continue
		}
		result.Orders = append(result.Orders, orderData)
	}
	return result, nil
}

func convertShopifyGraphQLOrder(node shopifyOrderNode, shopLinkID uint) (types.OrderData, error) {
	if node.LegacyResourceID == "" {
		return types.OrderData{}, fmt.Errorf("Shopify order is missing legacy resource ID")
	}
	if node.LineItems.PageInfo.HasNextPage {
		return types.OrderData{}, fmt.Errorf("Shopify order %s has more than 250 line items", node.Name)
	}
	if node.ShippingLines.PageInfo.HasNextPage {
		return types.OrderData{}, fmt.Errorf("Shopify order %s has more than 50 shipping lines", node.Name)
	}
	totalPrice, err := shopifyMoneyDecimal(node.CurrentTotalPriceSet.ShopMoney)
	if err != nil {
		return types.OrderData{}, err
	}
	subtotalPrice, err := shopifyMoneyDecimal(node.CurrentSubtotalPriceSet.ShopMoney)
	if err != nil {
		return types.OrderData{}, err
	}
	totalTax, err := shopifyMoneyDecimal(node.CurrentTotalTaxSet.ShopMoney)
	if err != nil {
		return types.OrderData{}, err
	}
	totalShipping, err := shopifyMoneyDecimal(node.TotalShippingPriceSet.ShopMoney)
	if err != nil {
		return types.OrderData{}, err
	}
	orderData := types.OrderData{
		ID: node.LegacyResourceID, Name: node.Name, Email: node.Email, Phone: node.Phone,
		FinancialStatus:   types.OrderFinancialStatusPaid,
		FulfillmentStatus: shopifyFulfillmentStatus(node.DisplayFulfillmentStatus),
		CreatedAt:         &node.CreatedAt, UpdatedAt: &node.UpdatedAt,
		TotalPrice: totalPrice, SubtotalPrice: subtotalPrice, TotalTax: totalTax, TotalShipping: totalShipping,
		Currency: node.CurrentTotalPriceSet.ShopMoney.CurrencyCode,
		RawData:  map[string]interface{}{"source_name": "shopify", "pull_source": "manual_pull"},
	}
	orderData.ShippingAddress = shopifyGraphQLAddress(node.ShippingAddress)
	orderData.BillingAddress = shopifyGraphQLAddress(node.BillingAddress)
	for _, line := range node.LineItems.Nodes {
		lineItemID, err := shopifyLineItemLegacyID(line.ID)
		if err != nil {
			return types.OrderData{}, err
		}
		if line.Product.LegacyResourceID == "" || line.Variant.LegacyResourceID == "" {
			return types.OrderData{}, fmt.Errorf("Shopify order %s contains a removed product or variant", node.Name)
		}
		product, ok, err := utils.GetShopProduct("shopify", line.Product.LegacyResourceID)
		if err != nil {
			return types.OrderData{}, err
		}
		if !ok || product.ShopID != shopLinkID {
			return types.OrderData{}, fmt.Errorf("shop product mapping not found for Shopify product %s", line.Product.LegacyResourceID)
		}
		remoteVariantID, err := strconv.ParseUint(line.Variant.LegacyResourceID, 10, 64)
		if err != nil {
			return types.OrderData{}, fmt.Errorf("invalid Shopify variant ID %s", line.Variant.LegacyResourceID)
		}
		mapping := models.ShopifyVariantMapping{}
		if err := database.Database().Where("shop_id = ? AND shop_product_id = ? AND shopify_variant_id = ?", shopLinkID, product.ID, remoteVariantID).First(&mapping).Error; err != nil {
			return types.OrderData{}, fmt.Errorf("variant mapping not found for Shopify variant %s: %w", line.Variant.LegacyResourceID, err)
		}
		price, err := shopifyMoneyDecimal(line.OriginalUnitPriceSet.ShopMoney)
		if err != nil {
			return types.OrderData{}, err
		}
		properties := map[string]string{}
		for _, property := range line.CustomAttributes {
			properties[property.Key] = property.Value
		}
		orderData.LineItems = append(orderData.LineItems, types.OrderLineItem{
			ID: lineItemID, ProductID: line.Product.LegacyResourceID, VariantID: mapping.LocalVariantID,
			Title: line.Name, SKU: line.SKU, Quantity: line.Quantity, Price: price, Properties: properties, VariantTitle: line.VariantTitle,
		})
	}
	for _, shipping := range node.ShippingLines.Nodes {
		price, err := shopifyMoneyDecimal(shipping.OriginalPriceSet.ShopMoney)
		if err != nil {
			return types.OrderData{}, err
		}
		orderData.ShippingLines = append(orderData.ShippingLines, types.OrderShippingLine{Code: shipping.Code, Title: shipping.Title, Price: price, Source: shipping.Source, Carrier: shipping.Source, CarrierID: shipping.Code})
	}
	return orderData, nil
}

func shopifyLineItemLegacyID(gid string) (string, error) {
	if !strings.HasPrefix(gid, shopifyLineItemGIDPrefix) {
		return "", fmt.Errorf("invalid Shopify line item GID %s", gid)
	}
	legacyID := strings.TrimPrefix(gid, shopifyLineItemGIDPrefix)
	if _, err := strconv.ParseUint(legacyID, 10, 64); err != nil {
		return "", fmt.Errorf("invalid Shopify line item GID %s", gid)
	}
	return legacyID, nil
}

func shopifyMoneyDecimal(money shopifyMoney) (*decimal.Decimal, error) {
	if money.Amount == "" {
		return nil, nil
	}
	amount, err := decimal.NewFromString(money.Amount)
	if err != nil {
		return nil, fmt.Errorf("invalid Shopify money amount %s", money.Amount)
	}
	return &amount, nil
}

func shopifyGraphQLAddress(address *shopifyOrderAddress) *types.OrderAddress {
	if address == nil {
		return nil
	}
	return &types.OrderAddress{
		FirstName: address.FirstName, LastName: address.LastName, Address1: address.Address1, Address2: address.Address2,
		City: address.City, Province: address.Province, ProvinceCode: address.ProvinceCode, Country: address.Country,
		CountryCode: address.CountryCode, Zip: address.Zip, Phone: address.Phone, Company: address.Company,
	}
}

func shopifyFulfillmentStatus(status string) types.OrderFulfillmentStatus {
	switch strings.ToUpper(status) {
	case "FULFILLED":
		return types.OrderFulfillmentStatusFulfilled
	case "PARTIALLY_FULFILLED":
		return types.OrderFulfillmentStatusPartial
	default:
		return types.OrderFulfillmentStatusUnfulfilled
	}
}
