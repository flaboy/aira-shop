package paypal

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	webconfig "github.com/flaboy/aira-web/pkg/config"
	"github.com/plutov/paypal/v4"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const paypalCreateRetryWindow = time.Hour

func (p *PayPal) PreparePayment(ctx context.Context, business interface{}, amount int64, currency string) (string, error) {
	if amount <= 0 || currency != "USD" {
		return "", errors.New("invalid recoverable PayPal amount or currency")
	}
	serialized, err := utils.SerializeBusinessContext(business)
	if err != nil {
		return "", err
	}
	record := models.PaymentRecord{Channel: p.GetChannelName(), Amount: amount, Currency: currency, Status: "pending", BusinessContext: serialized}
	if err := database.Database().WithContext(ctx).Create(&record).Error; err != nil {
		return "", err
	}
	return utils.EncodePaymentID(record.ID), nil
}

func (p *PayPal) RecoverPayment(ctx context.Context, paymentID string, record models.PaymentRecord, resume bool) (types.RecoveryResult, error) {
	status := types.RecoveryResult{Status: types.PaymentProcessing}
	if record.ID == 0 || utils.EncodePaymentID(record.ID) != paymentID || record.Channel != p.GetChannelName() ||
		record.Amount <= 0 || record.Currency != "USD" {
		return status, errors.New("invalid recoverable PayPal payment identity")
	}
	switch record.Status {
	case "completed":
		return types.RecoveryResult{Status: types.PaymentSucceeded}, nil
	case "cancelled", "failed":
		return types.RecoveryResult{Status: types.PaymentFailed}, nil
	case "created", "pending":
	default:
		return status, errors.New("invalid recoverable PayPal payment state")
	}

	ctx, cancel := context.WithTimeout(ctx, paypalGuardTimeout)
	defer cancel()
	guard, err := utils.ResolveCaptureGuard(ctx, paymentID, record)
	if err != nil || guard == nil {
		return status, errors.New("PayPal capture protection is unavailable")
	}
	if record.ExternalOrderID == "" {
		if !resume || record.Status != "pending" || record.CreatedAt.IsZero() || time.Since(record.CreatedAt) >= paypalCreateRetryWindow {
			return status, nil
		}
		return p.createPreparedOrder(ctx, database.Database(), paymentID, record)
	}
	return p.reconcileGuardedOrder(ctx, database.Database(), paymentID, record, guard, resume, false)
}

func (p *PayPal) createPreparedOrder(ctx context.Context, db *gorm.DB, paymentID string, record models.PaymentRecord) (types.RecoveryResult, error) {
	status := types.RecoveryResult{Status: types.PaymentProcessing}
	successURL, cancelURL, err := recoverablePayPalCallbackURLs(paymentID)
	if err != nil {
		return status, err
	}
	pspCtx, cancel := context.WithTimeout(ctx, paypalCaptureTimeout)
	order, err := p.client.CreateOrderWithPaypalRequestID(pspCtx, "CAPTURE", []paypal.PurchaseUnitRequest{{
		ReferenceID: paymentID,
		Amount:      &paypal.PurchaseUnitAmount{Currency: record.Currency, Value: decimal.New(record.Amount, -2).StringFixed(2)},
		Description: "Order payment",
	}}, nil, &paypal.ApplicationContext{
		ReturnURL: successURL, CancelURL: cancelURL,
	}, "create-"+paymentID)
	cancel()
	if err != nil || order == nil || order.ID == "" {
		return status, nil
	}
	// 新建响应可能不含购买单元，进入批准或捕获前再读取并核对完整原单。
	bindCtx, cancelBind := context.WithTimeout(context.WithoutCancel(ctx), paypalCaptureTimeout)
	defer cancelBind()
	db = db.WithContext(bindCtx)
	binding := db.Model(&models.PaymentRecord{}).Where("id = ? AND status = ? AND external_order_id = ?", record.ID, "pending", "").
		Updates(map[string]any{"status": "created", "external_order_id": order.ID})
	if binding.Error != nil {
		return status, binding.Error
	}
	if binding.RowsAffected == 0 {
		var current models.PaymentRecord
		if err := db.First(&current, record.ID).Error; err != nil || current.ExternalOrderID != order.ID || current.Status != "created" {
			return status, err
		}
	}
	record.ExternalOrderID = order.ID
	snapshot, err := inspectGuardedPayPalOrder(ctx, p.client, paymentID, record)
	if err != nil || snapshot.result.Status == types.PaymentFailed {
		return status, nil
	}
	snapshot.result.ExternalID = order.ID
	return snapshot.result, nil
}

func guardedPayPalApprovalURL(order *paypal.Order) string {
	for _, link := range order.Links {
		if link.Rel != "approve" && link.Rel != "payer-action" {
			continue
		}
		parsed, err := url.Parse(link.Href)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || (parsed.Port() != "" && parsed.Port() != "443") {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "paypal.com" || strings.HasSuffix(host, ".paypal.com") {
			return parsed.String()
		}
	}
	return ""
}

func recoverablePayPalCallbackURLs(paymentID string) (string, string, error) {
	if webconfig.Config == nil {
		return "", "", errors.New("PayPal callback URL is unavailable")
	}
	base, err := url.Parse(webconfig.Config.FrontURL)
	if err != nil || base.Hostname() == "" || base.User != nil ||
		(base.Scheme != "https" && !(base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1" || base.Hostname() == "::1"))) {
		return "", "", errors.New("invalid PayPal callback URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/payment/paypal/callback/" + paymentID
	base.RawPath, base.Fragment = "", ""
	base.RawQuery = "action=success"
	success := base.String()
	base.RawQuery = "action=cancel"
	return success, base.String(), nil
}
