package etsy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestEtsyOAuthProviderExchangesTokenAndResolvesShop(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload any
		switch request.URL.Path {
		case "/v3/public/oauth/token":
			if err := request.ParseForm(); err != nil {
				return nil, err
			}
			assertEqual(t, "authorization_code", request.Form.Get("grant_type"))
			assertEqual(t, "verifier", request.Form.Get("code_verifier"))
			payload = map[string]any{"access_token": "123.token", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600, "scope": strings.Join(RequiredScopes, " ")}
		case "/v3/application/users/me":
			assertEqual(t, "Bearer 123.token", request.Header.Get("Authorization"))
			assertEqual(t, "key:secret", request.Header.Get("x-api-key"))
			payload = map[string]any{"user_id": 123}
		case "/v3/application/users/123/shops":
			payload = map[string]any{"shop_id": 456, "shop_name": "Test Shop", "url": "https://www.etsy.com/shop/TestShop"}
		default:
			return nil, fmt.Errorf("unexpected request path: %s", request.URL.Path)
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}, nil
	})}
	provider, err := New(Config{APIKey: "key", SharedSecret: "secret", RedirectURL: "https://app.example.test/stores/callback/etsy", OAuthBaseURL: "https://www.etsy.test", APIBaseURL: "https://api.etsy.test", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}

	authorizationURL, err := provider.AuthorizationURL("state", "challenge")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "S256", parsed.Query().Get("code_challenge_method"))
	assertEqual(t, "shops_r", parsed.Query().Get("scope"))

	token, err := provider.ExchangeAuthorizationCode(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := provider.ResolveShopIdentity(context.Background(), token.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "456", identity.ExternalShopID)
	assertEqual(t, "Test Shop", identity.Name)
}

func assertEqual(t *testing.T, expected, actual string) {
	t.Helper()
	if expected != actual {
		t.Fatalf("expected %q, got %q", expected, actual)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
