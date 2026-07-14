package shopify

import (
	"context"
	"testing"
	"time"

	"github.com/flaboy/aira-shop/pkg/types"
)

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
