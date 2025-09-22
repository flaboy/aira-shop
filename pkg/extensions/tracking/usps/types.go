package usps

// 本包没有启用
//https://www.usps.com/business/web-tools-apis/track-and-confirm-api.htm#_Toc188442122

// TrackRequest 表示USPS查询包裹状态的请求
type TrackRequest struct {
	USERID   string    `xml:"USERID,attr"`
	PASSWORD string    `xml:"PASSWORD,attr,omitempty"`
	TrackIDs []TrackID `xml:"TrackID"`
}

// TrackID 表示要跟踪的包裹ID
type TrackID struct {
	ID string `xml:"ID,attr"`
}

// TrackFieldRequest 表示USPS更详细的包裹查询请求
type TrackFieldRequest struct {
	USERID   string  `xml:"USERID,attr"`
	PASSWORD string  `xml:"PASSWORD,attr,omitempty"`
	Revision int     `xml:"Revision"`
	ClientIp string  `xml:"ClientIp"`
	SourceId string  `xml:"SourceId"`
	TrackID  TrackID `xml:"TrackID"`
}

// uspsResponse 包含USPS跟踪请求的响应
type TrackingResponse struct {
	TrackInfo []TrackInfo `xml:"TrackInfo"`
}

// TrackInfo 包含单个包裹的跟踪信息
type TrackInfo struct {
	ID                     string        `xml:"ID,attr"`
	Class                  string        `xml:"Class,omitempty"`
	ClassOfMailCode        string        `xml:"ClassOfMailCode,omitempty"`
	DestinationCity        string        `xml:"DestinationCity,omitempty"`
	DestinationState       string        `xml:"DestinationState,omitempty"`
	DestinationZip         string        `xml:"DestinationZip,omitempty"`
	EmailEnabled           bool          `xml:"EmailEnabled,omitempty"`
	KahalaIndicator        bool          `xml:"KahalaIndicator,omitempty"`
	MailTypeCode           string        `xml:"MailTypeCode,omitempty"`
	MPDATE                 string        `xml:"MPDATE,omitempty"`
	MPSUFFIX               string        `xml:"MPSUFFIX,omitempty"`
	OriginCity             string        `xml:"OriginCity,omitempty"`
	OriginState            string        `xml:"OriginState,omitempty"`
	OriginZip              string        `xml:"OriginZip,omitempty"`
	PodEnabled             bool          `xml:"PodEnabled,omitempty"`
	TPodEnabled            bool          `xml:"TPodEnabled,omitempty"`
	RestoreEnabled         bool          `xml:"RestoreEnabled,omitempty"`
	RramEnabled            bool          `xml:"RramEnabled,omitempty"`
	RreEnabled             bool          `xml:"RreEnabled,omitempty"`
	Service                []string      `xml:"Service,omitempty"`
	ServiceTypeCode        string        `xml:"ServiceTypeCode,omitempty"`
	Status                 string        `xml:"Status,omitempty"`
	StatusCategory         string        `xml:"StatusCategory,omitempty"`
	StatusSummary          string        `xml:"StatusSummary,omitempty"`
	TABLECODE              string        `xml:"TABLECODE,omitempty"`
	TrackSummary           EventDetail   `xml:"TrackSummary"`
	TrackDetail            []EventDetail `xml:"TrackDetail,omitempty"`
	ExpectedDeliveryDate   string        `xml:"ExpectedDeliveryDate,omitempty"`
	ExpectedDeliveryTime   string        `xml:"ExpectedDeliveryTime,omitempty"`
	GuaranteedDeliveryDate string        `xml:"GuaranteedDeliveryDate,omitempty"`
	Error                  *Error        `xml:"Error,omitempty"`
}

// EventDetail 表示包裹跟踪事件的详细信息
type EventDetail struct {
	EventTime             string `xml:"EventTime,omitempty"`
	EventDate             string `xml:"EventDate,omitempty"`
	Event                 string `xml:"Event,omitempty"`
	EventCity             string `xml:"EventCity,omitempty"`
	EventState            string `xml:"EventState,omitempty"`
	EventZIPCode          string `xml:"EventZIPCode,omitempty"`
	EventCountry          string `xml:"EventCountry,omitempty"`
	FirmName              string `xml:"FirmName,omitempty"`
	Name                  string `xml:"Name,omitempty"`
	AuthorizedAgent       bool   `xml:"AuthorizedAgent,omitempty"`
	EventCode             string `xml:"EventCode,omitempty"`
	DeliveryAttributeCode string `xml:"DeliveryAttributeCode,omitempty"`
	GMT                   string `xml:"GMT,omitempty"`
	GMTOffset             string `xml:"GMTOffset,omitempty"`
}

// Error 表示跟踪请求的错误信息
type Error struct {
	Number      string `xml:"Number,omitempty"`
	Description string `xml:"Description,omitempty"`
	HelpFile    string `xml:"HelpFile,omitempty"`
	HelpContext string `xml:"HelpContext,omitempty"`
}
