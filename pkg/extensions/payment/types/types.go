package types

// CreatePaymentResult 创建支付结果
type CreatePaymentResult struct {
	Success       bool                   `json:"success"`
	PaymentHashID string                 `json:"payment_hash_id"` // 使用hashid编码的支付ID
	ExternalID    string                 `json:"external_id"`     // 外部支付系统的订单ID
	Amount        int64                  `json:"amount"`
	Currency      string                 `json:"currency"`
	Status        string                 `json:"status"`       // pending, created, failed等
	RedirectURL   string                 `json:"redirect_url"` // 需要用户跳转的URL
	ClientArgs    map[string]interface{} `json:"client_args"`  // 传递给前端的参数
	Message       string                 `json:"message"`      // 状态消息
}

// PaymentCallbackResult 支付回调处理结果
type PaymentCallbackResult struct {
	Success         bool        `json:"success"`
	PaymentHashID   string      `json:"payment_hash_id"`
	Status          string      `json:"status"` // completed, failed, cancelled
	Message         string      `json:"message"`
	BusinessContext interface{} `json:"business_context"` // 反序列化的业务上下文
}

// WebhookResult Webhook处理结果
type WebhookResult struct {
	Success       bool   `json:"success"`
	PaymentHashID string `json:"payment_hash_id"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}
