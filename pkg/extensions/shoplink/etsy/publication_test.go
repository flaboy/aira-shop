package etsy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"
)

func publicationTestCredential() *types.ShopCredential {
	return &types.ShopCredential{Platform: PlatformName, Data: map[string]any{"AccessToken": "test-access-token", "TokenType": "Bearer", "ExternalShopID": "456", "GrantedScopes": "shops_r listings_r listings_w"}}
}

func publicationTestRequest(t *testing.T) types.PublicationRequest {
	t.Helper()
	quantity := 3
	choice := false
	price := decimal.RequireFromString("12.34")
	configuration := ListingConfiguration{SchemaVersion: 1, TaxonomyID: "1", ShippingProfileID: "2", ReadinessStateID: "3", ReturnPolicyID: "4", WhoMade: "someone_else", WhenMade: "made_to_order", IsSupply: &choice, ShouldAutoRenew: &choice, ProductionPartnerIDs: []string{"5"}, Variants: []ListingVariantConfiguration{{LocalVariantID: 11, Quantity: &quantity, PropertyValues: []ListingPropertyValue{}}}}
	data, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	return types.PublicationRequest{Target: types.PublicationTarget{ExternalShopID: "456"}, Product: &types.ProductData{ProductName: "Printed shirt", ExternalShopID: "456", Variants: []types.ProductVariant{{ID: 11, Sku: "SKU-A", Price: &price}}}, Description: "A physical printed shirt.", Configuration: data}
}

func publicationTestProvider(t *testing.T, handler http.HandlerFunc) *Etsy {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := New(Config{APIKey: "key", SharedSecret: "secret", RedirectURL: "https://app.example.test/callback", OAuthBaseURL: "https://www.etsy.com", APIBaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func publicationListingJSON(state string) string {
	return fmt.Sprintf(`{"listing_id":789,"shop_id":456,"state":%q,"url":"https://www.etsy.com/listing/789","images":[{"listing_image_id":900,"rank":1}]}`, state)
}

func TestPublicationCreateDraftDoesNotActivateOrDelete(t *testing.T) {
	var calls int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v3/application/shops/456/listings" {
			t.Errorf("unexpected method/path: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-access-token" || r.Header.Get("x-api-key") != "key:secret" {
			t.Error("credential headers missing")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("state") != "" || r.Form.Get("type") != "physical" || r.Form.Get("quantity") != "3" || r.Form.Get("price") != "12.34" || r.Form.Get("readiness_state_id") != "3" || r.Form.Get("should_auto_renew") != "false" {
			t.Errorf("unexpected draft form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, publicationListingJSON("draft"))
	})
	result, err := provider.CreateDraft(context.Background(), publicationTestCredential(), publicationTestRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.SchemaVersion != 1 || result.State != "draft" || result.ListingID != "789" || result.ExternalShopID != "456" {
		t.Fatalf("unexpected draft result: %+v calls=%d", result, calls)
	}
}

func TestPublicationPreflightFailuresMakeNoRequests(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*types.PublicationRequest, *types.ShopCredential)
	}{
		{"missing configuration", func(r *types.PublicationRequest, c *types.ShopCredential) { r.Configuration = nil }},
		{"missing quantity", func(r *types.PublicationRequest, c *types.ShopCredential) {
			r.Configuration = bytes.Replace(r.Configuration, []byte(`"quantity":3`), []byte(`"quantity":null`), 1)
		}},
		{"missing explicit supply", func(r *types.PublicationRequest, c *types.ShopCredential) {
			r.Configuration = bytes.Replace(r.Configuration, []byte(`"is_supply":false`), []byte(`"is_supply":null`), 1)
		}},
		{"missing return policy", func(r *types.PublicationRequest, c *types.ShopCredential) {
			r.Configuration = bytes.Replace(r.Configuration, []byte(`"return_policy_id":"4"`), []byte(`"return_policy_id":""`), 1)
		}},
		{"unknown configuration field", func(r *types.PublicationRequest, c *types.ShopCredential) {
			r.Configuration = append([]byte(`{"state":"active",`), r.Configuration[1:]...)
		}},
		{"wrong shop credential", func(r *types.PublicationRequest, c *types.ShopCredential) { c.Data["ExternalShopID"] = "999" }},
		{"wrong product shop", func(r *types.PublicationRequest, c *types.ShopCredential) { r.Product.ExternalShopID = "999" }},
		{"missing scope", func(r *types.PublicationRequest, c *types.ShopCredential) {
			c.Data["GrantedScopes"] = "shops_r listings_r"
		}},
		{"known draft ID", func(r *types.PublicationRequest, c *types.ShopCredential) { r.Target.ListingID = "789" }},
		{"price absent", func(r *types.PublicationRequest, c *types.ShopCredential) { r.Product.Variants[0].Price = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) })
			request := publicationTestRequest(t)
			credential := publicationTestCredential()
			test.alter(&request, credential)
			if _, err := provider.CreateDraft(context.Background(), credential, request); err == nil {
				t.Fatal("expected validation failure")
			}
			if calls != 0 {
				t.Fatalf("validation sent %d requests", calls)
			}
		})
	}
}

func TestPublicationWriteFailuresAreNotRetriedAndRedactBody(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		uncertain bool
	}{
		{"server error", 500, `test-access-token key:secret`, true},
		{"request timeout", 408, `test-access-token`, true},
		{"malformed success", 200, `not-json test-access-token`, true},
		{"missing identity", 200, `{"state":"draft"}`, true},
		{"unexpected active", 200, publicationListingJSON("active"), true},
		{"wrong response shop", 200, strings.Replace(publicationListingJSON("draft"), `"shop_id":456`, `"shop_id":999`, 1), true},
		{"denied", 403, `test-access-token key:secret`, false},
		{"invalid input", 400, `test-access-token`, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			_, err := provider.CreateDraft(context.Background(), publicationTestCredential(), publicationTestRequest(t))
			if err == nil {
				t.Fatal("expected failure")
			}
			var unknown *types.PublishResultUncertainError
			if errors.As(err, &unknown) != test.uncertain {
				t.Fatalf("wrong uncertainty classification: %T %v", err, err)
			}
			if calls != 1 {
				t.Fatalf("unexpected retries: %d", calls)
			}
			if strings.Contains(err.Error(), "test-access-token") || strings.Contains(err.Error(), "key:secret") {
				t.Fatal("credential leaked in error")
			}
			if test.status == 403 {
				var status *PublicationHTTPError
				if !errors.As(err, &status) || status.StatusCode != 403 {
					t.Fatal("permission status unavailable")
				}
			}
		})
	}
}

func TestPublicationTimeoutAndCancellationAreBounded(t *testing.T) {
	var calls int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("test transport should replace HTTP") })
	provider.config.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > publicationTimeout {
			t.Error("request has no bounded deadline")
		}
		<-r.Context().Done()
		return nil, errors.New("unsafe injected test-access-token")
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := provider.CreateDraft(ctx, publicationTestCredential(), publicationTestRequest(t))
	var unknown *types.PublishResultUncertainError
	if !errors.As(err, &unknown) || !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("timeout was not uncertain: %v calls=%d", err, calls)
	}
	if strings.Contains(err.Error(), "test-access-token") {
		t.Fatal("transport details leaked")
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = provider.CreateDraft(cancelled, publicationTestCredential(), publicationTestRequest(t))
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled request reached transport: %v calls=%d", err, calls)
	}
}

func TestPublicationRefusesRedirectWithoutCredentialForwardOrReplay(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	_, err := provider.CreateDraft(context.Background(), publicationTestCredential(), publicationTestRequest(t))
	var unknown *types.PublishResultUncertainError
	if !errors.As(err, &unknown) || forwarded.Load() != 0 {
		t.Fatalf("redirect replayed or not uncertain: %v", err)
	}
}

func TestPublicationUpdateCannotActivateAndActivationIsSeparate(t *testing.T) {
	for _, activate := range []bool{false, true} {
		t.Run(fmt.Sprint(activate), func(t *testing.T) {
			var methods []string
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, publicationListingJSON("draft"))
					return
				}
				if r.Method != http.MethodPatch || r.URL.Path != "/v3/application/shops/456/listings/789" {
					t.Errorf("unexpected mutation: %s %s", r.Method, r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if activate {
					if len(r.Form) != 1 || r.Form.Get("state") != "active" {
						t.Errorf("unexpected activation body: %v", r.Form)
					}
				} else if r.Form.Has("state") {
					t.Fatal("ordinary update can activate listing")
				}
				state := "draft"
				if activate {
					state = "active"
				}
				_, _ = io.WriteString(w, publicationListingJSON(state))
			})
			request := publicationTestRequest(t)
			request.Target.ListingID = "789"
			var result *types.PublicationSnapshot
			var err error
			if activate {
				result, err = provider.ActivateListing(context.Background(), publicationTestCredential(), request.Target)
			} else {
				result, err = provider.UpdateListing(context.Background(), publicationTestCredential(), request)
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := "draft"
			if activate {
				expected = "active"
			}
			if result.State != expected || strings.Join(methods, ",") != "GET,PATCH" {
				t.Fatalf("unexpected result: %+v methods=%v", result, methods)
			}
		})
	}
}

func TestPublicationKnownListingOwnershipMismatchBlocksAllWrites(t *testing.T) {
	for _, operation := range []string{"update", "activate", "inventory", "image"} {
		t.Run(operation, func(t *testing.T) {
			var writes int
			provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
				}
				_, _ = io.WriteString(w, strings.Replace(publicationListingJSON("draft"), `"shop_id":456`, `"shop_id":999`, 1))
			})
			request := publicationTestRequest(t)
			request.Target.ListingID = "789"
			var err error
			switch operation {
			case "update":
				_, err = provider.UpdateListing(context.Background(), publicationTestCredential(), request)
			case "activate":
				_, err = provider.ActivateListing(context.Background(), publicationTestCredential(), request.Target)
			case "inventory":
				_, err = provider.ReplaceListingInventory(context.Background(), publicationTestCredential(), request)
			case "image":
				_, err = provider.UploadListingImage(context.Background(), publicationTestCredential(), types.PublicationImageRequest{Target: request.Target, Filename: "image.png", Rank: 1, Content: publicationTestPNG(t)})
			}
			if err == nil || writes != 0 {
				t.Fatalf("ownership mismatch allowed mutation: err=%v writes=%d", err, writes)
			}
		})
	}
}

func publicationTestPNG(t *testing.T) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.White)
	if err := png.Encode(buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPublicationImageUsesBoundedMultipartAndReturnsImageIdentity(t *testing.T) {
	content := publicationTestPNG(t)
	var writes int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, publicationListingJSON("draft"))
			return
		}
		writes++
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/images") {
			t.Fatal("unexpected image write")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := r.FormFile("image")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		actual, err := io.ReadAll(file)
		if err != nil {
			t.Fatal(err)
		}
		if header.Filename != "shirt.png" || !bytes.Equal(actual, content) || r.FormValue("rank") != "1" || r.FormValue("overwrite") != "" {
			t.Fatal("incorrect image multipart")
		}
		_, _ = io.WriteString(w, `{"listing_id":789,"listing_image_id":901,"rank":1}`)
	})
	request := types.PublicationImageRequest{Target: types.PublicationTarget{ExternalShopID: "456", ListingID: "789"}, Filename: "shirt.png", Rank: 1, Content: content}
	result, err := provider.UploadListingImage(context.Background(), publicationTestCredential(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ImageID != "901" || result.Rank != 1 || writes != 1 {
		t.Fatalf("unexpected image result: %+v", result)
	}
	for _, invalid := range []types.PublicationImageRequest{{Target: request.Target, Filename: "a.png", Rank: 1, Content: []byte("not image")}, {Target: request.Target, Filename: "../a.png", Rank: 1, Content: content}, {Target: request.Target, Filename: "a.png", Rank: 1, Content: make([]byte, maxPublicationImageBytes+1)}} {
		if _, err := provider.UploadListingImage(context.Background(), publicationTestCredential(), invalid); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if writes != 1 {
		t.Fatal("invalid image caused a write")
	}
}

func TestPublicationActivationRejectsNonDraftAndMissingImage(t *testing.T) {
	for _, body := range []string{publicationListingJSON("active"), strings.Replace(publicationListingJSON("draft"), `[{"listing_image_id":900,"rank":1}]`, `[]`, 1)} {
		var writes int
		provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				writes++
			}
			_, _ = io.WriteString(w, body)
		})
		_, err := provider.ActivateListing(context.Background(), publicationTestCredential(), types.PublicationTarget{ExternalShopID: "456", ListingID: "789"})
		if err == nil || writes != 0 {
			t.Fatal("invalid activation mutated listing")
		}
	}
}

func TestPublicationInspectRequiresKnownIDAndDoesNotAssumeAbsence(t *testing.T) {
	var calls int
	provider := publicationTestProvider(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusForbidden) })
	target := types.PublicationTarget{ExternalShopID: "456"}
	if _, err := provider.InspectListing(context.Background(), publicationTestCredential(), target); err == nil || calls != 0 {
		t.Fatal("unknown ID inspected")
	}
	target.ListingID = "789"
	_, err := provider.InspectListing(context.Background(), publicationTestCredential(), target)
	var status *PublicationHTTPError
	if !errors.As(err, &status) || status.StatusCode != 403 || calls != 1 {
		t.Fatalf("inspection hid actual permission failure: %v", err)
	}
}
