package etsy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/flaboy/aira-shop/pkg/types"
)

const (
	PlatformName    = "etsy"
	maxResponseSize = 1 << 20
)

var (
	RequiredScopes = []string{"shops_r"}
	ErrNoValidShop = errors.New("Etsy account does not have a valid shop")
)

type Config struct {
	APIKey       string
	SharedSecret string
	RedirectURL  string
	OAuthBaseURL string
	APIBaseURL   string
	HTTPClient   *http.Client
}

type Etsy struct {
	config Config
}

func New(config Config) (*Etsy, error) {
	provider := &Etsy{config: config}
	if err := provider.Init(); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *Etsy) Init() error {
	if p.config.HTTPClient == nil || strings.TrimSpace(p.config.APIKey) == "" || strings.TrimSpace(p.config.SharedSecret) == "" || strings.TrimSpace(p.config.OAuthBaseURL) == "" || strings.TrimSpace(p.config.APIBaseURL) == "" {
		return fmt.Errorf("Etsy platform configuration is incomplete")
	}
	redirectURL, err := url.Parse(p.config.RedirectURL)
	if err != nil || redirectURL.Scheme != "https" || redirectURL.Host == "" {
		return fmt.Errorf("Etsy redirect URL must be an absolute HTTPS URL")
	}
	return nil
}

func (p *Etsy) GetPlatformName() string { return PlatformName }

func (p *Etsy) RedirectURL() string { return p.config.RedirectURL }

func (p *Etsy) RequiredScopes() []string {
	return append([]string(nil), RequiredScopes...)
}

func (p *Etsy) AuthorizationURL(state, codeChallenge string) (string, error) {
	authorizeURL, err := url.Parse(strings.TrimRight(p.config.OAuthBaseURL, "/") + "/oauth/connect")
	if err != nil {
		return "", fmt.Errorf("build Etsy authorize URL: %w", err)
	}
	query := authorizeURL.Query()
	query.Set("response_type", "code")
	query.Set("redirect_uri", p.config.RedirectURL)
	query.Set("scope", strings.Join(RequiredScopes, " "))
	query.Set("client_id", p.config.APIKey)
	query.Set("state", state)
	query.Set("code_challenge", codeChallenge)
	query.Set("code_challenge_method", "S256")
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func (p *Etsy) ExchangeAuthorizationCode(ctx context.Context, code, verifier string) (types.OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", p.config.APIKey)
	form.Set("redirect_uri", p.config.RedirectURL)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	return p.requestToken(ctx, "exchange authorization code", form)
}

func (p *Etsy) RefreshOAuthToken(ctx context.Context, refreshToken string) (types.OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", p.config.APIKey)
	form.Set("refresh_token", refreshToken)
	return p.requestToken(ctx, "refresh access token", form)
}

func (p *Etsy) ResolveShopIdentity(ctx context.Context, accessToken string) (types.ShopIdentity, error) {
	userResponse := struct {
		UserID json.Number `json:"user_id"`
	}{}
	if err := p.getJSON(ctx, "/v3/application/users/me", accessToken, &userResponse); err != nil {
		return types.ShopIdentity{}, err
	}
	userID, err := positiveID(userResponse.UserID)
	if err != nil {
		return types.ShopIdentity{}, fmt.Errorf("Etsy user identity is incomplete")
	}
	shopResponse := struct {
		ShopID   json.Number `json:"shop_id"`
		ShopName string      `json:"shop_name"`
		URL      string      `json:"url"`
	}{}
	if err := p.getJSON(ctx, "/v3/application/users/"+url.PathEscape(userID)+"/shops", accessToken, &shopResponse); err != nil {
		var statusErr *HTTPError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return types.ShopIdentity{}, ErrNoValidShop
		}
		return types.ShopIdentity{}, err
	}
	shopID, err := positiveID(shopResponse.ShopID)
	if err != nil {
		return types.ShopIdentity{}, ErrNoValidShop
	}
	identity := types.ShopIdentity{ExternalShopID: shopID, Name: strings.TrimSpace(shopResponse.ShopName), URL: strings.TrimSpace(shopResponse.URL)}
	shopURL, parseErr := url.Parse(identity.URL)
	if identity.Name == "" || parseErr != nil || shopURL.Scheme != "https" || shopURL.Host == "" {
		return types.ShopIdentity{}, ErrNoValidShop
	}
	return identity, nil
}

func (p *Etsy) requestToken(ctx context.Context, operation string, form url.Values) (types.OAuthToken, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.config.APIBaseURL, "/")+"/v3/public/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return types.OAuthToken{}, fmt.Errorf("create Etsy token request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := p.config.HTTPClient.Do(request)
	if err != nil {
		return types.OAuthToken{}, fmt.Errorf("%s with Etsy: %w", operation, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return types.OAuthToken{}, responseError(operation, response)
	}
	token := types.OAuthToken{}
	if err := decodeJSON(response.Body, &token); err != nil {
		return types.OAuthToken{}, fmt.Errorf("decode Etsy token response: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.ExpiresIn <= 0 || !strings.EqualFold(token.TokenType, "Bearer") || strings.TrimSpace(token.Scope) == "" {
		return types.OAuthToken{}, fmt.Errorf("Etsy token response is incomplete")
	}
	return token, nil
}

func (p *Etsy) getJSON(ctx context.Context, path, accessToken string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.config.APIBaseURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("create Etsy API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("x-api-key", p.config.APIKey+":"+p.config.SharedSecret)
	response, err := p.config.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("request Etsy API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return responseError("request Etsy API", response)
	}
	if err := decodeJSON(response.Body, target); err != nil {
		return fmt.Errorf("decode Etsy API response: %w", err)
	}
	return nil
}

type HTTPError struct {
	Operation  string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("Etsy %s failed with status %d: %s", e.Operation, e.StatusCode, e.Body)
}

func responseError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return &HTTPError{Operation: operation, StatusCode: response.StatusCode, Body: strings.TrimSpace(string(body))}
}

func decodeJSON(reader io.Reader, target any) error {
	payload, err := io.ReadAll(io.LimitReader(reader, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(payload) > maxResponseSize {
		return fmt.Errorf("Etsy response exceeds %d bytes", maxResponseSize)
	}
	return json.Unmarshal(payload, target)
}

func positiveID(value json.Number) (string, error) {
	parsed, err := strconv.ParseUint(value.String(), 10, 64)
	if err != nil || parsed == 0 {
		return "", fmt.Errorf("Etsy id must be a positive integer")
	}
	return value.String(), nil
}
