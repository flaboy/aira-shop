package types

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type OrderFulfillmentStatus string

const (
	OrderFulfillmentStatusShipped     OrderFulfillmentStatus = "shipped"
	OrderFulfillmentStatusPartial     OrderFulfillmentStatus = "partial"
	OrderFulfillmentStatusUnshipped   OrderFulfillmentStatus = "unshipped"
	OrderFulfillmentStatusAny         OrderFulfillmentStatus = "any"
	OrderFulfillmentStatusUnfulfilled OrderFulfillmentStatus = "unfulfilled"
	OrderFulfillmentStatusFulfilled   OrderFulfillmentStatus = "fulfilled"
)

type OrderFinancialStatus string

const (
	OrderFinancialStatusAuthorized        OrderFinancialStatus = "authorized"
	OrderFinancialStatusPending           OrderFinancialStatus = "pending"
	OrderFinancialStatusPaid              OrderFinancialStatus = "paid"
	OrderFinancialStatusPartiallyPaid     OrderFinancialStatus = "partially_paid"
	OrderFinancialStatusRefunded          OrderFinancialStatus = "refunded"
	OrderFinancialStatusVoided            OrderFinancialStatus = "voided"
	OrderFinancialStatusPartiallyRefunded OrderFinancialStatus = "partially_refunded"
	OrderFinancialStatusAny               OrderFinancialStatus = "any"
	OrderFinancialStatusUnpaid            OrderFinancialStatus = "unpaid"
)

type ShopConnectedEvent struct {
	ShopID          uint                   `json:"shop_id"`
	Platform        string                 `json:"platform"`
	ShopData        map[string]interface{} `json:"shop_data"`
	BusinessContext json.RawMessage        `json:"business_context"`
	CreatedAt       time.Time              `json:"created_at"`
}

type ProductPublishedEvent struct {
	ShopProductID   uint                   `json:"shop_product_id"`
	ShopID          uint                   `json:"shop_id"`
	Platform        string                 `json:"platform"`
	OuterID         string                 `json:"outer_id"`
	ProductData     map[string]interface{} `json:"product_data"`
	BusinessContext json.RawMessage        `json:"business_context"`
	CreatedAt       time.Time              `json:"created_at"`
}

type OrderData struct {
	ID                string                 `json:"id"`                 // 平台订单ID
	Name              string                 `json:"name"`               // 订单编号
	Email             string                 `json:"email"`              // 客户邮箱
	Phone             string                 `json:"phone"`              // 客户电话
	FinancialStatus   OrderFinancialStatus   `json:"financial_status"`   // 支付状态
	FulfillmentStatus OrderFulfillmentStatus `json:"fulfillment_status"` // 配送状态
	CreatedAt         *time.Time             `json:"created_at"`         // 创建时间
	UpdatedAt         *time.Time             `json:"updated_at"`         // 更新时间
	TotalPrice        *decimal.Decimal       `json:"total_price"`        // 总价
	SubtotalPrice     *decimal.Decimal       `json:"subtotal_price"`     // 商品总价
	TotalShipping     *decimal.Decimal       `json:"total_shipping"`     // 运费
	TotalTax          *decimal.Decimal       `json:"total_tax"`          // 税费
	Currency          string                 `json:"currency"`           // 货币
	Customer          *OrderCustomer         `json:"customer"`           // 客户信息
	ShippingAddress   *OrderAddress          `json:"shipping_address"`   // 收货地址
	BillingAddress    *OrderAddress          `json:"billing_address"`    // 账单地址
	LineItems         []OrderLineItem        `json:"line_items"`         // 订单行项目
	ShippingLines     []OrderShippingLine    `json:"shipping_lines"`     // 配送方式
	RawData           map[string]interface{} `json:"raw_data"`           // 原始数据
}

type OrderCustomer struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

type OrderAddress struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Address1     string `json:"address1"`
	Address2     string `json:"address2"`
	City         string `json:"city"`
	Province     string `json:"province"`
	ProvinceCode string `json:"province_code"`
	Country      string `json:"country"`
	CountryCode  string `json:"country_code"`
	Zip          string `json:"zip"`
	Phone        string `json:"phone"`
	Company      string `json:"company"`
}

type OrderLineItem struct {
	ID           string                 `json:"id"`
	ProductID    string                 `json:"product_id"`
	VariantID    uint                   `json:"variant_id"`
	Title        string                 `json:"title"`
	SKU          string                 `json:"sku"`
	Quantity     int                    `json:"quantity"`
	Price        *decimal.Decimal       `json:"price"`
	Properties   map[string]string      `json:"properties"`
	VariantTitle string                 `json:"variant_title"`
	RawData      map[string]interface{} `json:"raw_data"`
}

type OrderShippingLine struct {
	Code      string           `json:"code"`
	Title     string           `json:"title"`
	Price     *decimal.Decimal `json:"price"`
	Source    string           `json:"source"`
	Carrier   string           `json:"carrier"`
	CarrierID string           `json:"carrier_id"`
}

type OrderReceivedEvent struct {
	Platform  string    `json:"platform"`
	OrderData OrderData `json:"order_data"`
	ShopID    uint      `json:"shop_id"`
	CreatedAt time.Time `json:"created_at"`
}

type PaymentCompletedEvent struct {
	TX              *gorm.DB
	PaymentHashID   string           `json:"payment_hash_id"`
	Channel         string           `json:"channel"` // paypal, stripe等
	Amount          *decimal.Decimal `json:"amount"`
	Currency        string           `json:"currency"`
	ExternalOrderID string           `json:"external_order_id"`
	BusinessContext json.RawMessage  `json:"business_context"`
	CompletedAt     time.Time        `json:"completed_at"`
}
