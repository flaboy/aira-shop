package types

import (
	"github.com/flaboy/aira/aira-shop/pkg/models"
	"github.com/shopspring/decimal"
)

// HandleRequest结果 - 返回授权URL给前端跳转到Shopify
type HandleRequestResult struct {
	AuthURL string `json:"auth_url"`
}

// 定义回调响应类型常量
type CallbackResponseType string

const (
	CallbackResponseTypeShopLinked CallbackResponseType = "shopLinked"
)

// 店铺链接数据结构 - 包含创建的shoplink对象
type ShopLinkedData struct {
	ShopLink *models.ShopLink
}

// HandleCallback响应 - 返回shoplink对象给控制器
type CallbackResponse struct {
	Type           CallbackResponseType
	ShopLinkedData *ShopLinkedData
}

type CommandResult struct {
	Success       bool            `json:"success"`
	Message       string          `json:"message"`
	NewCredential *ShopCredential `json:"new_credential,omitempty"`
}

type PutProductResult struct {
	CommandResult CommandResult `json:"command_result"`
	Url           string        `json:"url"`
	OuterID       string        `json:"outer_id"`
	RemoteData    interface{}   `json:"remote_data"`
}

type ShopCredential struct {
	Platform string                 `json:"platform"`
	Data     map[string]interface{} `json:"data"`
}

func (c *ShopCredential) GetPlatform() string {
	return c.Platform
}

func (c *ShopCredential) GetCredentialData() map[string]interface{} {
	return c.Data
}

func (c *ShopCredential) IsValid() bool {
	return c.Platform != "" && len(c.Data) > 0
}

type ProductData struct {
	ProductName     string           `json:"product_name"`
	BodyHTML        string           `json:"body_html"`
	Tags            string           `json:"tags"`
	Image           ProductImage     `json:"image"`
	Images          []ProductImage   `json:"images"`
	Options         []ProductOption  `json:"options"`
	Variants        []ProductVariant `json:"variants"`
	BusinessContext interface{}      `json:"business_context"`
}

type ProductImage struct {
	Src      string `json:"src"`
	Alt      string `json:"alt"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Filename string `json:"filename"`
}

type ProductOption struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type ProductVariant struct {
	ID             uint             `json:"id"`
	Sku            string           `json:"sku"`
	Title          string           `json:"title"`
	Price          *decimal.Decimal `json:"price"`
	CompareAtPrice *decimal.Decimal `json:"compare_at_price"`
	Weight         *decimal.Decimal `json:"weight"`
	WeightUnit     string           `json:"weight_unit"`
	Option1        string           `json:"option1"`
	Option2        string           `json:"option2"`
	Option3        string           `json:"option3"`
}
