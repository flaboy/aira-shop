package paypal

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/config"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-web/pkg/helper"
	"github.com/flaboy/pin"
	"github.com/plutov/paypal/v4"
	"gorm.io/gorm"
)

type PayPal struct {
	client *paypal.Client
}

// Init 初始化PayPal客户端
func (p *PayPal) Init() error {
	// 根据配置选择环境
	var environment string
	// 如果没有配置ClientSecret或者是测试环境，使用沙盒
	environment = paypal.APIBaseSandBox

	// 创建PayPal客户端
	client, err := paypal.NewClient(
		config.Config.PayPal.ClientID,
		config.Config.PayPal.ClientSecret,
		environment,
	)
	if err != nil {
		return err
	}

	// 获取访问令牌
	_, err = client.GetAccessToken(context.Background())
	if err != nil {
		return err
	}

	p.client = client
	log.Printf("PayPal payment channel initialized successfully")
	return nil
}

// GetChannelName 获取渠道名称
func (p *PayPal) GetChannelName() string {
	return "paypal"
}

// CreatePayment 创建PayPal支付
func (p *PayPal) CreatePayment(businessContext interface{}, amount int64, currency string) (*types.CreatePaymentResult, error) {
	log.Printf("[PayPal CreatePayment] Starting payment creation - amount: %d, currency: %s", amount, currency)

	// 序列化业务上下文
	contextJSON, err := utils.SerializeBusinessContext(businessContext)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize business context: %w", err)
	}

	// 创建支付记录
	paymentRecord := &models.PaymentRecord{
		Channel:         p.GetChannelName(),
		Amount:          amount,
		Currency:        currency,
		Status:          "pending",
		BusinessContext: contextJSON,
	}

	log.Printf("[PayPal CreatePayment] Created PaymentRecord with amount: %d", paymentRecord.Amount)

	err = database.Database().Create(paymentRecord).Error
	if err != nil {
		return nil, fmt.Errorf("failed to create payment record: %w", err)
	}

	log.Printf("[PayPal CreatePayment] Saved PaymentRecord to DB - ID: %d, Amount: %d", paymentRecord.ID, paymentRecord.Amount)

	// 生成 payment hash ID
	paymentHashID := utils.EncodePaymentID(paymentRecord.ID)

	// 创建PayPal订单
	ctx := context.Background()
	amountFloat := float64(amount) / 100 // 转换为元

	// 构建购买单元
	purchaseUnits := []paypal.PurchaseUnitRequest{
		{
			ReferenceID: paymentHashID,
			Amount: &paypal.PurchaseUnitAmount{
				Currency: strings.ToUpper(currency),
				Value:    fmt.Sprintf("%.2f", amountFloat),
			},
			Description: "Payment via project Platform",
		},
	}

	// 构建应用上下文 - 使用新的回调URL格式
	applicationContext := &paypal.ApplicationContext{
		ReturnURL: p.getCallbackURL(paymentHashID, "success"),
		CancelURL: p.getCallbackURL(paymentHashID, "cancel"),
	}

	// 创建订单
	order, err := p.client.CreateOrder(ctx, "CAPTURE", purchaseUnits, nil, applicationContext)
	if err != nil {
		return nil, fmt.Errorf("failed to create PayPal order: %w", err)
	}

	// 获取批准URL
	approvalURL := p.getApprovalURL(order)
	if approvalURL == "" {
		return nil, fmt.Errorf("failed to get PayPal approval URL")
	}

	// 更新支付记录，添加PayPal订单ID
	err = database.Database().Model(paymentRecord).Updates(map[string]interface{}{
		"status":            "created",
		"external_order_id": order.ID,
	}).Error
	if err != nil {
		return nil, fmt.Errorf("failed to update payment record status: %w", err)
	}

	// 构造客户端参数
	clientArgs := map[string]interface{}{
		"paypal": map[string]interface{}{
			"order_id":        order.ID,
			"approval_url":    approvalURL,
			"payment_hash_id": paymentHashID,
			"amount":          amountFloat,
			"currency":        strings.ToUpper(currency),
			"description":     "Payment via project Platform",
		},
	}

	return &types.CreatePaymentResult{
		Success:       false, // PayPal需要用户重定向，所以Success为false
		PaymentHashID: paymentHashID,
		ExternalID:    order.ID,
		Amount:        amount,
		Currency:      strings.ToUpper(currency),
		Status:        "created",
		RedirectURL:   approvalURL,
		ClientArgs:    clientArgs,
		Message:       "Please complete payment on PayPal",
	}, nil
}

// HandleRequest 处理PayPal的回调和webhook请求
func (p *PayPal) HandleRequest(c *pin.Context, path string) error {
	switch {
	case strings.HasPrefix(path, "callback/"):
		return p.handleCallback(c, path)
	case path == "webhook" || strings.HasPrefix(path, "webhook"):
		return p.handleWebhook(c)
	default:
		c.JSON(404, map[string]string{"error": "Not found"})
		return nil
	}
}

// handleCallback 处理PayPal支付回调
func (p *PayPal) handleCallback(c *pin.Context, path string) error {
	// 从路径中提取 payment_hash_id
	// path 格式: "callback/{payment_hash_id}"
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		log.Printf("PayPal callback invalid path: %s", path)
		return p.renderErrorPage(c, "Invalid callback URL")
	}

	paymentHashID := parts[1]
	action := c.Query("action") // success 或 cancel

	// 解码获取数据库ID
	paymentID, err := utils.DecodePaymentHashID(paymentHashID)
	if err != nil {
		log.Printf("Failed to decode payment hash ID: %v", err)
		return p.renderErrorPage(c, "Invalid payment ID")
	}

	// 如果用户取消了支付
	if action == "cancel" {
		err := p.updatePaymentStatus(paymentID, "cancelled", "Payment cancelled by user")
		if err != nil {
			log.Printf("Failed to update payment status to cancelled: %v", err)
		}
		return p.renderErrorPage(c, "Payment was cancelled")
	}

	// 获取支付记录
	var paymentRecord models.PaymentRecord
	err = database.Database().Where("id = ?", paymentID).First(&paymentRecord).Error
	if err != nil {
		log.Printf("Failed to find payment record: %v", err)
		return p.renderErrorPage(c, "Payment record not found")
	}

	log.Printf("[PayPal Callback] Retrieved PaymentRecord - ID: %d, Amount: %d, Status: %s",
		paymentRecord.ID, paymentRecord.Amount, paymentRecord.Status)

	orderID := paymentRecord.ExternalOrderID
	if orderID == "" {
		log.Printf("PayPal order ID not found for payment %s", paymentHashID)
		return p.renderErrorPage(c, "PayPal order not found")
	}

	// 获取PayPal订单详情验证状态
	order, err := p.client.GetOrder(context.Background(), orderID)
	if err != nil {
		log.Printf("Failed to get PayPal order: %v", err)
		return p.renderErrorPage(c, "Failed to retrieve PayPal order")
	}

	// 检查订单状态
	if order.Status != "APPROVED" {
		log.Printf("PayPal order not approved, status: %s", order.Status)
		err = p.updatePaymentStatus(paymentID, "failed", fmt.Sprintf("Order status: %s", order.Status))
		if err != nil {
			log.Printf("Failed to update payment status: %v", err)
		}
		return p.renderErrorPage(c, fmt.Sprintf("Payment not approved, status: %s", order.Status))
	}

	// 捕获支付
	capture, err := p.client.CaptureOrder(context.Background(), orderID, paypal.CaptureOrderRequest{})
	if err != nil {
		log.Printf("Failed to capture PayPal payment: %v", err)
		err = p.updatePaymentStatus(paymentID, "failed", fmt.Sprintf("Capture failed: %v", err))
		if err != nil {
			log.Printf("Failed to update payment status: %v", err)
		}
		return p.renderErrorPage(c, "Failed to capture payment")
	}

	// 检查捕获状态
	if capture.Status != "COMPLETED" {
		log.Printf("PayPal capture not completed, status: %s", capture.Status)
		err = p.updatePaymentStatus(paymentID, "failed", fmt.Sprintf("Capture status: %s", capture.Status))
		if err != nil {
			log.Printf("Failed to update payment status: %v", err)
		}
		return p.renderErrorPage(c, fmt.Sprintf("Payment capture incomplete, status: %s", capture.Status))
	}

	// 处理成功的支付
	err = p.processSuccessfulPayment(paymentID, orderID, &paymentRecord)
	if err != nil {
		log.Printf("Failed to process successful payment: %v", err)
		return p.renderErrorPage(c, "Failed to process payment")
	}

	// 成功重定向
	return p.renderSuccessPage(c, "Payment completed successfully")
}

// handleWebhook 处理PayPal webhook事件
func (p *PayPal) handleWebhook(c *pin.Context) error {
	// TODO: 实现PayPal webhook验证和处理
	// 这可以作为额外的安全层，验证支付状态
	c.JSON(200, map[string]string{"status": "ok"})
	return nil
}

// updatePaymentStatus 更新支付状态
func (p *PayPal) updatePaymentStatus(paymentID uint, status, message string) error {
	updates := map[string]interface{}{
		"status": status,
	}

	err := database.Database().Model(&models.PaymentRecord{}).
		Where("id = ?", paymentID).
		Updates(updates).Error

	if err != nil {
		return err
	}

	log.Printf("Updated payment %d status to %s", paymentID, status)
	return nil
}

// processSuccessfulPayment 处理成功的支付
func (p *PayPal) processSuccessfulPayment(paymentID uint, orderID string, paymentRecord *models.PaymentRecord) error {
	log.Printf("[PayPal ProcessSuccessful] Starting - paymentID: %d, amount: %d", paymentID, paymentRecord.Amount)

	return database.Database().Transaction(func(tx *gorm.DB) error {
		// 确保支付记录尚未处理
		if paymentRecord.Status == "completed" {
			log.Printf("Payment %d already completed, skipping", paymentID)
			return nil
		}

		// 更新支付记录状态
		err := tx.Model(paymentRecord).Updates(map[string]interface{}{
			"status":       "completed",
			"completed_at": gorm.Expr("NOW()"),
		}).Error
		if err != nil {
			return err
		}

		log.Printf("Successfully processed PayPal payment: paymentID=%d, orderID=%s, amount=%d",
			paymentID, orderID, paymentRecord.Amount)

		// 在通知业务系统前再次检查金额
		log.Printf("[PayPal ProcessSuccessful] Before NotifyBusinessSystem - Amount: %d", paymentRecord.Amount)

		if err := utils.NotifyBusinessSystem(tx, paymentRecord); err != nil {
			log.Printf("Failed to notify business system: %v", err)
			return err
		}

		return nil
	})
}

// getApprovalURL 从PayPal订单链接中获取批准URL
func (p *PayPal) getApprovalURL(order *paypal.Order) string {
	for _, link := range order.Links {
		if link.Rel == "approve" {
			return link.Href
		}
	}
	return ""
}

// getCallbackURL 生成回调URL
func (p *PayPal) getCallbackURL(paymentHashID, action string) string {
	return helper.BuildUrl("/payment/paypal/callback/" + paymentHashID + "?action=" + action)
}

// renderSuccessPage 渲染成功页面
func (p *PayPal) renderSuccessPage(c *pin.Context, message string) error {
	html := p.generateResultHTML("Payment Successful", message, "success")
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(200, html)
	return nil
}

// renderErrorPage 渲染错误页面
func (p *PayPal) renderErrorPage(c *pin.Context, message string) error {
	html := p.generateResultHTML("Payment Failed", message, "error")
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(200, html)
	return nil
}

// generateResultHTML 生成结果页面HTML
func (p *PayPal) generateResultHTML(title, message, messageType string) string {
	var icon, bgColor, textColor string
	switch messageType {
	case "success":
		icon = "✓"
		bgColor = "bg-green-100"
		textColor = "text-green-800"
	case "error":
		icon = "✗"
		bgColor = "bg-red-100"
		textColor = "text-red-800"
	default:
		icon = "⚠"
		bgColor = "bg-yellow-100"
		textColor = "text-yellow-800"
	}

	return fmt.Sprintf(`
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
    <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-gray-100 h-screen flex items-center justify-center">
    <div class="max-w-md w-full mx-auto">
        <div class="bg-white shadow-lg rounded-lg p-6">
            <div class="text-center">
                <div class="mx-auto flex items-center justify-center h-12 w-12 rounded-full %s mb-4">
                    <span class="text-2xl font-bold %s">%s</span>
                </div>
                <h3 class="text-lg font-medium text-gray-900 mb-2">%s</h3>
                <p class="text-sm text-gray-500 mb-4">%s</p>
                <button onclick="window.close()" class="w-full bg-blue-600 hover:bg-blue-700 text-white font-medium py-2 px-4 rounded">
                    Close Window
                </button>
            </div>
        </div>
    </div>
    <script>
        // 尝试通过 postMessage 通知父窗口
        if (window.opener) {
            window.opener.postMessage({
                type: 'payment_result',
                success: %v,
                message: '%s'
            }, '*');
        }
        
        // 如果是在iframe中，通知父窗口
        if (window.parent !== window) {
            window.parent.postMessage({
                type: 'payment_result',
                success: %v,
                message: '%s'
            }, '*');
        }
    </script>
</body>
</html>`,
		title, bgColor, textColor, icon, title, message,
		messageType == "success", message,
		messageType == "success", message)
}
