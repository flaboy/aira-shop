package shoplink

import (
	"encoding/json"
	"net/url"

	"github.com/flaboy/aira/aira-shop/pkg/types"
	"github.com/flaboy/pin"
)

type ShopPlatform interface {
	// 处理OAuth回调 - 使用BusinessContext参数
	HandleCallback(c *pin.Context, businessContext json.RawMessage, callbackUrl *url.URL) (*types.CallbackResponse, error)

	// 发布产品到平台 - 使用BusinessContext参数
	PutProduct(credential *types.ShopCredential, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error)

	// 处理公开请求（如OAuth授权）
	HandleRequest(c *pin.Context, path string) (*types.HandleRequestResult, error)

	// 初始化平台
	Init() error

	// 获取平台名称
	GetPlatformName() string
}
