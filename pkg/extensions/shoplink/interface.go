package shoplink

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/flaboy/pin"
)

// Platform 描述所有电商平台实现都必须提供的基础能力。
type Platform interface {
	Init() error
	GetPlatformName() string
}

// AuthorizationProvider 处理依赖 Web Context 的既有平台授权流程。
// Shopify 当前使用该能力；Redirect OAuth 平台使用 OAuthProvider。
type AuthorizationProvider interface {
	Platform

	// 处理OAuth回调 - 使用BusinessContext参数
	HandleCallback(c *pin.Context, businessContext json.RawMessage, callbackUrl *url.URL) (*types.CallbackResponse, error)

	// 处理公开请求（如OAuth授权）
	HandleRequest(c *pin.Context, path string) (*types.HandleRequestResult, error)
}

// OAuthProvider 提供 Redirect OAuth 平台的协议能力，不持有业务用户、Store 或数据库归属。
type OAuthProvider interface {
	Platform
	AuthorizationURL(state, codeChallenge string) (string, error)
	RedirectURL() string
	RequiredScopes() []string
	ExchangeAuthorizationCode(ctx context.Context, code, verifier string) (types.OAuthToken, error)
	RefreshOAuthToken(ctx context.Context, refreshToken string) (types.OAuthToken, error)
	ResolveShopIdentity(ctx context.Context, accessToken string) (types.ShopIdentity, error)
}

// ScopedOAuthProvider 只接受平台认可的 scope 组合；不改变既有最小授权接口。
type ScopedOAuthProvider interface {
	OAuthProvider
	AuthorizationURLForScopes(state, codeChallenge string, scopes []string) (string, error)
}

type ProductProvider interface {
	Platform

	// 发布产品到平台 - 使用BusinessContext参数
	PutProduct(credential *types.ShopCredential, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error)

	// 更新平台产品 - 使用BusinessContext参数
	UpdateProduct(credential *types.ShopCredential, outerID string, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error)

	// 从平台删除产品
	DeleteProduct(credential *types.ShopCredential, outerID string) (*types.DeleteProductResult, error)
	AuditProducts(credential *types.ShopCredential, externalShopID string) ([]types.ProductAuditIssue, error)
	CleanupPublishOperation(credential *types.ShopCredential, operationID string) error
}

// PublicationProvider 支持分步、可取消的发布协议；不要求远端删除能力。
// 每一步均由 service 持久化事实后继续，未知写入不得直接重发。
type PublicationProvider interface {
	Platform
	CreateDraft(context.Context, *types.ShopCredential, types.PublicationRequest) (*types.PublicationSnapshot, error)
	UpdateListing(context.Context, *types.ShopCredential, types.PublicationRequest) (*types.PublicationSnapshot, error)
	UploadListingImage(context.Context, *types.ShopCredential, types.PublicationImageRequest) (*types.PublicationImage, error)
	ReplaceListingInventory(context.Context, *types.ShopCredential, types.PublicationRequest) ([]types.PublicationVariantMapping, error)
	InspectListingInventory(context.Context, *types.ShopCredential, types.PublicationRequest) ([]types.PublicationVariantMapping, error)
	ActivateListing(context.Context, *types.ShopCredential, types.PublicationTarget) (*types.PublicationSnapshot, error)
	InspectListing(context.Context, *types.ShopCredential, types.PublicationTarget) (*types.PublicationSnapshot, error)
}

// PublicationConfigurationProvider 只读取当前店铺可选择的配置，不修改全局设置。
type PublicationConfigurationProvider interface {
	Platform
	QueryPublicationConfiguration(context.Context, *types.ShopCredential, types.PublicationConfigurationQuery) (*types.PublicationConfigurationResult, error)
}

type OrderProvider interface {
	Platform
	PullOrders(ctx context.Context, credential *types.ShopCredential, request types.OrderPullRequest) (*types.OrderPullResult, error)
}

type FulfillmentProvider interface {
	Platform
	SyncFulfillment(ctx context.Context, credential *types.ShopCredential, fulfillment types.FulfillmentData) (*types.FulfillmentResult, error)
}

type WebhookProvider interface {
	Platform
	ProcessWebhook(ctx context.Context, eventID, messageID, topic, shopDomain, deliveryMethod string, payload json.RawMessage) error
}

// ShopPlatform 保留为完整能力组合，用于编译期验证完整平台实现；Registry 不再要求所有平台实现该接口。
type ShopPlatform interface {
	AuthorizationProvider
	ProductProvider
	OrderProvider
	FulfillmentProvider
	WebhookProvider
}
