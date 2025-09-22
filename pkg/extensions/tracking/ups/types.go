package ups

// https://developer.ups.com/tag/Tracking?loc=en_US#operation/Reference%20Tracking%20API

// upstrack 包含UPS追踪相关的类型定义
type TrackingResponse struct {
	TrackResponse TrackResponse `json:"trackResponse"`
}

// TrackResponse 包含追踪信息的主要容器
type TrackResponse struct {
	Shipments []Shipment `json:"shipment"`
}

// Shipment 表示一个运输项目
type Shipment struct {
	InquiryNumber string    `json:"inquiryNumber"`
	Packages      []Package `json:"package"`
	UserRelation  string    `json:"userRelation"`
	Warnings      []Warning `json:"warnings"`
}

// Warning 表示追踪过程中的警告信息
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Package 表示单个包裹的详细信息
type Package struct {
	AccessPointInformation   AccessPointInformation    `json:"accessPointInformation"`
	Activities               []Activity                `json:"activity"`
	AdditionalAttributes     []string                  `json:"additionalAttributes"`
	AdditionalServices       []string                  `json:"additionalServices"`
	AlternateTrackingNumbers []AlternateTrackingNumber `json:"alternateTrackingNumber"`
	CurrentStatus            CurrentStatus             `json:"currentStatus"`
	DeliveryDates            []DeliveryDate            `json:"deliveryDate"`
	DeliveryInformation      DeliveryInformation       `json:"deliveryInformation"`
	DeliveryTime             DeliveryTime              `json:"deliveryTime"`
	Dimension                Dimension                 `json:"dimension"`
	IsSmartPackage           bool                      `json:"isSmartPackage"`
	Milestones               []Milestone               `json:"milestones"`
	PackageAddresses         []PackageAddress          `json:"packageAddress"`
	PackageCount             int                       `json:"packageCount"`
	PaymentInformation       []PaymentInfo             `json:"paymentInformation"`
	ReferenceNumbers         []ReferenceNumber         `json:"referenceNumber"`
	Service                  Service                   `json:"service"`
	StatusCode               string                    `json:"statusCode"`
	StatusDescription        string                    `json:"statusDescription"`
	SuppressionIndicators    string                    `json:"suppressionIndicators"`
	TrackingNumber           string                    `json:"trackingNumber"`
	UcixStatus               string                    `json:"ucixStatus"`
	Weight                   Weight                    `json:"weight"`
}

// AccessPointInformation 表示取件点信息
type AccessPointInformation struct {
	PickupByDate string `json:"pickupByDate"`
}

// Activity 表示包裹的活动记录
type Activity struct {
	Date      *string `json:"date"`
	GMTDate   *string `json:"gmtDate"`
	GMTOffset *string `json:"gmtOffset"`
	GMTTime   *string `json:"gmtTime"`
	Location  *string `json:"location"`
	Status    *string `json:"status"`
	Time      *string `json:"time"`
}

// AlternateTrackingNumber 表示替代追踪号码
type AlternateTrackingNumber struct {
	Number *string `json:"number"`
	Type   *string `json:"type"`
}

// CurrentStatus 表示包裹当前状态
type CurrentStatus struct {
	Code                      string `json:"code"`
	Description               string `json:"description"`
	SimplifiedTextDescription string `json:"simplifiedTextDescription"`
	StatusCode                string `json:"statusCode"`
	Type                      string `json:"type"`
}

// DeliveryDate 表示预计送达日期
type DeliveryDate struct {
	Date *string `json:"date"`
	Type *string `json:"type"`
}

// DeliveryInformation 表示送达信息
type DeliveryInformation struct {
	DeliveryPhoto DeliveryPhoto `json:"deliveryPhoto"`
	Location      string        `json:"location"`
	ReceivedBy    string        `json:"receivedBy"`
	Signature     Signature     `json:"signature"`
	Pod           Pod           `json:"pod"`
}

// DeliveryPhoto 表示送达照片信息
type DeliveryPhoto struct {
	IsNonPostalCodeCountry *string `json:"isNonPostalCodeCountry"`
	Photo                  *string `json:"photo"`
	PhotoCaptureInd        *string `json:"photoCaptureInd"`
	PhotoDispositionCode   *string `json:"photoDispositionCode"`
}

// Signature 表示签收信息
type Signature struct {
	Image *string `json:"image"`
}

// Pod 表示送达证明
type Pod struct {
	Content *string `json:"content"`
}

// DeliveryTime 表示送达时间
type DeliveryTime struct {
	EndTime   string `json:"endTime"`
	StartTime string `json:"startTime"`
	Type      string `json:"type"`
}

// Dimension 表示包裹尺寸
type Dimension struct {
	Height          string `json:"height"`
	Length          string `json:"length"`
	UnitOfDimension string `json:"unitOfDimension"`
	Width           string `json:"width"`
}

// Milestone 表示包裹运输过程中的里程碑
type Milestone struct {
	Category       *string `json:"category"`
	Code           *string `json:"code"`
	Current        *bool   `json:"current"`
	Description    *string `json:"description"`
	LinkedActivity *string `json:"linkedActivity"`
	State          *string `json:"state"`
	SubMilestone   *string `json:"subMilestone"`
}

// PackageAddress 表示包裹地址
type PackageAddress struct {
	Address       *string `json:"address"`
	AttentionName *string `json:"attentionName"`
	Name          *string `json:"name"`
	Type          *string `json:"type"`
}

// PaymentInfo 表示支付信息
type PaymentInfo struct {
	Amount        *string `json:"amount"`
	Currency      *string `json:"currency"`
	ID            *string `json:"id"`
	Paid          *bool   `json:"paid"`
	PaymentMethod *string `json:"paymentMethod"`
	Type          *string `json:"type"`
}

// ReferenceNumber 表示参考编号
type ReferenceNumber struct {
	Number *string `json:"number"`
	Type   *string `json:"type"`
}

// Service 表示服务类型
type Service struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	LevelCode   string `json:"levelCode"`
}

// Weight 表示包裹重量
type Weight struct {
	UnitOfMeasurement string `json:"unitOfMeasurement"`
	Weight            string `json:"weight"`
}
