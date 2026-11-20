package shoplink

import (
	"context"
	"errors"
	"time"
)

// OrderSourceProvider 读取外部订单证据，来源身份独立于接收订单的本地店铺。
type OrderSourceProvider interface {
	Platform
	CheckSourceShop(context.Context, OrderSourceShop) (OrderSourceShop, error)
	ListSourcePackages(context.Context, SourcePackageQuery) (SourcePage[SourcePackage], error)
	ReadSourcePackage(context.Context, OrderSourceShop, string) (SourcePackageDetail, error)
	ReadSourceOrder(context.Context, SourceOrderQuery) (SourceOrderEvidence, error)
}

// ErrSourcePermission 标记需要运营修正授权配置的失败。
var ErrSourcePermission = errors.New("source permission denied")

// ErrSourceUncertain 标记外部证据缺失、相互冲突或超出可核对范围。
var ErrSourceUncertain = errors.New("source evidence is uncertain")

type OrderSourceShop struct {
	ShopID      string
	Marketplace string
	SellerID    string
	Name        string
}

type SourcePackageQuery struct {
	Shop       OrderSourceShop
	Page       int
	Start, End time.Time
}

type SourcePage[T any] struct {
	Page, TotalPages, TotalRows int
	Rows                        []T
}

type SourcePackage struct {
	Number, ShopID, Marketplace, Platform, Status string
	Orders                                        []SourcePackageOrder
}

type SourcePackageOrder struct {
	ID, Status, Split, ServiceLevel, BuyerRequestedCancel string
	PurchaseAt, LatestShipAt                              time.Time
}

type SourcePackageDetail struct {
	SourcePackage
	Resend, Reship, OutOfStock      string
	Warehouse, Channel              string
	TrackingNumber, ForwardNo       string
	ShipTime, ApplyTime, SubmitTime string
	Address                         SourceAddress
	Items                           []SourcePackageItem
}

type SourceAddress struct {
	Name, Country, CountryCode, PostalCode, StateOrRegion, City        string
	Address1, Address2, ApartmentNumber, Company, Phone, Mobile, Email string
}

type SourcePackageItem struct {
	OrderID, OrderItemID, SellerSKU, CommoditySKU, Quantity, Group, CustomMark string
	PackageID                                                                  int64
	HasCustomURL, HasCustomOptions, HasChildren                                bool
}

type SourceOrderQuery struct {
	Shop          OrderSourceShop
	AmazonOrderID string
	PurchaseAt    time.Time
}

// SourceOrderEvidence 来自独立原单接口和按原单号完整翻页的 FBM 包裹查询。
type SourceOrderEvidence struct {
	ID, ShopID, Marketplace, Status, Fulfillment              string
	BuyerRequestedCancel, Replacement, OrderFlag, CustomOrder string
	PurchaseAt, UpdatedAt, LatestShipAt                       time.Time
	Items                                                     []SourceOrderItem
	DetailPackages, ListedPackages                            []SourceOrderPackage
}

type SourceOrderItem struct {
	ID, SellerSKU, CommoditySKU, Quantity, QuantityShipped, QuantityUnfulfillable string
}

type SourceOrderPackage struct {
	Number, Status, Warehouse, Channel string
}
