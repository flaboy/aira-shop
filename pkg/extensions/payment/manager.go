package payment

import (
	"fmt"
	"log/slog"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
)

// PaymentManager 支付管理器
type PaymentManager struct{}

// NewPaymentManager 创建支付管理器
func NewPaymentManager() *PaymentManager {
	return &PaymentManager{}
}

// CreatePayment 创建支付订单
func (pm *PaymentManager) CreatePayment(channel string, businessContext interface{}, amount int64, currency string) (*types.CreatePaymentResult, error) {
	paymentChannel := Get(channel)
	if paymentChannel == nil {
		return nil, fmt.Errorf("payment channel '%s' not found", channel)
	}

	slog.Info("[PaymentManager] Calling CreatePayment", "channel", channel, "amount", amount)
	result, err := paymentChannel.CreatePayment(businessContext, amount, currency)
	if err != nil {
		return nil, err
	}
	slog.Info("[PaymentManager] CreatePayment returned", "channel", channel, "amount", result.Amount)

	return result, nil
}

// GetPaymentRecord 根据HashID获取支付记录
func (pm *PaymentManager) GetPaymentRecord(paymentHashID string) (*models.PaymentRecord, error) {
	paymentID, err := utils.DecodePaymentHashID(paymentHashID)
	if err != nil {
		return nil, fmt.Errorf("invalid payment hash ID: %w", err)
	}

	var record models.PaymentRecord
	err = database.Database().Where("id = ?", paymentID).First(&record).Error
	if err != nil {
		return nil, fmt.Errorf("payment record not found: %w", err)
	}

	return &record, nil
}

// GetPaymentRecordWithBusinessContext 获取支付记录并反序列化业务上下文
func (pm *PaymentManager) GetPaymentRecordWithBusinessContext(paymentHashID string, target interface{}) (*models.PaymentRecord, error) {
	record, err := pm.GetPaymentRecord(paymentHashID)
	if err != nil {
		return nil, err
	}

	if target != nil && record.BusinessContext != "" {
		err = utils.DeserializeBusinessContext(record.BusinessContext, target)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize business context: %w", err)
		}
	}

	return record, nil
}
