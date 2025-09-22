package config

type CommenceConfig struct {
	// 追踪服务配置
	The17TrackSecretKey string `cfg:"17TRACK_SECRET_KEY"`

	Shopify struct {
		Enabled        bool   `cfg:"ENABLED" default:"false"`
		ApiKey         string `cfg:"API_KEY"`
		ApiSecret      string `cfg:"API_SECRET"`
		EventBridgeARN string `cfg:"EVENT_BRIDGE_ARN"`
		AWSRegion      string `cfg:"AWS_REGION"`
		AWSAccessKey   string `cfg:"AWS_ACCESS_KEY"`
		AWSSecret      string `cfg:"AWS_SECRET"`
		SQSQueueURL    string `cfg:"SQS_QUEUE_URL"`
	} `cfg:"SHOPIFY"`

	// 支付服务配置
	PayPal struct {
		ClientID     string `cfg:"CLIENT_ID"`
		ClientSecret string `cfg:"CLIENT_SECRET"`
	} `cfg:"PAYPAL"`
}

var Config *CommenceConfig
