package shoplink

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/flaboy/pin"
)

type ShopPlatform interface {
	// 处理OAuth回调 - 使用BusinessContext参数
	HandleCallback(c *pin.Context, businessContext json.RawMessage, callbackUrl *url.URL) (*types.CallbackResponse, error)

	// 发布产品到平台 - 使用BusinessContext参数
	PutProduct(credential *types.ShopCredential, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error)

	// 更新平台产品 - 使用BusinessContext参数
	UpdateProduct(credential *types.ShopCredential, outerID string, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error)

	// 从平台删除产品
	DeleteProduct(credential *types.ShopCredential, outerID string) (*types.DeleteProductResult, error)
	AuditProducts(credential *types.ShopCredential, externalShopID string) ([]types.ProductAuditIssue, error)
	CleanupPublishOperation(credential *types.ShopCredential, operationID string) error
	PullOrders(ctx context.Context, credential *types.ShopCredential, request types.OrderPullRequest) (*types.OrderPullResult, error)
	SyncFulfillment(ctx context.Context, credential *types.ShopCredential, fulfillment types.FulfillmentData) (*types.FulfillmentResult, error)
	ProcessWebhook(ctx context.Context, eventID, messageID, topic, shopDomain, deliveryMethod string, payload json.RawMessage) error

	// 处理公开请求（如OAuth授权）
	HandleRequest(c *pin.Context, path string) (*types.HandleRequestResult, error)

	// 初始化平台
	Init() error

	// 获取平台名称
	GetPlatformName() string
}
