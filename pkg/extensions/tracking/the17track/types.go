package the17track

// https://api.17track.net/en/doc?version=v2.2&anchor=webhook

type TrackEvent struct {
	Event string        `json:"event"`
	Data  TrackResponse `json:"data"`
}

// 17Track API response structure
type TrackResponse struct {
	Number    string    `json:"number"`
	Carrier   int       `json:"carrier"`
	Param     string    `json:"param"`
	Tag       string    `json:"tag"`
	TrackInfo TrackInfo `json:"track_info"`
}

// 17Track API register request structure
type RegisterRequest struct {
	Number string `json:"number"`
}

type RegisterResponse struct {
	Code int `json:"code"`
	Data struct {
		Accepted []struct {
			Number string `json:"number"`
		} `json:"accepted"`
		Rejected []struct {
			Number string `json:"number"`
			Error  struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"rejected"`
	} `json:"data"`
}

type TrackInfo struct {
	ShippingInfo ShippingInfo `json:"shipping_info"`
	LatestStatus Status       `json:"latest_status"`
	LatestEvent  Event        `json:"latest_event"`
	TimeMetrics  TimeMetrics  `json:"time_metrics"`
	Milestone    []Milestone  `json:"milestone"`
	MiscInfo     MiscInfo     `json:"misc_info"`
	Tracking     Tracking     `json:"tracking"`
}

type ShippingInfo struct {
	ShipperAddress   Address `json:"shipper_address"`
	RecipientAddress Address `json:"recipient_address"`
}

type Address struct {
	Country     string      `json:"country"`
	State       string      `json:"state"`
	City        string      `json:"city"`
	Street      string      `json:"street"`
	PostalCode  string      `json:"postal_code"`
	Coordinates Coordinates `json:"coordinates"`
}

type Coordinates struct {
	Longitude string `json:"longitude"`
	Latitude  string `json:"latitude"`
}

type Status struct {
	Status        string `json:"status"`
	SubStatus     string `json:"sub_status"`
	SubStatusDesc string `json:"sub_status_descr"`
}

type Event struct {
	TimeISO              string               `json:"time_iso"`
	TimeUTC              string               `json:"time_utc"`
	TimeRaw              TimeRaw              `json:"time_raw"`
	Description          string               `json:"description"`
	DescriptionTranslate DescriptionTranslate `json:"description_translation"`
	Location             string               `json:"location"`
	Stage                string               `json:"stage"`
	SubStatus            string               `json:"sub_status"`
	Address              Address              `json:"address"`
}

type TimeRaw struct {
	Date     string `json:"date"`
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
}

type DescriptionTranslate struct {
	Lang        string `json:"lang"`
	Description string `json:"description"`
}

type TimeMetrics struct {
	DaysAfterOrder        int                   `json:"days_after_order"`
	DaysOfTransit         int                   `json:"days_of_transit"`
	DaysOfTransitDone     int                   `json:"days_of_transit_done"`
	DaysAfterLastUpdate   int                   `json:"days_after_last_update"`
	EstimatedDeliveryDate EstimatedDeliveryDate `json:"estimated_delivery_date"`
}

type EstimatedDeliveryDate struct {
	Source string `json:"source"`
	From   string `json:"from"`
	To     string `json:"to"`
}

type Milestone struct {
	KeyStage string  `json:"key_stage"`
	TimeISO  string  `json:"time_iso"`
	TimeUTC  string  `json:"time_utc"`
	TimeRaw  TimeRaw `json:"time_raw"`
}

type MiscInfo struct {
	RiskFactor      int    `json:"risk_factor"`
	ServiceType     string `json:"service_type"`
	WeightRaw       string `json:"weight_raw"`
	WeightKg        string `json:"weight_kg"`
	Pieces          string `json:"pieces"`
	Dimensions      string `json:"dimensions"`
	CustomerNumber  string `json:"customer_number"`
	ReferenceNumber string `json:"reference_number"`
	LocalNumber     string `json:"local_number"`
	LocalProvider   string `json:"local_provider"`
	LocalKey        int    `json:"local_key"`
}

type Tracking struct {
	ProvidersHash int        `json:"providers_hash"`
	Providers     []Provider `json:"providers"`
}

type Provider struct {
	ProviderInfo     ProviderInfo `json:"provider"`
	ServiceType      string       `json:"service_type"`
	LatestSyncStatus string       `json:"latest_sync_status"`
	LatestSyncTime   string       `json:"latest_sync_time"`
	EventsHash       int          `json:"events_hash"`
	Events           []Event      `json:"events"`
}

type ProviderInfo struct {
	Key      int    `json:"key"`
	Name     string `json:"name"`
	Alias    string `json:"alias"`
	Tel      string `json:"tel"`
	Homepage string `json:"homepage"`
	Country  string `json:"country"`
}
