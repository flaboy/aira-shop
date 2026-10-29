package etsy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

type inventoryOfferingRequest struct {
	Price            json.RawMessage `json:"price"`
	Quantity         int             `json:"quantity"`
	Enabled          bool            `json:"is_enabled"`
	ReadinessStateID json.Number     `json:"readiness_state_id"`
}
type inventoryProductRequest struct {
	SKU        string                     `json:"sku"`
	Offerings  []inventoryOfferingRequest `json:"offerings"`
	Properties []ListingPropertyValue     `json:"property_values"`
}
type inventoryRequest struct {
	Products            []inventoryProductRequest `json:"products"`
	PriceOnProperty     []int64                   `json:"price_on_property"`
	QuantityOnProperty  []int64                   `json:"quantity_on_property"`
	SKUOnProperty       []int64                   `json:"sku_on_property"`
	ReadinessOnProperty []int64                   `json:"readiness_state_on_property"`
}
type inventoryResponse struct {
	Products []struct {
		ProductID  json.Number            `json:"product_id"`
		SKU        string                 `json:"sku"`
		Deleted    bool                   `json:"is_deleted"`
		Properties []ListingPropertyValue `json:"property_values"`
		Offerings  []struct {
			OfferingID json.Number `json:"offering_id"`
			Deleted    bool        `json:"is_deleted"`
			Quantity   *int        `json:"quantity"`
			Enabled    *bool       `json:"is_enabled"`
			Price      struct {
				Amount  json.Number `json:"amount"`
				Divisor json.Number `json:"divisor"`
			} `json:"price"`
		} `json:"offerings"`
	} `json:"products"`
}

func (p *Etsy) ReplaceListingInventory(ctx context.Context, credential *types.ShopCredential, request types.PublicationRequest) ([]types.PublicationVariantMapping, error) {
	token, err := publicationCredential(credential, request.Target.ExternalShopID, "listings_r", "listings_w")
	if err != nil {
		return nil, err
	}
	configuration, variants, err := validatePublicationRequest(request)
	if err != nil {
		return nil, err
	}
	if _, err := p.InspectListing(ctx, credential, request.Target); err != nil {
		return nil, err
	}
	payload := inventoryRequest{Products: make([]inventoryProductRequest, 0, len(variants)), PriceOnProperty: []int64{}, QuantityOnProperty: []int64{}, SKUOnProperty: []int64{}, ReadinessOnProperty: []int64{}}
	for _, property := range variants[0].Properties {
		payload.PriceOnProperty = append(payload.PriceOnProperty, property.PropertyID)
	}
	sort.Slice(payload.PriceOnProperty, func(i, j int) bool { return payload.PriceOnProperty[i] < payload.PriceOnProperty[j] })
	payload.QuantityOnProperty = append(payload.QuantityOnProperty, payload.PriceOnProperty...)
	payload.SKUOnProperty = append(payload.SKUOnProperty, payload.PriceOnProperty...)
	for _, variant := range variants {
		properties := append([]ListingPropertyValue{}, variant.Properties...)
		for i := range properties {
			if properties[i].ValueIDs == nil {
				properties[i].ValueIDs = []int64{}
			}
		}
		payload.Products = append(payload.Products, inventoryProductRequest{SKU: variant.SKU, Properties: properties, Offerings: []inventoryOfferingRequest{{Price: json.RawMessage(variant.Price.String()), Quantity: variant.Quantity, Enabled: true, ReadinessStateID: json.Number(configuration.ReadinessStateID)}}})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Etsy inventory: %w", err)
	}
	result := inventoryResponse{}
	if err := p.publicationJSON(ctx, http.MethodPut, "/v3/application/listings/"+request.Target.ListingID+"/inventory?max_variations_supported=3", token, "application/json", bytes.NewReader(body), &result); err != nil {
		return nil, err
	}
	mappings, err := mapPublicationInventory(result, variants)
	if err != nil {
		return nil, uncertainPublication(err)
	}
	return mappings, nil
}

// InspectListingInventory 只读取已知 Listing 的实际库存；不以缺少信息推导可以重发写入。
func (p *Etsy) InspectListingInventory(ctx context.Context, credential *types.ShopCredential, request types.PublicationRequest) ([]types.PublicationVariantMapping, error) {
	token, err := publicationCredential(credential, request.Target.ExternalShopID, "listings_r")
	if err != nil {
		return nil, err
	}
	_, variants, err := validatePublicationRequest(request)
	if err != nil {
		return nil, err
	}
	if _, err := p.InspectListing(ctx, credential, request.Target); err != nil {
		return nil, err
	}
	result := inventoryResponse{}
	if err := p.publicationJSON(ctx, http.MethodGet, "/v3/application/listings/"+request.Target.ListingID+"/inventory", token, "", nil, &result); err != nil {
		return nil, err
	}
	return mapPublicationInventory(result, variants)
}

func mapPublicationInventory(response inventoryResponse, variants []validatedListingVariant) ([]types.PublicationVariantMapping, error) {
	expected := make(map[string]validatedListingVariant, len(variants))
	for _, variant := range variants {
		key, _, err := propertySignature(variant.Properties)
		if err != nil {
			return nil, err
		}
		expected[variant.SKU+"\x00"+key] = variant
	}
	mappings := make([]types.PublicationVariantMapping, 0, len(variants))
	seenProducts := make(map[string]bool)
	seenOfferings := make(map[string]bool)
	for _, product := range response.Products {
		if product.Deleted {
			continue
		}
		key, _, err := propertySignature(product.Properties)
		if err != nil {
			return nil, errors.New("Etsy inventory returned unsupported property identities")
		}
		lookup := product.SKU + "\x00" + key
		variant, exists := expected[lookup]
		if !exists || !validPublicationID(product.ProductID.String()) || seenProducts[product.ProductID.String()] {
			return nil, errors.New("Etsy inventory product mapping is incomplete or ambiguous")
		}
		var offeringID string
		for _, offering := range product.Offerings {
			if offering.Deleted {
				continue
			}
			if offeringID != "" || !validPublicationID(offering.OfferingID.String()) || seenOfferings[offering.OfferingID.String()] || offering.Quantity == nil || *offering.Quantity != variant.Quantity || offering.Enabled == nil || !*offering.Enabled {
				return nil, errors.New("Etsy inventory offering does not match the requested variant")
			}
			amount, err := decimal.NewFromString(offering.Price.Amount.String())
			if err != nil {
				return nil, errors.New("Etsy inventory price is incomplete")
			}
			divisor, err := decimal.NewFromString(offering.Price.Divisor.String())
			if err != nil || !divisor.GreaterThan(decimal.Zero) || !amount.Equal(variant.Price.Mul(divisor)) {
				return nil, errors.New("Etsy inventory price does not match the requested variant")
			}
			offeringID = offering.OfferingID.String()
		}
		if offeringID == "" {
			return nil, errors.New("Etsy inventory offering identity is missing")
		}
		properties, err := json.Marshal(product.Properties)
		if err != nil {
			return nil, fmt.Errorf("encode Etsy inventory properties: %w", err)
		}
		mappings = append(mappings, types.PublicationVariantMapping{LocalVariantID: variant.LocalID, ProductID: product.ProductID.String(), OfferingID: offeringID, SKU: product.SKU, Properties: properties})
		seenProducts[product.ProductID.String()] = true
		seenOfferings[offeringID] = true
		delete(expected, lookup)
	}
	if len(expected) != 0 || len(mappings) != len(variants) {
		return nil, errors.New("Etsy inventory did not confirm every local variant")
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].LocalVariantID < mappings[j].LocalVariantID })
	return mappings, nil
}
