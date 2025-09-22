package utils

import "time"

// TrackingResponse 统一的追踪响应结构
type TrackingResponse struct {
	// 基本信息
	TrackingNumber string `json:"tracking_number"`
	Carrier        string `json:"carrier"`      // 承运商名称 (17track, ups, usps)
	CarrierCode    string `json:"carrier_code"` // 承运商代码
	ServiceType    string `json:"service_type"` // 服务类型

	// 状态信息
	Status        TrackingStatus `json:"status"`
	StatusCode    string         `json:"status_code"`
	StatusMessage string         `json:"status_message"`

	// 地址信息
	Origin      *Address `json:"origin,omitempty"`
	Destination *Address `json:"destination,omitempty"`

	// 时间信息
	ShippedDate           *time.Time `json:"shipped_date,omitempty"`
	EstimatedDeliveryDate *time.Time `json:"estimated_delivery_date,omitempty"`
	ActualDeliveryDate    *time.Time `json:"actual_delivery_date,omitempty"`
	LastUpdated           *time.Time `json:"last_updated,omitempty"`

	// 包裹信息
	Package *PackageInfo `json:"package,omitempty"`

	// 送达信息
	DeliveryInfo *DeliveryInfo `json:"delivery_info,omitempty"`

	// 事件历史
	Events []TrackingEvent `json:"events"`

	// 原始数据引用
	RawData interface{} `json:"raw_data,omitempty"`
}

// TrackingStatus 追踪状态枚举
type TrackingStatus string

const (
	StatusPreTransit     TrackingStatus = "pre_transit"      // 预运输（标签已创建）
	StatusInTransit      TrackingStatus = "in_transit"       // 运输中
	StatusOutForDelivery TrackingStatus = "out_for_delivery" // 派送中
	StatusDelivered      TrackingStatus = "delivered"        // 已送达
	StatusReturnToSender TrackingStatus = "return_to_sender" // 退回发件人
	StatusFailure        TrackingStatus = "failure"          // 配送失败
	StatusCancelled      TrackingStatus = "cancelled"        // 已取消
	StatusException      TrackingStatus = "exception"        // 异常
	StatusUnknown        TrackingStatus = "unknown"          // 未知状态
)

// Address 地址信息
type Address struct {
	Country     string       `json:"country,omitempty"`
	State       string       `json:"state,omitempty"`
	City        string       `json:"city,omitempty"`
	Street      string       `json:"street,omitempty"`
	PostalCode  string       `json:"postal_code,omitempty"`
	Coordinates *Coordinates `json:"coordinates,omitempty"`
}

// Coordinates 坐标信息
type Coordinates struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// PackageInfo 包裹信息
type PackageInfo struct {
	Weight     *Weight     `json:"weight,omitempty"`
	Dimensions *Dimensions `json:"dimensions,omitempty"`
	Pieces     int         `json:"pieces,omitempty"`
	Reference  string      `json:"reference,omitempty"`
}

// Weight 重量信息
type Weight struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"` // kg, lb, oz
}

// Dimensions 尺寸信息
type Dimensions struct {
	Length float64 `json:"length"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Unit   string  `json:"unit"` // cm, in
}

// DeliveryInfo 送达信息
type DeliveryInfo struct {
	DeliveredTo       string `json:"delivered_to,omitempty"`      // 签收人
	DeliveryLocation  string `json:"delivery_location,omitempty"` // 送达位置
	SignatureRequired bool   `json:"signature_required"`          // 是否需要签名
	SignatureImage    string `json:"signature_image,omitempty"`   // 签名图片URL
	DeliveryPhoto     string `json:"delivery_photo,omitempty"`    // 送达照片URL
	DeliveryProof     string `json:"delivery_proof,omitempty"`    // 送达证明
}

// TrackingEvent 追踪事件
type TrackingEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	Status      string    `json:"status"`
	StatusCode  string    `json:"status_code,omitempty"`
	Description string    `json:"description"`
	Location    *Address  `json:"location,omitempty"`
	Details     string    `json:"details,omitempty"`
}
