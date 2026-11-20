package utils

import (
	"context"

	"github.com/flaboy/aira-shop/pkg/models"
)

// CaptureGuard 由业务系统选择并保护需要在资金捕获前复核的支付。
type CaptureGuard interface {
	Authorize(ctx context.Context) error
	RecordCaptured(ctx context.Context, receiptID string) error
	Cancel(ctx context.Context) error
}

type CaptureGuardResolver func(ctx context.Context, paymentHashID string, record models.PaymentRecord) (CaptureGuard, error)

var captureGuardResolver CaptureGuardResolver

// SetCaptureGuardResolver 在应用启动时注册业务侧捕获校验。
func SetCaptureGuardResolver(resolve CaptureGuardResolver) {
	captureGuardResolver = resolve
}

func ResolveCaptureGuard(ctx context.Context, paymentHashID string, record models.PaymentRecord) (CaptureGuard, error) {
	if captureGuardResolver == nil {
		return nil, nil
	}
	return captureGuardResolver(ctx, paymentHashID, record)
}

// CaptureDecliner 按业务原付款记录收口 PSP 已确认的拒付。
type CaptureDecliner interface {
	Decline(context.Context) error
}
