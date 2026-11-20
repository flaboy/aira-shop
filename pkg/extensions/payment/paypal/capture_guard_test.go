package paypal

import (
	"context"
	"errors"
	"testing"

	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/plutov/paypal/v4"
)

type fakeCaptureGuard struct {
	reject, recordError              error
	authorizations, records, cancels int
	receipt                          string
}

func (g *fakeCaptureGuard) Authorize(context.Context) error {
	g.authorizations++
	return g.reject
}

func (g *fakeCaptureGuard) RecordCaptured(_ context.Context, receipt string) error {
	g.records++
	g.receipt = receipt
	return g.recordError
}

func (g *fakeCaptureGuard) Cancel(context.Context) error {
	g.cancels++
	return nil
}

type fakeGuardedPayPal struct {
	order      *paypal.Order
	response   *paypal.CaptureOrderResponse
	captureErr error
	captures   []string
}

func (f *fakeGuardedPayPal) GetOrder(context.Context, string) (*paypal.Order, error) {
	return f.order, nil
}

func (f *fakeGuardedPayPal) CaptureOrderWithPaypalRequestId(_ context.Context, _ string, _ paypal.CaptureOrderRequest, requestID string, _ *paypal.CaptureOrderMockResponse) (*paypal.CaptureOrderResponse, error) {
	f.captures = append(f.captures, requestID)
	return f.response, f.captureErr
}

func guardedPayPalFixture() (*fakeGuardedPayPal, models.PaymentRecord) {
	const externalID, hashID = "ORDER-123", "pm-123"
	amount := &paypal.PurchaseUnitAmount{Currency: "USD", Value: "12.30"}
	payments := &paypal.CapturedPayments{Captures: []paypal.CaptureAmount{{ID: "CAPTURE-123", Status: "COMPLETED", Amount: amount}}}
	return &fakeGuardedPayPal{
		order:    &paypal.Order{ID: externalID, Status: "APPROVED", PurchaseUnits: []paypal.PurchaseUnit{{ReferenceID: hashID, Amount: amount}}},
		response: &paypal.CaptureOrderResponse{ID: externalID, Status: "COMPLETED", PurchaseUnits: []paypal.CapturedPurchaseUnit{{ReferenceID: hashID, Payments: payments}}},
	}, models.PaymentRecord{ExternalOrderID: externalID, Channel: "paypal", Currency: "USD", Amount: 1230}
}

func TestGuardedPayPalSourceRejectionNeverCaptures(t *testing.T) {
	for _, reason := range []string{"source_cancelled", "source_changed", "source_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			client, record := guardedPayPalFixture()
			guard := &fakeCaptureGuard{reject: errors.New(reason)}
			_, err := testGuardedReceipt(context.Background(), client, guard, false, record)
			if !errors.Is(err, guard.reject) || len(client.captures) != 0 || guard.authorizations != 1 || guard.records != 0 {
				t.Fatalf("blocked source reached capture: err=%v captures=%d guard=%+v", err, len(client.captures), guard)
			}
		})
	}
}

func TestGuardedPayPalRecoverCapturedOrderWithoutRecapture(t *testing.T) {
	for _, lost := range []string{"provider response", "local receipt"} {
		t.Run(lost, func(t *testing.T) {
			client, record := guardedPayPalFixture()
			guard := &fakeCaptureGuard{}
			if lost == "provider response" {
				client.captureErr = errors.New("response lost")
			} else {
				guard.recordError = errors.New("local receipt write failed")
			}
			if _, err := testGuardedReceipt(context.Background(), client, guard, false, record); err == nil {
				t.Fatal("first confirmation should have uncertain outcome")
			}
			if len(client.captures) != 1 || client.captures[0] != "capture-pm-123" {
				t.Fatalf("wrong capture idempotency key: %v", client.captures)
			}
			client.order.Status = "COMPLETED"
			client.order.PurchaseUnits[0].Payments = client.response.PurchaseUnits[0].Payments
			guard.recordError = nil
			guard.reject = errors.New("source changed after capture")
			receipt, err := testGuardedReceipt(context.Background(), client, guard, true, record)
			if err != nil || receipt != "CAPTURE-123" || len(client.captures) != 1 || guard.authorizations != 1 || guard.cancels != 0 {
				t.Fatalf("captured order must settle from provider receipt despite a cancel or source change: %q %v %+v", receipt, err, guard)
			}
		})
	}
}

func TestGuardedPayPalRejectsMismatchedAmountAndReceipt(t *testing.T) {
	client, record := guardedPayPalFixture()
	record.Amount--
	guard := &fakeCaptureGuard{}
	_, err := testGuardedReceipt(context.Background(), client, guard, false, record)
	if !errors.Is(err, errGuardedCapturePending) || len(client.captures) != 0 || guard.authorizations != 0 {
		t.Fatalf("incorrect order amount authorized: %v", err)
	}
	client, record = guardedPayPalFixture()
	client.order.Status = "COMPLETED"
	client.order.PurchaseUnits[0].Payments = client.response.PurchaseUnits[0].Payments
	client.order.PurchaseUnits[0].Payments.Captures[0].Amount = &paypal.PurchaseUnitAmount{Currency: "USD", Value: "20.00"}
	_, err = testGuardedReceipt(context.Background(), client, guard, false, record)
	if !errors.Is(err, errGuardedCapturePending) || guard.records != 0 {
		t.Fatalf("incorrect capture receipt accepted: %v", err)
	}
}

func testGuardedReceipt(ctx context.Context, client guardedCaptureClient, guard *fakeCaptureGuard, cancelOrder bool, record models.PaymentRecord) (string, error) {
	snapshot, err := advanceGuardedPayPalOrder(ctx, client, guard, "pm-123", record, cancelOrder)
	if err != nil || snapshot.receipt == "" {
		return "", err
	}
	return snapshot.receipt, guard.RecordCaptured(ctx, snapshot.receipt)
}
