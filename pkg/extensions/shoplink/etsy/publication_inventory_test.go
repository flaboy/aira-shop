package etsy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

func publicationThreeVariationRequest(t *testing.T) types.PublicationRequest {
	t.Helper()
	request := publicationTestRequest(t)
	request.Target.ListingID = "789"
	var configuration ListingConfiguration
	if err := json.Unmarshal(request.Configuration, &configuration); err != nil {
		t.Fatal(err)
	}
	properties := []ListingPropertyValue{{PropertyID: 100, PropertyName: "Size", ValueIDs: []int64{}, Values: []string{"M"}}, {PropertyID: 200, PropertyName: "Color", ValueIDs: []int64{}, Values: []string{"Red"}}, {PropertyID: 300, PropertyName: "Style", ValueIDs: []int64{}, Values: []string{"Regular"}}}
	configuration.Variants[0].PropertyValues = properties
	secondProperties := append([]ListingPropertyValue{}, properties...)
	secondProperties[0].Values = []string{"L"}
	quantity := 4
	configuration.Variants = append(configuration.Variants, ListingVariantConfiguration{LocalVariantID: 12, Quantity: &quantity, PropertyValues: secondProperties})
	price := decimal.RequireFromString("19.99")
	// 同一 SKU 的两个规格，必须根据属性维度和值区分，不能仅按 SKU 认领。
	request.Product.Variants = append(request.Product.Variants, types.ProductVariant{ID: 12, Sku: "SKU-A", Price: &price})
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	request.Configuration = data
	return request
}

func publicationInventoryJSON() string {
	return `{"products":[{"product_id":812,"sku":"SKU-A","property_values":[{"property_id":300,"value_ids":[330],"values":["Regular"]},{"property_id":200,"value_ids":[220],"values":["Red"]},{"property_id":100,"value_ids":[112],"values":["L"]}],"offerings":[{"offering_id":912,"quantity":4,"is_enabled":true,"price":{"amount":1999,"divisor":100}}]},{"product_id":811,"sku":"SKU-A","property_values":[{"property_id":200,"value_ids":[220],"values":["Red"]},{"property_id":100,"value_ids":[111],"values":["M"]},{"property_id":300,"value_ids":[330],"values":["Regular"]}],"offerings":[{"offering_id":911,"quantity":3,"is_enabled":true,"price":{"amount":1234,"divisor":100}}]}]}`
}

func TestPublicationInventorySupportsThreeDimensionsAndMapsUnorderedResults(t *testing.T) {
	var writes int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, publicationListingJSON("draft"))
			return
		}
		writes++
		if r.Method != http.MethodPut || r.URL.Path != "/v3/application/listings/789/inventory" || r.URL.Query().Get("max_variations_supported") != "3" {
			t.Errorf("wrong inventory endpoint: %s %s", r.Method, r.URL)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"price":12.34`) || !strings.Contains(string(raw), `"price":19.99`) || strings.Contains(string(raw), `"price":"`) {
			t.Fatalf("prices are not exact JSON numbers: %s", raw)
		}
		data := inventoryRequest{}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		if len(data.Products) != 2 || len(data.PriceOnProperty) != 3 || len(data.QuantityOnProperty) != 3 || len(data.SKUOnProperty) != 3 || len(data.ReadinessOnProperty) != 0 || data.Products[0].Offerings[0].ReadinessStateID.String() != "3" {
			t.Fatalf("incomplete inventory fields: %+v", data)
		}
		_, _ = io.WriteString(w, publicationInventoryJSON())
	})
	mappings, err := provider.ReplaceListingInventory(context.Background(), publicationTestCredential(), publicationThreeVariationRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if writes != 1 || len(mappings) != 2 || mappings[0].LocalVariantID != 11 || mappings[0].ProductID != "811" || mappings[0].OfferingID != "911" || mappings[1].LocalVariantID != 12 || mappings[1].ProductID != "812" {
		t.Fatalf("incorrect identity mapping: %+v", mappings)
	}
}

func TestPublicationInventoryReadbackUsesOnlyGETAndConfirmsAllVariants(t *testing.T) {
	var calls int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Fatal("inspection wrote inventory")
		}
		if strings.HasSuffix(r.URL.Path, "/inventory") {
			_, _ = io.WriteString(w, publicationInventoryJSON())
		} else {
			_, _ = io.WriteString(w, publicationListingJSON("draft"))
		}
	})
	mappings, err := provider.InspectListingInventory(context.Background(), publicationTestCredential(), publicationThreeVariationRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(mappings) != 2 {
		t.Fatalf("incomplete readback: %+v calls=%d", mappings, calls)
	}
}

func TestPublicationInventoryAmbiguityAndChangedFactsRemainUncertain(t *testing.T) {
	cases := []struct {
		name     string
		response string
	}{
		{"missing mapping", `{"products":[]}`},
		{"changed price", strings.Replace(publicationInventoryJSON(), `"amount":1234`, `"amount":1200`, 1)},
		{"changed quantity", strings.Replace(publicationInventoryJSON(), `"quantity":3`, `"quantity":2`, 1)},
		{"duplicate offering", strings.Replace(publicationInventoryJSON(), `"offering_id":911`, `"offering_id":912`, 1)},
		{"property cannot be matched", strings.Replace(publicationInventoryJSON(), `"values":["M"]`, `"values":["XXL"]`, 1)},
		{"product ID missing", strings.Replace(publicationInventoryJSON(), `"product_id":811`, `"product_id":null`, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var writes int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, publicationListingJSON("draft"))
					return
				}
				writes++
				_, _ = io.WriteString(w, test.response)
			})
			_, err := provider.ReplaceListingInventory(context.Background(), publicationTestCredential(), publicationThreeVariationRequest(t))
			var unknown *types.PublishResultUncertainError
			if !errors.As(err, &unknown) || writes != 1 {
				t.Fatalf("ambiguous write is retryable: %v writes=%d", err, writes)
			}
		})
	}
}

func TestPublicationInventoryRejectsIncompleteAndAmbiguousChoicesBeforeHTTP(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ListingConfiguration)
	}{
		{"duplicate local ID", func(c *ListingConfiguration) { c.Variants[1].LocalVariantID = c.Variants[0].LocalVariantID }},
		{"same property combination", func(c *ListingConfiguration) { c.Variants[1].PropertyValues = c.Variants[0].PropertyValues }},
		{"four variations", func(c *ListingConfiguration) {
			c.Variants[0].PropertyValues = append(c.Variants[0].PropertyValues, ListingPropertyValue{PropertyID: 400, Values: []string{"extra"}})
		}},
		{"dimensions differ", func(c *ListingConfiguration) { c.Variants[1].PropertyValues = c.Variants[1].PropertyValues[:2] }},
		{"negative quantity", func(c *ListingConfiguration) { n := -1; c.Variants[1].Quantity = &n }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { calls++; t.Fatal("invalid inventory reached HTTP") })
			request := publicationThreeVariationRequest(t)
			configuration := ListingConfiguration{}
			if err := json.Unmarshal(request.Configuration, &configuration); err != nil {
				t.Fatal(err)
			}
			test.mutate(&configuration)
			data, err := json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			request.Configuration = data
			if _, err := provider.ReplaceListingInventory(context.Background(), publicationTestCredential(), request); err == nil || calls != 0 {
				t.Fatalf("invalid inventory accepted: %v calls=%d", err, calls)
			}
		})
	}
}
