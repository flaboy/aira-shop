package shoplink

import (
	"context"
	"testing"

	"github.com/flaboy/aira-shop/pkg/types"
)

type testOAuthPlatform struct {
	initialized bool
}

func (p *testOAuthPlatform) Init() error {
	p.initialized = true
	return nil
}

func (p *testOAuthPlatform) GetPlatformName() string { return "oauth-test" }
func (p *testOAuthPlatform) AuthorizationURL(string, string) (string, error) {
	return "https://example.test/oauth", nil
}
func (p *testOAuthPlatform) RedirectURL() string      { return "https://example.test/callback" }
func (p *testOAuthPlatform) RequiredScopes() []string { return []string{"shops_r"} }
func (p *testOAuthPlatform) ExchangeAuthorizationCode(context.Context, string, string) (types.OAuthToken, error) {
	return types.OAuthToken{}, nil
}
func (p *testOAuthPlatform) RefreshOAuthToken(context.Context, string) (types.OAuthToken, error) {
	return types.OAuthToken{}, nil
}
func (p *testOAuthPlatform) ResolveShopIdentity(context.Context, string) (types.ShopIdentity, error) {
	return types.ShopIdentity{}, nil
}

func TestRegisterAllowsPartialPlatformCapabilities(t *testing.T) {
	previousPlatforms := platforms
	t.Cleanup(func() { platforms = previousPlatforms })
	platforms = nil
	provider := &testOAuthPlatform{}
	if err := Register(provider); err != nil {
		t.Fatal(err)
	}
	if !provider.initialized {
		t.Fatal("expected platform initialization")
	}
	if GetOAuthProvider(provider.GetPlatformName()) == nil {
		t.Fatal("expected OAuth provider capability")
	}
	if GetProductProvider(provider.GetPlatformName()) != nil {
		t.Fatal("unexpected product provider capability")
	}
	if err := Register(provider); err == nil {
		t.Fatal("expected duplicate platform registration to fail")
	}
}
