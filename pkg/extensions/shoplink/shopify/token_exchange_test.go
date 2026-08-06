package shopify

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type recordingHTTPDoer func(request *http.Request) (*http.Response, error)

func (f recordingHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestExchangeExpiringOfflineUsesOfficialTokenExchangeGrant(t *testing.T) {
	now := time.Date(2026, time.August, 6, 10, 0, 0, 0, time.UTC)
	doer := recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "https://verified-shop.myshopify.com/admin/oauth/access_token", request.URL.String())
		require.Equal(t, "application/x-www-form-urlencoded", request.Header.Get("Content-Type"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		form, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "client-id", form.Get("client_id"))
		require.Equal(t, "client-secret", form.Get("client_secret"))
		require.Equal(t, tokenExchangeGrantType, form.Get("grant_type"))
		require.Equal(t, "session-token", form.Get("subject_token"))
		require.Equal(t, idTokenType, form.Get("subject_token_type"))
		require.Equal(t, offlineAccessTokenType, form.Get("requested_token_type"))
		require.Equal(t, "1", form.Get("expiring"))
		return jsonResponse(http.StatusOK, `{"access_token":"access","scope":"write_products,read_orders,write_products","expires_in":3600,"refresh_token":"refresh","refresh_token_expires_in":7776000}`), nil
	})
	client, err := NewTokenExchangeClient("client-id", "client-secret", doer, func() time.Time { return now })
	require.NoError(t, err)

	token, err := client.ExchangeExpiringOffline(context.Background(), "verified-shop.myshopify.com", "session-token")
	require.NoError(t, err)
	require.Equal(t, "access", token.AccessToken)
	require.Equal(t, "refresh", token.RefreshToken)
	require.Equal(t, []string{"read_orders", "write_products"}, token.GrantedScopes)
	require.Equal(t, now.Add(time.Hour), token.AccessTokenExpiresAt)
	require.Equal(t, now.Add(2160*time.Hour), token.RefreshTokenExpiresAt)
}

func TestMigrateNonExpiringOfflineUsesOfflineTokenAsSubject(t *testing.T) {
	doer := recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		form, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "legacy-offline-token", form.Get("subject_token"))
		require.Equal(t, offlineAccessTokenType, form.Get("subject_token_type"))
		require.Equal(t, offlineAccessTokenType, form.Get("requested_token_type"))
		require.Equal(t, "1", form.Get("expiring"))
		return jsonResponse(http.StatusOK, `{"access_token":"access","scope":"read_orders","expires_in":3600,"refresh_token":"refresh","refresh_token_expires_in":7776000}`), nil
	})
	client, err := NewTokenExchangeClient("client-id", "client-secret", doer, time.Now)
	require.NoError(t, err)

	_, err = client.MigrateNonExpiringOffline(context.Background(), "verified-shop.myshopify.com", "legacy-offline-token")
	require.NoError(t, err)
}

func TestRefreshExpiringOfflineRotatesRefreshToken(t *testing.T) {
	doer := recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		form, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "refresh_token", form.Get("grant_type"))
		require.Equal(t, "current-refresh-token", form.Get("refresh_token"))
		require.Equal(t, "", form.Get("subject_token"))
		return jsonResponse(http.StatusOK, `{"access_token":"new-access","scope":"read_orders","expires_in":3600,"refresh_token":"new-refresh","refresh_token_expires_in":7776000}`), nil
	})
	client, err := NewTokenExchangeClient("client-id", "client-secret", doer, time.Now)
	require.NoError(t, err)

	token, err := client.RefreshExpiringOffline(context.Background(), "verified-shop.myshopify.com", "current-refresh-token")
	require.NoError(t, err)
	require.Equal(t, "new-access", token.AccessToken)
	require.Equal(t, "new-refresh", token.RefreshToken)
}

func TestExchangeExpiringOfflineRejectsIncompleteAndFailedResponses(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "http failure", statusCode: http.StatusBadRequest, body: `{"error":"invalid_subject_token"}`},
		{name: "missing refresh token", statusCode: http.StatusOK, body: `{"access_token":"access","scope":"read_orders","expires_in":3600,"refresh_token_expires_in":7776000}`},
		{name: "invalid json", statusCode: http.StatusOK, body: `{`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client, err := NewTokenExchangeClient("client-id", "client-secret", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
				return jsonResponse(testCase.statusCode, testCase.body), nil
			}), time.Now)
			require.NoError(t, err)
			_, err = client.ExchangeExpiringOffline(context.Background(), "verified-shop.myshopify.com", "session-token")
			require.Error(t, err)
			require.NotContains(t, err.Error(), testCase.body)
		})
	}
}

func TestFetchShopIdentityUsesGraphQLAndRequiresExactSessionShop(t *testing.T) {
	doer := recordingHTTPDoer(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "https://verified-shop.myshopify.com/admin/api/2026-07/graphql.json", request.URL.String())
		require.Equal(t, "access-token", request.Header.Get("X-Shopify-Access-Token"))
		return jsonResponse(http.StatusOK, `{"data":{"shop":{"id":"gid://shopify/Shop/123456","name":"Verified Shop","myshopifyDomain":"verified-shop.myshopify.com"}}}`), nil
	})
	client, err := NewTokenExchangeClient("client-id", "client-secret", doer, time.Now)
	require.NoError(t, err)

	identity, err := client.FetchShopIdentity(context.Background(), "verified-shop.myshopify.com", "access-token")
	require.NoError(t, err)
	require.Equal(t, ShopIdentity{ExternalShopID: "123456", Name: "Verified Shop", ShopDomain: "verified-shop.myshopify.com"}, identity)
}

func TestFetchShopIdentityRejectsMismatchAndGraphQLErrors(t *testing.T) {
	testCases := []string{
		`{"data":{"shop":{"id":"gid://shopify/Shop/123456","name":"Other Shop","myshopifyDomain":"other-shop.myshopify.com"}}}`,
		`{"errors":[{"message":"denied"}]}`,
		`{"data":{"shop":{"id":"invalid","name":"Verified Shop","myshopifyDomain":"verified-shop.myshopify.com"}}}`,
	}
	for _, body := range testCases {
		client, err := NewTokenExchangeClient("client-id", "client-secret", recordingHTTPDoer(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, body), nil
		}), time.Now)
		require.NoError(t, err)
		_, err = client.FetchShopIdentity(context.Background(), "verified-shop.myshopify.com", "access-token")
		require.Error(t, err)
	}
}

func jsonResponse(statusCode int, body string) *http.Response {
	return &http.Response{StatusCode: statusCode, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
