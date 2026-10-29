package etsy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

// ListingConfiguration 的 ID 均来自商家在当前店铺可用配置中的明确选择。
// 不对缺失值猜默认；is_supply/should_auto_renew 用指针区别显式 false 与缺失。
type ListingConfiguration struct {
	SchemaVersion        int                           `json:"schema_version"`
	TaxonomyID           string                        `json:"taxonomy_id"`
	ShippingProfileID    string                        `json:"shipping_profile_id"`
	ReadinessStateID     string                        `json:"readiness_state_id"`
	ReturnPolicyID       string                        `json:"return_policy_id"`
	WhoMade              string                        `json:"who_made"`
	WhenMade             string                        `json:"when_made"`
	IsSupply             *bool                         `json:"is_supply"`
	ShouldAutoRenew      *bool                         `json:"should_auto_renew"`
	ProductionPartnerIDs []string                      `json:"production_partner_ids"`
	Variants             []ListingVariantConfiguration `json:"variants"`
}

type ListingVariantConfiguration struct {
	LocalVariantID uint                   `json:"local_variant_id"`
	Quantity       *int                   `json:"quantity"`
	PropertyValues []ListingPropertyValue `json:"property_values"`
}

type ListingPropertyValue struct {
	PropertyID   int64    `json:"property_id"`
	PropertyName string   `json:"property_name,omitempty"`
	ScaleID      *int64   `json:"scale_id,omitempty"`
	ValueIDs     []int64  `json:"value_ids"`
	Values       []string `json:"values"`
}

type validatedListingVariant struct {
	LocalID    uint
	SKU        string
	Price      decimal.Decimal
	Quantity   int
	Properties []ListingPropertyValue
}

func validatePublicationRequest(request types.PublicationRequest) (ListingConfiguration, []validatedListingVariant, error) {
	configuration := ListingConfiguration{}
	if len(request.Configuration) == 0 || len(request.Configuration) > 256<<10 {
		return configuration, nil, errors.New("Etsy listing configuration is missing or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return configuration, nil, errors.New("Etsy listing configuration is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return configuration, nil, errors.New("Etsy listing configuration must contain one object")
	}
	if configuration.SchemaVersion != 1 || !validPublicationID(configuration.TaxonomyID) || !validPublicationID(configuration.ShippingProfileID) || !validPublicationID(configuration.ReadinessStateID) || !validPublicationID(configuration.ReturnPolicyID) || configuration.IsSupply == nil || configuration.ShouldAutoRenew == nil {
		return configuration, nil, errors.New("Etsy taxonomy, shipping, processing, return policy, supply and renewal choices are required")
	}
	if configuration.WhoMade != "i_did" && configuration.WhoMade != "someone_else" && configuration.WhoMade != "collective" {
		return configuration, nil, errors.New("Etsy who_made choice is invalid")
	}
	allowedEra := map[string]bool{"made_to_order": true, "2020_2026": true, "2010_2019": true, "2007_2009": true, "before_2007": true, "2000_2006": true, "1990s": true, "1980s": true, "1970s": true, "1960s": true, "1950s": true, "1940s": true, "1930s": true, "1920s": true, "1910s": true, "1900s": true, "1800s": true, "1700s": true, "before_1700": true}
	if !allowedEra[configuration.WhenMade] {
		return configuration, nil, errors.New("Etsy when_made choice is invalid")
	}
	seenPartners := make(map[string]bool)
	for _, id := range configuration.ProductionPartnerIDs {
		if !validPublicationID(id) || seenPartners[id] {
			return configuration, nil, errors.New("Etsy production partner choice is invalid")
		}
		seenPartners[id] = true
	}
	if request.Product == nil || strings.TrimSpace(request.Product.ProductName) == "" || strings.TrimSpace(request.Description) == "" || len(request.Product.ProductName) > 2048 || len(request.Description) > 100000 {
		return configuration, nil, errors.New("Etsy product title and plain text description are required")
	}
	if request.Product.ExternalShopID != "" && request.Product.ExternalShopID != request.Target.ExternalShopID {
		return configuration, nil, errors.New("Etsy product shop identity does not match the target")
	}
	if len(request.Product.Variants) == 0 || len(request.Product.Variants) > 1000 || len(configuration.Variants) != len(request.Product.Variants) {
		return configuration, nil, errors.New("Etsy quantity and property choices are required for every variant")
	}
	products := make(map[uint]types.ProductVariant, len(request.Product.Variants))
	for _, variant := range request.Product.Variants {
		if variant.ID == 0 || products[variant.ID].ID != 0 || strings.TrimSpace(variant.Sku) == "" || len(variant.Sku) > 256 || ValidatePublicationPriceResource(variant.Price) != nil || !variant.Price.GreaterThan(decimal.Zero) {
			return configuration, nil, errors.New("Etsy local variant identity, SKU and positive price are required")
		}
		products[variant.ID] = variant
	}
	seenVariants := make(map[uint]bool)
	seenProperties := make(map[string]bool)
	propertySet := ""
	result := make([]validatedListingVariant, 0, len(configuration.Variants))
	for index, choice := range configuration.Variants {
		variant, exists := products[choice.LocalVariantID]
		if !exists || seenVariants[choice.LocalVariantID] || choice.Quantity == nil || *choice.Quantity < 0 || *choice.Quantity > 1000000 {
			return configuration, nil, errors.New("Etsy variant quantity or identity is invalid")
		}
		seenVariants[choice.LocalVariantID] = true
		key, propertyIDs, err := propertySignature(choice.PropertyValues)
		if err != nil {
			return configuration, nil, err
		}
		if index == 0 {
			propertySet = propertyIDs
		} else if propertyIDs != propertySet {
			return configuration, nil, errors.New("Etsy variants must use the same property dimensions")
		}
		if seenProperties[key] {
			return configuration, nil, errors.New("Etsy variant property combinations must be unique")
		}
		seenProperties[key] = true
		result = append(result, validatedListingVariant{LocalID: choice.LocalVariantID, SKU: variant.Sku, Price: *variant.Price, Quantity: *choice.Quantity, Properties: choice.PropertyValues})
	}
	return configuration, result, nil
}

// propertySignature 忽略展示名称和 Etsy 分配的新 value_id，以稳定属性维度和值匹配。
// 不以返回顺序或可编辑 SKU 单独认领远端商品。
func propertySignature(properties []ListingPropertyValue) (string, string, error) {
	if len(properties) > 3 {
		return "", "", errors.New("Etsy listings support at most three variation dimensions")
	}
	type identity struct {
		ID    int64  `json:"id"`
		Scale int64  `json:"scale"`
		Value string `json:"value"`
	}
	identities := make([]identity, 0, len(properties))
	seen := make(map[int64]bool)
	for _, property := range properties {
		if property.PropertyID <= 0 || seen[property.PropertyID] || len(property.Values) != 1 || strings.TrimSpace(property.Values[0]) == "" || strings.ContainsAny(property.Values[0], "()") || len(property.Values[0]) > 512 {
			return "", "", errors.New("Etsy variant property identity or value is invalid")
		}
		if property.ScaleID != nil && *property.ScaleID <= 0 {
			return "", "", errors.New("Etsy variant scale is invalid")
		}
		for _, id := range property.ValueIDs {
			if id <= 0 {
				return "", "", errors.New("Etsy variant value ID is invalid")
			}
		}
		seen[property.PropertyID] = true
		scale := int64(0)
		if property.ScaleID != nil {
			scale = *property.ScaleID
		}
		identities = append(identities, identity{ID: property.PropertyID, Scale: scale, Value: property.Values[0]})
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].ID < identities[j].ID })
	encoded, err := json.Marshal(identities)
	if err != nil {
		return "", "", fmt.Errorf("encode Etsy property identity: %w", err)
	}
	ids := make([]string, 0, len(identities))
	for _, identity := range identities {
		ids = append(ids, strconv.FormatInt(identity.ID, 10))
	}
	return string(encoded), strings.Join(ids, ","), nil
}

func listingForm(request types.PublicationRequest, configuration ListingConfiguration) url.Values {
	form := url.Values{}
	form.Set("title", request.Product.ProductName)
	form.Set("description", request.Description)
	form.Set("taxonomy_id", configuration.TaxonomyID)
	form.Set("shipping_profile_id", configuration.ShippingProfileID)
	form.Set("return_policy_id", configuration.ReturnPolicyID)
	form.Set("who_made", configuration.WhoMade)
	form.Set("when_made", configuration.WhenMade)
	form.Set("is_supply", strconv.FormatBool(*configuration.IsSupply))
	form.Set("should_auto_renew", strconv.FormatBool(*configuration.ShouldAutoRenew))
	if len(configuration.ProductionPartnerIDs) > 0 {
		form.Set("production_partner_ids", strings.Join(configuration.ProductionPartnerIDs, ","))
	}
	if strings.TrimSpace(request.Product.Tags) != "" {
		form.Set("tags", request.Product.Tags)
	}
	return form
}

func (p *Etsy) QueryPublicationConfiguration(ctx context.Context, credential *types.ShopCredential, query types.PublicationConfigurationQuery) (*types.PublicationConfigurationResult, error) {
	token, err := publicationCredential(credential, query.ExternalShopID, "shops_r")
	if err != nil {
		return nil, err
	}
	path := ""
	paginated := false
	switch query.Group {
	case types.PublicationConfigurationShop:
		path = "/v3/application/shops/" + query.ExternalShopID
	case types.PublicationConfigurationTaxonomy:
		path = "/v3/application/seller-taxonomy/nodes"
	case types.PublicationConfigurationProperties:
		if !validPublicationID(query.TaxonomyID) {
			return nil, errors.New("Etsy taxonomy ID is required")
		}
		path = "/v3/application/seller-taxonomy/nodes/" + query.TaxonomyID + "/properties?supports_variations=true"
	case types.PublicationConfigurationShippingProfiles:
		path = "/v3/application/shops/" + query.ExternalShopID + "/shipping-profiles"
	case types.PublicationConfigurationReadinessStates:
		path = "/v3/application/shops/" + query.ExternalShopID + "/readiness-state-definitions"
		paginated = true
	case types.PublicationConfigurationReturnPolicies:
		path = "/v3/application/shops/" + query.ExternalShopID + "/policies/return"
	case types.PublicationConfigurationProductionPartners:
		path = "/v3/application/shops/" + query.ExternalShopID + "/production-partners"
	default:
		return nil, errors.New("Etsy publication configuration group is unsupported")
	}
	if query.Offset < 0 || query.Offset > 1000000 || query.Limit < 0 || query.Limit > 100 || (!paginated && (query.Offset != 0 || query.Limit != 0)) {
		return nil, errors.New("Etsy configuration pagination is invalid or unsupported")
	}
	limit := query.Limit
	if limit == 0 {
		limit = 25
	}
	if paginated {
		path += "?limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(query.Offset)
	}
	data := json.RawMessage{}
	if err := p.publicationJSON(ctx, http.MethodGet, path, token, "", nil, &data); err != nil {
		return nil, err
	}
	if query.Group == types.PublicationConfigurationShop {
		shop := struct {
			ShopID json.Number `json:"shop_id"`
		}{}
		if err := json.Unmarshal(data, &shop); err != nil || !validPublicationID(shop.ShopID.String()) || shop.ShopID.String() != query.ExternalShopID {
			return nil, errors.New("Etsy shop configuration identity does not match the target")
		}
		return &types.PublicationConfigurationResult{Data: data, Count: 1}, nil
	}
	envelope := struct {
		Count   *int              `json:"count"`
		Results []json.RawMessage `json:"results"`
	}{}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Count == nil || *envelope.Count < 0 || *envelope.Count < len(envelope.Results) || (!paginated && *envelope.Count != len(envelope.Results)) || (paginated && len(envelope.Results) == 0 && query.Offset < *envelope.Count) {
		return nil, errors.New("Etsy configuration response is incomplete")
	}
	return &types.PublicationConfigurationResult{Data: data, Count: *envelope.Count, PaginationSupported: paginated, HasMore: paginated && query.Offset+len(envelope.Results) < *envelope.Count}, nil
}

// ValidateDraftPublication 在任何远端写入之前复用适配器校验，不解析凭据、不发请求。
func ValidateDraftPublication(request types.PublicationRequest) error {
	if !validPublicationID(request.Target.ExternalShopID) || request.Target.ListingID != "" {
		return errors.New("Etsy draft target is invalid")
	}
	_, variants, err := validatePublicationRequest(request)
	if err != nil {
		return err
	}
	total := 0
	for _, variant := range variants {
		total += variant.Quantity
	}
	if total <= 0 {
		return errors.New("Etsy draft requires positive available quantity")
	}
	return nil
}

// ValidatePublicationPriceResource 是计算资源界限，不是 Etsy 的业务价格上下限。
// 必须先于比较、舍入及字符串/JSON格式化调用，避免科学记数触发巨量 rescale。
func ValidatePublicationPriceResource(price *decimal.Decimal) error {
	if !types.IsPublicationDecimalResourceBounded(price) {
		return errors.New("Etsy listing price exceeds supported numeric resource limits")
	}
	return nil
}
