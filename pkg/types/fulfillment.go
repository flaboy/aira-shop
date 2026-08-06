package types

type FulfillmentData struct {
	OrderID        string `json:"order_id"`
	Carrier        string `json:"carrier"`
	TrackingNumber string `json:"tracking_number"`
	TrackingURL    string `json:"tracking_url"`
	NotifyCustomer bool   `json:"notify_customer"`
}

type FulfillmentResult struct {
	ID string `json:"id"`
}
