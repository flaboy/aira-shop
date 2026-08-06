package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

const (
	shopifyProductPageSize       = 100
	shopifyProductMaximumPages   = 100
	shopifyProductMetafieldLimit = 20
)

var errShopifyProductNotFound = errors.New("Shopify product not found")

type shopifyGraphQLClient struct {
	shopDomain  string
	accessToken string
	httpClient  HTTPDoer
}

type shopifyGraphQLCost struct {
	RequestedQueryCost int `json:"requestedQueryCost"`
	ActualQueryCost    int `json:"actualQueryCost"`
	ThrottleStatus     struct {
		MaximumAvailable   float64 `json:"maximumAvailable"`
		CurrentlyAvailable float64 `json:"currentlyAvailable"`
		RestoreRate        float64 `json:"restoreRate"`
	} `json:"throttleStatus"`
}

type shopifyGraphQLError struct {
	Message string `json:"message"`
}

type shopifyGraphQLUserError struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
	Code    string   `json:"code"`
}

type shopifyGraphQLResponse struct {
	Data       json.RawMessage       `json:"data"`
	Errors     []shopifyGraphQLError `json:"errors"`
	Extensions struct {
		Cost shopifyGraphQLCost `json:"cost"`
	} `json:"extensions"`
}

type shopifyGraphQLProduct struct {
	ID               string   `json:"id"`
	LegacyResourceID string   `json:"legacyResourceId"`
	Title            string   `json:"title"`
	DescriptionHTML  string   `json:"descriptionHtml"`
	Status           string   `json:"status"`
	Tags             []string `json:"tags"`
	Options          []struct {
		Name     string   `json:"name"`
		Position int      `json:"position"`
		Values   []string `json:"values"`
	} `json:"options"`
	Variants struct {
		Nodes []shopifyGraphQLVariant `json:"nodes"`
	} `json:"variants"`
	Metafields struct {
		Nodes []shopifyGraphQLMetafield `json:"nodes"`
	} `json:"metafields"`
}

type shopifyGraphQLVariant struct {
	ID               string `json:"id"`
	LegacyResourceID string `json:"legacyResourceId"`
	Title            string `json:"title"`
	SKU              string `json:"sku"`
	Price            string `json:"price"`
	CompareAtPrice   string `json:"compareAtPrice"`
	SelectedOptions  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"selectedOptions"`
}

type shopifyGraphQLMetafield struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Type      string `json:"type"`
	Value     string `json:"value"`
}

type shopifyGraphQLProductPage struct {
	Nodes    []shopifyGraphQLProduct `json:"nodes"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

func newShopifyGraphQLClient(shopDomain, accessToken string, httpClient HTTPDoer) (*shopifyGraphQLClient, error) {
	shopDomain, valid := NormalizePermanentShopDomain(shopDomain)
	if !valid || accessToken == "" || httpClient == nil {
		return nil, fmt.Errorf("Shopify GraphQL product configuration is incomplete")
	}
	return &shopifyGraphQLClient{shopDomain: shopDomain, accessToken: accessToken, httpClient: httpClient}, nil
}

func (c *shopifyGraphQLClient) execute(ctx context.Context, operation, query string, variables map[string]any, target any) (shopifyGraphQLCost, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("encode Shopify GraphQL %s request: %w", operation, err)
	}
	endpoint := "https://" + c.shopDomain + "/admin/api/" + shopifyAdminAPIVersion + "/graphql.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("create Shopify GraphQL %s request: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Shopify-Access-Token", c.accessToken)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("execute Shopify GraphQL %s request: %w", operation, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, maximumShopifyResponseBodySize+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("read Shopify GraphQL %s response: %w", operation, readErr)
	}
	if closeErr != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("close Shopify GraphQL %s response: %w", operation, closeErr)
	}
	if len(responseBody) > maximumShopifyResponseBodySize {
		return shopifyGraphQLCost{}, fmt.Errorf("Shopify GraphQL %s response exceeds the maximum size", operation)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return shopifyGraphQLCost{}, fmt.Errorf("Shopify GraphQL %s returned HTTP %d", operation, response.StatusCode)
	}

	var envelope shopifyGraphQLResponse
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return shopifyGraphQLCost{}, fmt.Errorf("decode Shopify GraphQL %s response: %w", operation, err)
	}
	if len(envelope.Errors) > 0 {
		return envelope.Extensions.Cost, fmt.Errorf("Shopify GraphQL %s failed: %s", operation, envelope.Errors[0].Message)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return envelope.Extensions.Cost, fmt.Errorf("Shopify GraphQL %s returned no data", operation)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return envelope.Extensions.Cost, fmt.Errorf("decode Shopify GraphQL %s data: %w", operation, err)
	}
	slog.DebugContext(ctx, "Shopify GraphQL 请求完成",
		"operation", operation,
		"requested_cost", envelope.Extensions.Cost.RequestedQueryCost,
		"actual_cost", envelope.Extensions.Cost.ActualQueryCost,
		"currently_available", envelope.Extensions.Cost.ThrottleStatus.CurrentlyAvailable,
		"restore_rate", envelope.Extensions.Cost.ThrottleStatus.RestoreRate,
	)
	return envelope.Extensions.Cost, nil
}

const shopifyProductSelection = `
  id
  legacyResourceId
  title
  descriptionHtml
  status
  tags
  options { name position values }
  variants(first: 100) {
    nodes {
      id
      legacyResourceId
      title
      sku
      price
      compareAtPrice
      selectedOptions { name value }
    }
  }
  metafields(first: 20, namespace: "effiprint") {
    nodes { id namespace key type value }
  }
`

func (c *shopifyGraphQLClient) getProduct(ctx context.Context, productID uint64) (*goshopify.Product, []goshopify.Metafield, shopifyGraphQLCost, error) {
	query := `query EffiPrintProduct($id: ID!) { product(id: $id) {` + shopifyProductSelection + `} }`
	var data struct {
		Product *shopifyGraphQLProduct `json:"product"`
	}
	cost, err := c.execute(ctx, "product", query, map[string]any{"id": shopifyProductGID(productID)}, &data)
	if err != nil {
		return nil, nil, cost, err
	}
	if data.Product == nil {
		return nil, nil, cost, errShopifyProductNotFound
	}
	product, metafields, err := normalizeShopifyGraphQLProduct(*data.Product)
	if err != nil {
		return nil, nil, cost, err
	}
	return &product, metafields, cost, nil
}

func (c *shopifyGraphQLClient) setProduct(ctx context.Context, productID uint64, input map[string]any) (*goshopify.Product, []goshopify.Metafield, shopifyGraphQLCost, error) {
	query := `mutation EffiPrintProductSet($input: ProductSetInput!, $identifier: ProductSetIdentifiers, $synchronous: Boolean!) {
  productSet(input: $input, identifier: $identifier, synchronous: $synchronous) {
    product {` + shopifyProductSelection + `}
    userErrors { field message code }
  }
}`
	variables := map[string]any{"input": input, "synchronous": true}
	if productID > 0 {
		variables["identifier"] = map[string]any{"id": shopifyProductGID(productID)}
	}
	var data struct {
		ProductSet struct {
			Product    *shopifyGraphQLProduct    `json:"product"`
			UserErrors []shopifyGraphQLUserError `json:"userErrors"`
		} `json:"productSet"`
	}
	cost, err := c.execute(ctx, "productSet", query, variables, &data)
	if err != nil {
		return nil, nil, cost, err
	}
	if err := shopifyUserErrors("productSet", data.ProductSet.UserErrors); err != nil {
		return nil, nil, cost, err
	}
	if data.ProductSet.Product == nil {
		return nil, nil, cost, fmt.Errorf("Shopify productSet returned no product")
	}
	product, metafields, err := normalizeShopifyGraphQLProduct(*data.ProductSet.Product)
	if err != nil {
		return nil, nil, cost, err
	}
	return &product, metafields, cost, nil
}

func (c *shopifyGraphQLClient) setProductMetafields(ctx context.Context, productID uint64, metafields []map[string]any) (shopifyGraphQLCost, error) {
	query := `mutation EffiPrintMetafieldsSet($metafields: [MetafieldsSetInput!]!) {
  metafieldsSet(metafields: $metafields) {
    metafields { id namespace key type value }
    userErrors { field message code }
  }
}`
	ownerID := shopifyProductGID(productID)
	for index := range metafields {
		metafields[index]["ownerId"] = ownerID
	}
	var data struct {
		MetafieldsSet struct {
			UserErrors []shopifyGraphQLUserError `json:"userErrors"`
		} `json:"metafieldsSet"`
	}
	cost, err := c.execute(ctx, "metafieldsSet", query, map[string]any{"metafields": metafields}, &data)
	if err != nil {
		return cost, err
	}
	return cost, shopifyUserErrors("metafieldsSet", data.MetafieldsSet.UserErrors)
}

func (c *shopifyGraphQLClient) deleteProduct(ctx context.Context, productID uint64) (shopifyGraphQLCost, error) {
	query := `mutation EffiPrintProductDelete($input: ProductDeleteInput!) {
  productDelete(input: $input) {
    deletedProductId
    userErrors { field message }
  }
}`
	var data struct {
		ProductDelete struct {
			DeletedProductID string                    `json:"deletedProductId"`
			UserErrors       []shopifyGraphQLUserError `json:"userErrors"`
		} `json:"productDelete"`
	}
	cost, err := c.execute(ctx, "productDelete", query, map[string]any{"input": map[string]any{"id": shopifyProductGID(productID)}}, &data)
	if err != nil {
		return cost, err
	}
	if err := shopifyUserErrors("productDelete", data.ProductDelete.UserErrors); err != nil {
		return cost, err
	}
	return cost, nil
}

func (c *shopifyGraphQLClient) listProducts(ctx context.Context) ([]goshopify.Product, map[uint64][]goshopify.Metafield, error) {
	query := `query EffiPrintProducts($first: Int!, $after: String) {
  products(first: $first, after: $after, sortKey: ID) {
    nodes {` + shopifyProductSelection + `}
    pageInfo { hasNextPage endCursor }
  }
}`
	products := make([]goshopify.Product, 0)
	metafields := map[uint64][]goshopify.Metafield{}
	after := ""
	for page := 0; page < shopifyProductMaximumPages; page++ {
		variables := map[string]any{"first": shopifyProductPageSize}
		if after != "" {
			variables["after"] = after
		}
		var data struct {
			Products shopifyGraphQLProductPage `json:"products"`
		}
		if _, err := c.execute(ctx, "products", query, variables, &data); err != nil {
			return nil, nil, err
		}
		for _, remote := range data.Products.Nodes {
			product, productMetafields, err := normalizeShopifyGraphQLProduct(remote)
			if err != nil {
				return nil, nil, err
			}
			products = append(products, product)
			metafields[product.Id] = productMetafields
		}
		if !data.Products.PageInfo.HasNextPage {
			return products, metafields, nil
		}
		if data.Products.PageInfo.EndCursor == "" {
			return nil, nil, fmt.Errorf("Shopify products pagination returned no cursor")
		}
		after = data.Products.PageInfo.EndCursor
	}
	return nil, nil, fmt.Errorf("Shopify product audit exceeds %d products", shopifyProductPageSize*shopifyProductMaximumPages)
}

func shopifyUserErrors(operation string, userErrors []shopifyGraphQLUserError) error {
	if len(userErrors) == 0 {
		return nil
	}
	message := userErrors[0].Message
	if message == "" {
		message = "unknown user error"
	}
	return fmt.Errorf("Shopify %s rejected the request: %s", operation, message)
}

func normalizeShopifyGraphQLProduct(source shopifyGraphQLProduct) (goshopify.Product, []goshopify.Metafield, error) {
	productID, err := strconv.ParseUint(source.LegacyResourceID, 10, 64)
	if err != nil || productID == 0 {
		return goshopify.Product{}, nil, fmt.Errorf("Shopify product GraphQL identity is invalid")
	}
	product := goshopify.Product{Id: productID, Title: source.Title, BodyHTML: source.DescriptionHTML, Status: goshopify.ProductStatus(strings.ToLower(source.Status)), Tags: strings.Join(source.Tags, ",")}
	for _, option := range source.Options {
		product.Options = append(product.Options, goshopify.ProductOption{Name: option.Name, Position: option.Position, Values: option.Values})
	}
	for _, sourceVariant := range source.Variants.Nodes {
		variantID, err := strconv.ParseUint(sourceVariant.LegacyResourceID, 10, 64)
		if err != nil || variantID == 0 {
			return goshopify.Product{}, nil, fmt.Errorf("Shopify variant GraphQL identity is invalid")
		}
		variant := goshopify.Variant{Id: variantID, ProductId: productID, Title: sourceVariant.Title, Sku: sourceVariant.SKU}
		if sourceVariant.Price != "" {
			price, err := decimal.NewFromString(sourceVariant.Price)
			if err != nil {
				return goshopify.Product{}, nil, fmt.Errorf("decode Shopify variant price: %w", err)
			}
			variant.Price = &price
		}
		if sourceVariant.CompareAtPrice != "" {
			compareAtPrice, err := decimal.NewFromString(sourceVariant.CompareAtPrice)
			if err != nil {
				return goshopify.Product{}, nil, fmt.Errorf("decode Shopify variant compare-at price: %w", err)
			}
			variant.CompareAtPrice = &compareAtPrice
		}
		for index, selected := range sourceVariant.SelectedOptions {
			switch index {
			case 0:
				variant.Option1 = selected.Value
			case 1:
				variant.Option2 = selected.Value
			case 2:
				variant.Option3 = selected.Value
			}
		}
		product.Variants = append(product.Variants, variant)
	}
	resultMetafields := make([]goshopify.Metafield, 0, len(source.Metafields.Nodes))
	for _, metafield := range source.Metafields.Nodes {
		resultMetafields = append(resultMetafields, goshopify.Metafield{Namespace: metafield.Namespace, Key: metafield.Key, Type: goshopify.MetafieldType(metafield.Type), Value: metafield.Value})
	}
	return product, resultMetafields, nil
}

func shopifyProductGID(productID uint64) string {
	return fmt.Sprintf("gid://shopify/Product/%d", productID)
}

func shopifyVariantGID(variantID uint64) string {
	return fmt.Sprintf("gid://shopify/ProductVariant/%d", variantID)
}

func shopifyProductSetInput(product *types.ProductData, converted goshopify.Product, includeIdentity bool) (map[string]any, error) {
	input := map[string]any{
		"title":           converted.Title,
		"descriptionHtml": converted.BodyHTML,
		"status":          "ACTIVE",
		"tags":            splitShopifyTags(converted.Tags),
	}
	options, optionNames := shopifyProductOptions(product.Options)
	input["productOptions"] = options
	variants := make([]map[string]any, 0, len(product.Variants))
	for index, variant := range product.Variants {
		variantInput, err := shopifyVariantSetInput(variant, optionNames)
		if err != nil {
			return nil, err
		}
		variantInput["position"] = index + 1
		variantInput["metafields"] = []map[string]any{{
			"namespace": "aira-shop",
			"key":       "origin",
			"type":      "single_line_text_field",
			"value":     strconv.FormatUint(uint64(variant.ID), 10),
		}}
		variants = append(variants, variantInput)
	}
	input["variants"] = variants
	input["files"] = shopifyProductFiles(product)
	if includeIdentity {
		metafields, err := shopifyProductIdentityMetafields(product)
		if err != nil {
			return nil, err
		}
		input["metafields"] = metafields
	}
	return input, nil
}

func shopifyProductSelectiveSetInput(product *types.ProductData, converted goshopify.Product, remote goshopify.Product, mappings []models.ShopifyVariantMapping) (map[string]any, error) {
	fields := productUpdateFieldSet(product.UpdateFields)
	input := map[string]any{}
	if _, selected := fields[types.ProductUpdateFieldTitle]; selected {
		input["title"] = converted.Title
	}
	if _, selected := fields[types.ProductUpdateFieldDescription]; selected {
		input["descriptionHtml"] = converted.BodyHTML
	}
	if _, selected := fields[types.ProductUpdateFieldImages]; selected {
		input["files"] = shopifyProductFiles(product)
	}
	if _, selected := fields[types.ProductUpdateFieldVariantsPrices]; selected {
		localByRemote := make(map[uint64]types.ProductVariant, len(mappings))
		localByID := make(map[uint]types.ProductVariant, len(product.Variants))
		for _, variant := range product.Variants {
			localByID[variant.ID] = variant
		}
		for _, mapping := range mappings {
			variant, exists := localByID[mapping.LocalVariantID]
			if !exists {
				return nil, fmt.Errorf("EffiPrint local variant mapping is invalid: %d", mapping.LocalVariantID)
			}
			localByRemote[mapping.ShopifyVariantID] = variant
		}
		variants := make([]map[string]any, 0, len(remote.Variants))
		for _, remoteVariant := range remote.Variants {
			variantInput := map[string]any{
				"id":           shopifyVariantGID(remoteVariant.Id),
				"optionValues": shopifyRemoteOptionValues(remote.Options, remoteVariant),
			}
			if localVariant, exists := localByRemote[remoteVariant.Id]; exists {
				if localVariant.Price != nil {
					variantInput["price"] = localVariant.Price.String()
				}
				if localVariant.CompareAtPrice != nil {
					variantInput["compareAtPrice"] = localVariant.CompareAtPrice.String()
				}
			}
			variants = append(variants, variantInput)
		}
		input["variants"] = variants
	}
	return input, nil
}

func applyShopifyProductSetVariantIDs(input map[string]any, localVariants []types.ProductVariant, mappings []models.ShopifyVariantMapping) error {
	variants, ok := input["variants"].([]map[string]any)
	if !ok || len(variants) != len(localVariants) {
		return fmt.Errorf("local Shopify variant payload is incomplete")
	}
	remoteByLocalID := make(map[uint]uint64, len(mappings))
	for _, mapping := range mappings {
		remoteByLocalID[mapping.LocalVariantID] = mapping.ShopifyVariantID
	}
	for index, localVariant := range localVariants {
		remoteID := remoteByLocalID[localVariant.ID]
		if remoteID == 0 {
			return fmt.Errorf("Shopify variant mapping is missing for local variant %d", localVariant.ID)
		}
		variants[index]["id"] = shopifyVariantGID(remoteID)
	}
	return nil
}

func shopifyProductOptions(options []types.ProductOption) ([]map[string]any, []string) {
	if len(options) == 0 {
		return []map[string]any{{"name": "Title", "position": 1, "values": []map[string]any{{"name": "Default Title"}}}}, []string{"Title"}
	}
	result := make([]map[string]any, 0, len(options))
	names := make([]string, 0, len(options))
	for index, option := range options {
		values := make([]map[string]any, 0, len(option.Values))
		for _, value := range option.Values {
			values = append(values, map[string]any{"name": value})
		}
		result = append(result, map[string]any{"name": option.Name, "position": index + 1, "values": values})
		names = append(names, option.Name)
	}
	return result, names
}

func shopifyVariantSetInput(variant types.ProductVariant, optionNames []string) (map[string]any, error) {
	values := []string{variant.Option1, variant.Option2, variant.Option3}
	optionValues := make([]map[string]any, 0, len(optionNames))
	for index, optionName := range optionNames {
		value := "Default Title"
		if index < len(values) && values[index] != "" {
			value = values[index]
		}
		optionValues = append(optionValues, map[string]any{"optionName": optionName, "name": value})
	}
	inventoryItem := map[string]any{"requiresShipping": true}
	if variant.Weight != nil {
		unit, err := shopifyWeightUnit(variant.WeightUnit)
		if err != nil {
			return nil, err
		}
		weight, _ := variant.Weight.Float64()
		inventoryItem["measurement"] = map[string]any{"weight": map[string]any{"unit": unit, "value": weight}}
	}
	input := map[string]any{"optionValues": optionValues, "sku": variant.Sku, "inventoryItem": inventoryItem}
	if variant.Price != nil {
		input["price"] = variant.Price.String()
	}
	if variant.CompareAtPrice != nil {
		input["compareAtPrice"] = variant.CompareAtPrice.String()
	}
	return input, nil
}

func shopifyWeightUnit(unit string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "g":
		return "GRAMS", nil
	case "kg":
		return "KILOGRAMS", nil
	case "oz":
		return "OUNCES", nil
	case "lb":
		return "POUNDS", nil
	default:
		return "", fmt.Errorf("unsupported Shopify variant weight unit: %s", unit)
	}
}

func shopifyRemoteOptionValues(options []goshopify.ProductOption, variant goshopify.Variant) []map[string]any {
	values := []string{variant.Option1, variant.Option2, variant.Option3}
	result := make([]map[string]any, 0, len(options))
	for index, option := range options {
		if index >= len(values) {
			break
		}
		result = append(result, map[string]any{"optionName": option.Name, "name": values[index]})
	}
	return result
}

func shopifyProductFiles(product *types.ProductData) []map[string]any {
	images := make([]types.ProductImage, 0, len(product.Images)+1)
	if product.Image.Src != "" {
		images = append(images, product.Image)
	}
	images = append(images, product.Images...)
	files := make([]map[string]any, 0, len(images))
	for _, image := range images {
		if image.Src == "" {
			continue
		}
		file := map[string]any{"originalSource": image.Src, "contentType": "IMAGE"}
		if image.Alt != "" {
			file["alt"] = image.Alt
		}
		if image.Filename != "" {
			file["filename"] = image.Filename
		}
		files = append(files, file)
	}
	return files
}

func shopifyProductIdentityMetafields(product *types.ProductData) ([]map[string]any, error) {
	if product.OriginProductID == "" || product.PublishOperationID == "" || product.ExternalShopID == "" {
		return nil, fmt.Errorf("Shopify product publish identity is incomplete")
	}
	metafields := []map[string]any{
		{"namespace": "effiprint", "key": "origin_product_id", "type": "single_line_text_field", "value": product.OriginProductID},
		{"namespace": "effiprint", "key": "publish_operation_id", "type": "single_line_text_field", "value": product.PublishOperationID},
		{"namespace": "effiprint", "key": "external_shop_id", "type": "single_line_text_field", "value": product.ExternalShopID},
	}
	brandServices, err := shopifyBrandServicesMetafield(product.BrandServices)
	if err != nil {
		return nil, err
	}
	return append(metafields, brandServices), nil
}

func shopifyBrandServicesMetafield(brandServices types.ProductBrandServices) (map[string]any, error) {
	value, err := json.Marshal(brandServices)
	if err != nil {
		return nil, fmt.Errorf("marshal product brand services: %w", err)
	}
	return map[string]any{"namespace": "effiprint", "key": "branding_services", "type": "json", "value": string(value)}, nil
}

func splitShopifyTags(tags string) []string {
	result := make([]string, 0)
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			result = append(result, tag)
		}
	}
	return result
}
