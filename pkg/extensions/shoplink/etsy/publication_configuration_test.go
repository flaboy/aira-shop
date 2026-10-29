package etsy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

func TestPublicationConfigurationQueriesOnlySupportedReadEndpoints(t *testing.T) {
	cases := []struct {
		group types.PublicationConfigurationGroup
		path  string
	}{
		{types.PublicationConfigurationTaxonomy, "/v3/application/seller-taxonomy/nodes"},
		{types.PublicationConfigurationProperties, "/v3/application/seller-taxonomy/nodes/1/properties"},
		{types.PublicationConfigurationShippingProfiles, "/v3/application/shops/456/shipping-profiles"},
		{types.PublicationConfigurationReadinessStates, "/v3/application/shops/456/readiness-state-definitions"},
		{types.PublicationConfigurationReturnPolicies, "/v3/application/shops/456/policies/return"},
		{types.PublicationConfigurationProductionPartners, "/v3/application/shops/456/production-partners"},
	}
	for _, test := range cases {
		t.Run(string(test.group), func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != test.path {
					t.Errorf("unexpected configuration request: %s %s", r.Method, r.URL)
				}
				if test.group == types.PublicationConfigurationReadinessStates {
					if r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("offset") != "3" {
						t.Errorf("pagination not sent: %s", r.URL)
					}
				} else if r.URL.Query().Has("offset") || r.URL.Query().Has("limit") {
					t.Fatal("fake pagination on unpaginated API")
				}
				if test.group == types.PublicationConfigurationProperties && r.URL.Query().Get("supports_variations") != "true" {
					t.Fatal("variation property filter missing")
				}
				if test.group == types.PublicationConfigurationReadinessStates {
					_, _ = io.WriteString(w, `{"count":8,"results":[{"id":1}]}`)
				} else {
					_, _ = io.WriteString(w, `{"count":1,"results":[{"id":1}]}`)
				}
			})
			query := types.PublicationConfigurationQuery{ExternalShopID: "456", Group: test.group, TaxonomyID: "1"}
			if test.group == types.PublicationConfigurationReadinessStates {
				query.Limit = 2
				query.Offset = 3
			}
			result, err := provider.QueryPublicationConfiguration(context.Background(), publicationTestCredential(), query)
			if err != nil {
				t.Fatal(err)
			}
			paginated := test.group == types.PublicationConfigurationReadinessStates
			if calls != 1 || result.PaginationSupported != paginated || result.HasMore != paginated || result.Count != map[bool]int{true: 8, false: 1}[paginated] {
				t.Fatalf("incorrect configuration result: %+v", result)
			}
		})
	}
}

func TestPublicationConfigurationRejectsUnsupportedPagingAndTargets(t *testing.T) {
	cases := []types.PublicationConfigurationQuery{
		{ExternalShopID: "456", Group: types.PublicationConfigurationReadinessStates, Limit: 101},
		{ExternalShopID: "456", Group: types.PublicationConfigurationReadinessStates, Offset: -1},
		{ExternalShopID: "456", Group: types.PublicationConfigurationTaxonomy, Offset: 1},
		{ExternalShopID: "456", Group: types.PublicationConfigurationShippingProfiles, Limit: 10},
		{ExternalShopID: "456", Group: types.PublicationConfigurationProperties, TaxonomyID: "../1"},
		{ExternalShopID: "456", Group: "https://attacker.example.test"},
		{ExternalShopID: "999", Group: types.PublicationConfigurationShippingProfiles},
	}
	for _, query := range cases {
		t.Run(string(query.Group), func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				t.Fatal("invalid configuration query reached HTTP")
			})
			if _, err := provider.QueryPublicationConfiguration(context.Background(), publicationTestCredential(), query); err == nil || calls != 0 {
				t.Fatalf("invalid configuration accepted: %v", err)
			}
		})
	}
}

func TestPublicationConfigurationDoesNotInventEmptyOptionsOnPermissionOrMalformedResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"count":0,"results":[{"id":1}]}`, `{"count":2,"results":[{"id":1}]}`, `not-json`} {
		provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) })
		if _, err := provider.QueryPublicationConfiguration(context.Background(), publicationTestCredential(), types.PublicationConfigurationQuery{ExternalShopID: "456", Group: types.PublicationConfigurationShippingProfiles}); err == nil {
			t.Fatal("malformed configuration became empty options")
		}
	}
}

func TestPublicationConfigurationShopReadsOnlyBoundShop(t *testing.T) {
	for _, shopID := range []string{"456", "999"} {
		t.Run(shopID, func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/v3/application/shops/456" {
					t.Errorf("unexpected shop configuration call: %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, `{"shop_id":`+shopID+`,"shop_name":"Test shop","currency_code":"USD"}`)
			})
			result, err := provider.QueryPublicationConfiguration(context.Background(), publicationTestCredential(), types.PublicationConfigurationQuery{ExternalShopID: "456", Group: types.PublicationConfigurationShop})
			if shopID == "456" {
				if err != nil {
					t.Fatal(err)
				}
				if result.Count != 1 || result.HasMore || result.PaginationSupported {
					t.Fatalf("incorrect single-shop metadata: %+v", result)
				}
			} else if err == nil || result != nil {
				t.Fatal("wrong shop identity accepted")
			}
			if calls != 1 {
				t.Fatalf("unexpected request count: %d", calls)
			}
		})
	}
}

func TestPublicationDraftValidatorHasNoSideEffects(t *testing.T) {
	request := publicationTestRequest(t)
	if err := ValidateDraftPublication(request); err != nil {
		t.Fatal(err)
	}
	request.Target.ListingID = "789"
	if err := ValidateDraftPublication(request); err == nil {
		t.Fatal("known listing accepted for draft creation")
	}
	request = publicationTestRequest(t)
	request.Configuration = bytes.Replace(request.Configuration, []byte(`"quantity":3`), []byte(`"quantity":0`), 1)
	if err := ValidateDraftPublication(request); err == nil {
		t.Fatal("all-zero draft quantity accepted")
	}
}

func TestPublicationDraftRejectsUnboundedDecimalBeforeHTTP(t *testing.T) {
	for _, number := range []string{"1e999999999", "1e-999999999", "9223372036854775808", "-9223372036854775808"} {
		t.Run(number, func(t *testing.T) {
			calls := 0
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { calls++; t.Fatal("unbounded numeric input reached HTTP") })
			request := publicationTestRequest(t)
			var variant types.ProductVariant
			if err := json.Unmarshal([]byte(`{"price":`+number+`}`), &variant); err != nil {
				t.Fatal(err)
			}
			request.Product.Variants[0].Price = variant.Price
			if _, err := provider.CreateDraft(context.Background(), publicationTestCredential(), request); err == nil {
				t.Fatal("unbounded price was accepted")
			}
			if calls != 0 {
				t.Fatal("invalid price caused a request")
			}
		})
	}
}

func TestPublicationPriceResourceBoundsAreSeparateFromBusinessPrice(t *testing.T) {
	for _, number := range []string{"1e6", "1e-6", "9223372036854775807"} {
		price, err := decimal.NewFromString(number)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePublicationPriceResource(&price); err != nil {
			t.Fatalf("bounded number rejected: %s", number)
		}
	}
}
