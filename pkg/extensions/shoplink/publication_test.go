package shoplink

import (
	"net/http"
	"testing"

	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/etsy"
	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/shopify"
)

func TestPublicationRegistryKeepsLegacyProductsSeparate(t *testing.T) {
	previous := platforms
	t.Cleanup(func() { platforms = previous })
	platforms = make(map[string]Platform)
	provider, err := etsy.New(etsy.Config{APIKey: "test", SharedSecret: "test", RedirectURL: "https://example.test/callback", OAuthBaseURL: "https://www.etsy.com", APIBaseURL: "https://openapi.etsy.com", HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(provider); err != nil {
		t.Fatal(err)
	}
	if GetPublicationProvider("etsy") == nil || GetPublicationConfigurationProvider("etsy") == nil {
		t.Fatal("implemented publication protocols unavailable")
	}
	if GetProductProvider("etsy") != nil {
		t.Fatal("Etsy acquired legacy ProductProvider with deletion capability")
	}
	if GetOrderProvider("etsy") != nil || GetFulfillmentProvider("etsy") != nil || GetWebhookProvider("etsy") != nil {
		t.Fatal("unimplemented capability was registered")
	}
	platforms["shopify"] = &shopify.Shopify{}
	if GetProductProvider("shopify") == nil {
		t.Fatal("Shopify legacy product capability changed")
	}
	if GetPublicationProvider("shopify") != nil || GetPublicationConfigurationProvider("shopify") != nil {
		t.Fatal("Shopify was silently converted to the new protocol")
	}
	if GetPublicationProvider("missing") != nil || GetPublicationConfigurationProvider("missing") != nil {
		t.Fatal("missing platform fabricated publication provider")
	}
}
