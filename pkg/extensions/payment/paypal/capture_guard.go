package paypal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/pin"
	"github.com/plutov/paypal/v4"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	paypalCaptureTimeout = 15 * time.Second
	paypalGuardTimeout   = 2 * time.Minute
)

type guardedCaptureClient interface {
	GetOrder(context.Context, string) (*paypal.Order, error)
	CaptureOrderWithPaypalRequestId(context.Context, string, paypal.CaptureOrderRequest, string, *paypal.CaptureOrderMockResponse) (*paypal.CaptureOrderResponse, error)
}

var (
	errGuardedCaptureCancelled = errors.New("payment cancelled")
	errGuardedCapturePending   = errors.New("payment capture is unresolved")
)

func (p *PayPal) handleGuardedCapture(c *pin.Context, paymentHashID, action string, record *models.PaymentRecord, guard utils.CaptureGuard) error {
	ctx, cancel := context.WithTimeout(c.Request.Context(), paypalGuardTimeout)
	defer cancel()
	result, err := p.reconcileGuardedOrder(ctx, database.Database(), paymentHashID, *record, guard, true, action == "cancel")
	if err != nil {
		slog.Warn("guarded_paypal_recovery_incomplete", "payment_id", record.ID, "status", result.Status)
	}
	return p.renderGuardedResult(c, paymentHashID, result.Status)
}

type guardedPayPalSnapshot struct {
	result       types.RecoveryResult
	receipt      string
	approved     bool
	safeToCancel bool
}

func inspectGuardedPayPalOrder(ctx context.Context, client guardedCaptureClient, paymentHashID string, record models.PaymentRecord) (guardedPayPalSnapshot, error) {
	readCtx, cancelRead := context.WithTimeout(ctx, paypalCaptureTimeout)
	defer cancelRead()
	order, err := client.GetOrder(readCtx, record.ExternalOrderID)
	if err != nil || order == nil || order.ID != record.ExternalOrderID || len(order.PurchaseUnits) != 1 {
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
	unit := order.PurchaseUnits[0]
	if unit.ReferenceID != paymentHashID || !matchesPayPalAmount(unit.Amount, record) {
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
	if unit.Payments != nil && len(unit.Payments.Captures) > 0 || order.Status == "COMPLETED" {
		return paypalCaptureSnapshot(unit.Payments, record)
	}
	switch order.Status {
	case "CREATED", "SAVED", "PAYER_ACTION_REQUIRED":
		if approval := guardedPayPalApprovalURL(order); approval != "" {
			return guardedPayPalSnapshot{
				result: types.RecoveryResult{Status: types.PaymentApprovalRequired, ApprovalURL: approval, ExternalID: order.ID}, safeToCancel: true,
			}, nil
		}
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentProcessing}, safeToCancel: true}, nil
	case "APPROVED":
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentProcessing}, approved: true, safeToCancel: true}, nil
	case "VOIDED":
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentFailed}}, nil
	default:
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
}

func paypalCaptureSnapshot(payments *paypal.CapturedPayments, record models.PaymentRecord) (guardedPayPalSnapshot, error) {
	if payments == nil || len(payments.Captures) != 1 {
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
	capture := payments.Captures[0]
	if capture.ID == "" || !matchesPayPalAmount(capture.Amount, record) {
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
	switch capture.Status {
	case "COMPLETED":
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentSettlementPending}, receipt: capture.ID}, nil
	case "DECLINED", "FAILED":
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentFailed}}, nil
	default:
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
}

func advanceGuardedPayPalOrder(ctx context.Context, client guardedCaptureClient, guard utils.CaptureGuard, paymentHashID string, record models.PaymentRecord, cancelOrder bool) (guardedPayPalSnapshot, error) {
	observed, err := inspectGuardedPayPalOrder(ctx, client, paymentHashID, record)
	if err != nil || observed.receipt != "" || observed.result.Status == types.PaymentFailed {
		return observed, err
	}
	if cancelOrder {
		if !observed.safeToCancel {
			return observed, errGuardedCapturePending
		}
		if err := guard.Cancel(ctx); err != nil {
			return observed, err
		}
		return observed, errGuardedCaptureCancelled
	}
	if !observed.approved {
		return observed, nil
	}
	if err := guard.Authorize(ctx); err != nil {
		return observed, err
	}
	pspCtx, cancelPSP := context.WithTimeout(ctx, paypalCaptureTimeout)
	defer cancelPSP()
	response, err := client.CaptureOrderWithPaypalRequestId(pspCtx, record.ExternalOrderID, paypal.CaptureOrderRequest{}, "capture-"+paymentHashID, nil)
	if isPayPalInstrumentDecline(err) {
		// 只有已核对原单仍无捕获时，才能将明确的付款工具拒付终结。
		current, readErr := inspectGuardedPayPalOrder(ctx, client, paymentHashID, record)
		if readErr != nil || !current.safeToCancel {
			return current, readErr
		}
		return guardedPayPalSnapshot{result: types.RecoveryResult{Status: types.PaymentFailed}}, nil
	}
	if err != nil || response == nil || response.ID != record.ExternalOrderID ||
		response.Status != "COMPLETED" || len(response.PurchaseUnits) != 1 || response.PurchaseUnits[0].ReferenceID != paymentHashID {
		return guardedPayPalSnapshot{}, errGuardedCapturePending
	}
	return paypalCaptureSnapshot(response.PurchaseUnits[0].Payments, record)
}

func isPayPalInstrumentDecline(err error) bool {
	var remote *paypal.ErrorResponse
	return errors.As(err, &remote) && remote.Response != nil && remote.Response.StatusCode == http.StatusUnprocessableEntity &&
		remote.Name == "UNPROCESSABLE_ENTITY" && len(remote.Details) == 1 && remote.Details[0].Issue == "INSTRUMENT_DECLINED"
}

func (p *PayPal) reconcileGuardedOrder(ctx context.Context, db *gorm.DB, paymentID string, record models.PaymentRecord, guard utils.CaptureGuard, resume, cancelOrder bool) (types.RecoveryResult, error) {
	pending := types.RecoveryResult{Status: types.PaymentProcessing}
	if record.Channel != p.GetChannelName() || record.Currency != "USD" || record.Amount <= 0 {
		return pending, errors.New("invalid recoverable PayPal payment amount, currency or channel")
	}
	if record.Status == "completed" {
		return types.RecoveryResult{Status: types.PaymentSucceeded}, nil
	}
	if record.Status == "cancelled" || record.Status == "failed" {
		return types.RecoveryResult{Status: types.PaymentFailed}, nil
	}
	if record.ExternalOrderID == "" {
		return pending, nil
	}
	if !resume {
		snapshot, err := inspectGuardedPayPalOrder(ctx, p.client, paymentID, record)
		if err != nil || snapshot.result.Status == types.PaymentFailed {
			return pending, err
		}
		return snapshot.result, nil
	}
	snapshot, err := advanceGuardedPayPalOrder(ctx, p.client, guard, paymentID, record, cancelOrder)
	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancelWrite()
	if errors.Is(err, errGuardedCaptureCancelled) {
		return setGuardedPayPalFailure(writeCtx, db, record.ID, "cancelled")
	}
	if err != nil {
		return pending, err
	}
	if snapshot.result.Status == types.PaymentFailed {
		decliner, ok := guard.(utils.CaptureDecliner)
		if !ok {
			return pending, errors.New("PayPal decline protection is unavailable")
		}
		if err := decliner.Decline(writeCtx); err != nil {
			return pending, err
		}
		return setGuardedPayPalFailure(writeCtx, db, record.ID, "failed")
	}
	if snapshot.receipt == "" {
		return snapshot.result, nil
	}
	if err := guard.RecordCaptured(writeCtx, snapshot.receipt); err != nil {
		return snapshot.result, err
	}
	if err := p.processSuccessfulPaymentWithReceipt(writeCtx, db, &record, snapshot.receipt); err != nil {
		return snapshot.result, err
	}
	return types.RecoveryResult{Status: types.PaymentSucceeded}, nil
}

func setGuardedPayPalFailure(ctx context.Context, db *gorm.DB, recordID uint, state string) (types.RecoveryResult, error) {
	db = db.WithContext(ctx)
	result := db.Model(&models.PaymentRecord{}).Where("id = ? AND status IN ?", recordID, []string{"pending", "created"}).
		Update("status", state)
	if result.Error != nil {
		return types.RecoveryResult{Status: types.PaymentProcessing}, result.Error
	}
	if result.RowsAffected == 0 {
		var record models.PaymentRecord
		if err := db.First(&record, recordID).Error; err != nil {
			return types.RecoveryResult{Status: types.PaymentProcessing}, err
		}
		if record.Status == "completed" {
			return types.RecoveryResult{Status: types.PaymentSucceeded}, nil
		}
	}
	return types.RecoveryResult{Status: types.PaymentFailed}, nil
}

func (p *PayPal) renderGuardedResult(c *pin.Context, paymentID, status string) error {
	message := "Payment status is available from your Orders page."
	switch status {
	case types.PaymentSucceeded:
		message = "Payment completed successfully."
	case types.PaymentFailed:
		message = "Payment was declined or cancelled."
	case types.PaymentSettlementPending:
		message = "Payment was received and is being reconciled."
	}
	payload, err := json.Marshal(map[string]any{
		"type": "payment_result", "payment_id": paymentID, "payment_status": status,
		"success": status == types.PaymentSucceeded, "message": message,
	})
	if err != nil {
		return err
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.String(200, fmt.Sprintf(`<!doctype html><html lang="en"><meta charset="utf-8">
<title>Order payment</title><body><p>%s</p><button onclick="window.close()">Close Window</button>
<script>const result = %s;
if (window.opener) window.opener.postMessage(result, window.location.origin);
if (window.parent !== window) window.parent.postMessage(result, window.location.origin);
</script></body></html>`, message, payload))
	return nil
}

func matchesPayPalAmount(amount *paypal.PurchaseUnitAmount, record models.PaymentRecord) bool {
	if amount == nil || !strings.EqualFold(amount.Currency, record.Currency) {
		return false
	}
	value, err := decimal.NewFromString(amount.Value)
	return err == nil && value.Equal(decimal.New(record.Amount, -2))
}

func (p *PayPal) processSuccessfulPaymentWithReceipt(ctx context.Context, db *gorm.DB, record *models.PaymentRecord, receipt string) error {
	var afterCommit []func()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current models.PaymentRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, record.ID).Error; err != nil {
			return err
		}
		if current.ExternalOrderID != record.ExternalOrderID || current.Channel != record.Channel ||
			current.Amount != record.Amount || current.Currency != record.Currency || current.BusinessContext != record.BusinessContext {
			return fmt.Errorf("PayPal payment record changed during capture")
		}
		if current.Status == "completed" {
			return nil
		}
		completedAt := time.Now().UTC()
		if err := tx.Model(&current).Updates(map[string]any{"status": "completed", "completed_at": completedAt}).Error; err != nil {
			return err
		}
		current.CompletedAt = &completedAt
		return utils.NotifyBusinessSystemWithCapture(tx, &current, receipt, func(callback func()) {
			afterCommit = append(afterCommit, callback)
		})
	})
	if err != nil {
		return err
	}
	for _, callback := range afterCommit {
		callback()
	}
	return nil
}
