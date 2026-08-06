package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	shopifyAdminAPIVersion         = "2026-07"
	tokenExchangeGrantType         = "urn:ietf:params:oauth:grant-type:token-exchange"
	idTokenType                    = "urn:ietf:params:oauth:token-type:id_token"
	offlineAccessTokenType         = "urn:shopify:params:oauth:token-type:offline-access-token"
	maximumShopifyResponseBodySize = 1 << 20
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type TokenExchangeClient struct {
	clientID     string
	clientSecret string
	httpClient   HTTPDoer
	now          func() time.Time
}

type OfflineToken struct {
	AccessToken           string
	RefreshToken          string
	GrantedScopes         []string
	AccessTokenExpiresAt  time.Time
	RefreshTokenExpiresAt time.Time
}

type ShopIdentity struct {
	ExternalShopID string
	Name           string
	ShopDomain     string
}

func NewTokenExchangeClient(clientID, clientSecret string, httpClient HTTPDoer, now func() time.Time) (*TokenExchangeClient, error) {
	if clientID == "" || clientSecret == "" || httpClient == nil || now == nil {
		return nil, fmt.Errorf("Shopify token exchange configuration is incomplete")
	}
	return &TokenExchangeClient{clientID: clientID, clientSecret: clientSecret, httpClient: httpClient, now: now}, nil
}

func (c *TokenExchangeClient) ExchangeExpiringOffline(ctx context.Context, shopDomain, sessionToken string) (OfflineToken, error) {
	return c.exchangeExpiringOffline(ctx, shopDomain, sessionToken, idTokenType)
}

func (c *TokenExchangeClient) MigrateNonExpiringOffline(ctx context.Context, shopDomain, accessToken string) (OfflineToken, error) {
	return c.exchangeExpiringOffline(ctx, shopDomain, accessToken, offlineAccessTokenType)
}

func (c *TokenExchangeClient) RefreshExpiringOffline(ctx context.Context, shopDomain, refreshToken string) (OfflineToken, error) {
	shopDomain, valid := NormalizePermanentShopDomain(shopDomain)
	if !valid || refreshToken == "" {
		return OfflineToken{}, fmt.Errorf("Shopify token refresh input is invalid")
	}
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.requestOfflineToken(ctx, shopDomain, form, "refresh")
}

func (c *TokenExchangeClient) exchangeExpiringOffline(ctx context.Context, shopDomain, subjectToken, subjectTokenType string) (OfflineToken, error) {
	shopDomain, valid := NormalizePermanentShopDomain(shopDomain)
	if !valid || subjectToken == "" || (subjectTokenType != idTokenType && subjectTokenType != offlineAccessTokenType) {
		return OfflineToken{}, fmt.Errorf("Shopify token exchange input is invalid")
	}

	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", tokenExchangeGrantType)
	form.Set("subject_token", subjectToken)
	form.Set("subject_token_type", subjectTokenType)
	form.Set("requested_token_type", offlineAccessTokenType)
	form.Set("expiring", "1")
	return c.requestOfflineToken(ctx, shopDomain, form, "exchange")
}

func (c *TokenExchangeClient) requestOfflineToken(ctx context.Context, shopDomain string, form url.Values, operation string) (OfflineToken, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+shopDomain+"/admin/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return OfflineToken{}, fmt.Errorf("create Shopify token %s request: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	responseBody, statusCode, err := c.execute(request)
	if err != nil {
		return OfflineToken{}, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return OfflineToken{}, fmt.Errorf("Shopify token %s returned HTTP %d", operation, statusCode)
	}

	var payload struct {
		AccessToken           string `json:"access_token"`
		Scope                 string `json:"scope"`
		ExpiresIn             int64  `json:"expires_in"`
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return OfflineToken{}, fmt.Errorf("decode Shopify token %s response: %w", operation, err)
	}
	scopes := normalizeScopes(payload.Scope)
	if payload.AccessToken == "" || payload.RefreshToken == "" || payload.ExpiresIn <= 0 || payload.RefreshTokenExpiresIn <= 0 || len(scopes) == 0 {
		return OfflineToken{}, fmt.Errorf("Shopify token %s response is incomplete", operation)
	}

	now := c.now().UTC()
	return OfflineToken{
		AccessToken:           payload.AccessToken,
		RefreshToken:          payload.RefreshToken,
		GrantedScopes:         scopes,
		AccessTokenExpiresAt:  now.Add(time.Duration(payload.ExpiresIn) * time.Second),
		RefreshTokenExpiresAt: now.Add(time.Duration(payload.RefreshTokenExpiresIn) * time.Second),
	}, nil
}

func (c *TokenExchangeClient) FetchShopIdentity(ctx context.Context, shopDomain, accessToken string) (ShopIdentity, error) {
	shopDomain, valid := NormalizePermanentShopDomain(shopDomain)
	if !valid || accessToken == "" {
		return ShopIdentity{}, fmt.Errorf("Shopify shop identity input is invalid")
	}

	requestBody, err := json.Marshal(map[string]string{"query": `query EffiPrintShopIdentity { shop { id name myshopifyDomain } }`})
	if err != nil {
		return ShopIdentity{}, fmt.Errorf("encode Shopify shop identity request: %w", err)
	}
	endpoint := "https://" + shopDomain + "/admin/api/" + shopifyAdminAPIVersion + "/graphql.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return ShopIdentity{}, fmt.Errorf("create Shopify shop identity request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Shopify-Access-Token", accessToken)

	responseBody, statusCode, err := c.execute(request)
	if err != nil {
		return ShopIdentity{}, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return ShopIdentity{}, fmt.Errorf("Shopify shop identity returned HTTP %d", statusCode)
	}

	var payload struct {
		Data struct {
			Shop struct {
				ID              string `json:"id"`
				Name            string `json:"name"`
				MyshopifyDomain string `json:"myshopifyDomain"`
			} `json:"shop"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return ShopIdentity{}, fmt.Errorf("decode Shopify shop identity response: %w", err)
	}
	if len(payload.Errors) > 0 {
		return ShopIdentity{}, fmt.Errorf("Shopify shop identity returned GraphQL errors")
	}
	returnedDomain, valid := NormalizePermanentShopDomain(payload.Data.Shop.MyshopifyDomain)
	if !valid || returnedDomain != shopDomain || payload.Data.Shop.Name == "" {
		return ShopIdentity{}, fmt.Errorf("Shopify shop identity does not match the session shop")
	}
	externalShopID, valid := shopIDFromGID(payload.Data.Shop.ID)
	if !valid {
		return ShopIdentity{}, fmt.Errorf("Shopify shop identity is invalid")
	}

	return ShopIdentity{ExternalShopID: externalShopID, Name: payload.Data.Shop.Name, ShopDomain: returnedDomain}, nil
}

func (c *TokenExchangeClient) execute(request *http.Request) ([]byte, int, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("execute Shopify request: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumShopifyResponseBodySize+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, 0, fmt.Errorf("read Shopify response: %w", readErr)
	}
	if closeErr != nil {
		return nil, 0, fmt.Errorf("close Shopify response: %w", closeErr)
	}
	if len(body) > maximumShopifyResponseBodySize {
		return nil, 0, fmt.Errorf("Shopify response exceeds the maximum size")
	}
	return body, response.StatusCode, nil
}

func normalizeScopes(value string) []string {
	unique := map[string]struct{}{}
	for _, scope := range strings.Split(value, ",") {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			unique[scope] = struct{}{}
		}
	}
	scopes := make([]string, 0, len(unique))
	for scope := range unique {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
}

func NormalizePermanentShopDomain(shopDomain string) (string, bool) {
	shopDomain = strings.ToLower(strings.TrimSpace(shopDomain))
	const suffix = ".myshopify.com"
	if !strings.HasSuffix(shopDomain, suffix) {
		return "", false
	}
	shopName := strings.TrimSuffix(shopDomain, suffix)
	if shopName == "" || strings.Contains(shopName, ".") || shopName[0] == '-' || shopName[len(shopName)-1] == '-' {
		return "", false
	}
	for _, character := range shopName {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return "", false
		}
	}
	return shopDomain, true
}

func shopIDFromGID(value string) (string, bool) {
	const prefix = "gid://shopify/Shop/"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	numericID := strings.TrimPrefix(value, prefix)
	parsed, err := strconv.ParseUint(numericID, 10, 64)
	if err != nil || parsed == 0 {
		return "", false
	}
	return strconv.FormatUint(parsed, 10), true
}
