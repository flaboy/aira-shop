package payment

import (
	"context"
	"errors"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
)

type recoverablePaymentChannel interface {
	PreparePayment(context.Context, interface{}, int64, string) (string, error)
	RecoverPayment(context.Context, string, models.PaymentRecord, bool) (types.RecoveryResult, error)
}

// PrepareRecoverablePayment 先保留本地支付身份，调用方绑定业务占用后才能调用 PSP。
func (pm *PaymentManager) PrepareRecoverablePayment(ctx context.Context, channel string, business interface{}, amount int64, currency string) (string, error) {
	provider, ok := Get(channel).(recoverablePaymentChannel)
	if !ok {
		return "", errors.New("payment channel recovery is unavailable")
	}
	return provider.PreparePayment(ctx, business, amount, currency)
}

// RecoverPayment 只能在业务方验证原付款的用户、账户、订单集合及金额后使用。
func (pm *PaymentManager) RecoverPayment(ctx context.Context, paymentID string, record models.PaymentRecord, resume bool) (types.RecoveryResult, error) {
	provider, ok := Get(record.Channel).(recoverablePaymentChannel)
	if !ok {
		return types.RecoveryResult{}, errors.New("payment channel recovery is unavailable")
	}
	return provider.RecoverPayment(ctx, paymentID, record, resume)
}

func (pm *PaymentManager) GetPaymentRecordForRecovery(ctx context.Context, paymentID string, business interface{}) (models.PaymentRecord, error) {
	var record models.PaymentRecord
	id, err := utils.DecodePaymentHashID(paymentID)
	if err != nil {
		return record, err
	}
	if err := database.Database().WithContext(ctx).First(&record, id).Error; err != nil {
		return record, err
	}
	return record, utils.DeserializeBusinessContext(record.BusinessContext, business)
}
