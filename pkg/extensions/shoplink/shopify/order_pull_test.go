package shopify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-shop/pkg/types"
)

type orderPullRoundTripFunc func(*http.Request) (*http.Response, error)

func (f orderPullRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type orderPullGraphQLStub struct {
	pages []shopifyOrdersResponse
	vars  []map[string]any
}

func (s *orderPullGraphQLStub) Query(_ context.Context, _ string, variables, response interface{}) error {
	s.vars = append(s.vars, variables.(map[string]any))
	page := s.pages[0]
	s.pages = s.pages[1:]
	*response.(*shopifyOrdersResponse) = page
	return nil
}

func TestShopifyOrderSearchQueryOnlyRequestsPaidWindow(t *testing.T) {
	request := types.OrderPullRequest{
		Since: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 7, 14, 12, 30, 0, 0, time.UTC),
	}
	query := shopifyOrderSearchQuery(request)
	expected := "financial_status:paid created_at:>='2026-05-15T00:00:00Z' created_at:<='2026-07-14T12:30:00Z'"
	if query != expected {
		t.Fatalf("Paid 拉取查询错误：%s", query)
	}
}

func TestFetchShopifyPaidOrderNodesUsesCursorPagination(t *testing.T) {
	stub := &orderPullGraphQLStub{pages: []shopifyOrdersResponse{
		{Orders: shopifyOrderConnection{
			Edges:    []shopifyOrderEdge{{Cursor: "cursor-1", Node: shopifyOrderNode{LegacyResourceID: "1001"}}},
			PageInfo: shopifyPageInfo{HasNextPage: true, EndCursor: "cursor-1"},
		}},
		{Orders: shopifyOrderConnection{
			Edges:    []shopifyOrderEdge{{Cursor: "cursor-2", Node: shopifyOrderNode{LegacyResourceID: "1002"}}},
			PageInfo: shopifyPageInfo{HasNextPage: false, EndCursor: "cursor-2"},
		}},
	}}
	request := types.OrderPullRequest{Since: time.Unix(100, 0).UTC(), Until: time.Unix(200, 0).UTC()}

	nodes, err := fetchShopifyPaidOrderNodes(context.Background(), stub, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].LegacyResourceID != "1001" || nodes[1].LegacyResourceID != "1002" {
		t.Fatalf("分页结果错误：%+v", nodes)
	}
	if stub.vars[0]["after"] != nil || stub.vars[1]["after"] != "cursor-1" {
		t.Fatalf("游标变量错误：%+v", stub.vars)
	}
	if stub.vars[0]["first"] != shopifyPaidOrdersPageSize {
		t.Fatalf("订单分页大小错误：%+v", stub.vars[0])
	}
}

func TestPullOrdersUsesExplicitShopifyAPIVersion(t *testing.T) {
	previousApp := app
	app = &goshopify.App{}
	t.Cleanup(func() { app = previousApp })

	client := &http.Client{Transport: orderPullRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		expectedPath := "/admin/api/2026-04/graphql.json"
		if request.URL.Path != expectedPath {
			t.Fatalf("Shopify 拉单 API 路径错误：得到 %s，期望 %s", request.URL.Path, expectedPath)
		}
		payload := struct {
			Query string `json:"query"`
		}{}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Query, "lineItems(first: 250) {\n          nodes {\n            id") {
			t.Fatalf("Shopify 拉单查询没有使用 LineItem.id：%s", payload.Query)
		}
		if strings.Contains(payload.Query, "customer {") {
			t.Fatalf("Shopify 拉单查询不应额外要求 read_customers：%s", payload.Query)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"data":{"orders":{"edges":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`)),
			Request:    request,
		}, nil
	})}
	platform := &Shopify{httpClient: client}

	result, err := platform.PullOrders(context.Background(), &types.ShopCredential{Data: map[string]any{
		"Url":         "example.myshopify.com",
		"AccessToken": "token",
	}}, types.OrderPullRequest{Since: time.Unix(100, 0).UTC(), Until: time.Unix(200, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || len(result.Orders) != 0 {
		t.Fatalf("空订单响应结果错误：%+v", result)
	}
}

func TestShopifyLineItemLegacyID(t *testing.T) {
	tests := []struct {
		name     string
		gid      string
		expected string
		wantErr  bool
	}{
		{name: "有效行项目", gid: "gid://shopify/LineItem/123456", expected: "123456"},
		{name: "错误资源类型", gid: "gid://shopify/Product/123456", wantErr: true},
		{name: "缺少数字ID", gid: "gid://shopify/LineItem/", wantErr: true},
		{name: "非数字ID", gid: "gid://shopify/LineItem/invalid", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := shopifyLineItemLegacyID(test.gid)
			if test.wantErr {
				if err == nil {
					t.Fatalf("期望解析失败，实际得到 %s", actual)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if actual != test.expected {
				t.Fatalf("行项目 ID 错误：得到 %s，期望 %s", actual, test.expected)
			}
		})
	}
}
