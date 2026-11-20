package paypal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	coreconfig "github.com/flaboy/aira-core/pkg/config"
	"github.com/flaboy/aira-shop/pkg/events"
	paymenttypes "github.com/flaboy/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	shoptypes "github.com/flaboy/aira-shop/pkg/types"
	webconfig "github.com/flaboy/aira-web/pkg/config"
	psp "github.com/plutov/paypal/v4"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func paypalRecoveryFixture(t *testing.T, handler http.HandlerFunc) (*PayPal, *gorm.DB, models.PaymentRecord, string) {
	t.Helper()
	old := coreconfig.Config
	previousWeb := webconfig.Config
	webconfig.Config = &webconfig.FrameworkConfig{FrontURL: "https://shop.example.test/app"}
	coreconfig.Config = &coreconfig.InfraConfig{AppSecret: "fixture-secret"}
	t.Cleanup(func() { coreconfig.Config = old; webconfig.Config = previousWeb })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := psp.NewClient("fixture", "fixture", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Client = server.Client()
	client.SetAccessToken("fixture-token")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if err := db.AutoMigrate(&models.PaymentRecord{}); err != nil {
		t.Fatal(err)
	}
	record := models.PaymentRecord{Channel: "paypal", Amount: 1230, Currency: "USD", Status: "pending", BusinessContext: `{"business_type":"order_payment"}`}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	return &PayPal{client: client}, db, record, utils.EncodePaymentID(record.ID)
}

func guardedPayPalOrderJSON(id, status, capture, hash string) string {
	payments := ""
	if capture != "" {
		payments = fmt.Sprintf(`,"payments":{"captures":[{"id":"CAPTURE-1","status":%q,"amount":{"currency_code":"USD","value":"12.30"}}]}`, capture)
	}
	return fmt.Sprintf(`{"id":%q,"status":%q,"purchase_units":[{"reference_id":%q,"amount":{"currency_code":"USD","value":"12.30"}%s}],
"links":[{"rel":"approve","href":"https://www.sandbox.paypal.com/checkoutnow?token=ORDER-1"}]}`, id, status, hash, payments)
}

func TestPayPalRecoveryReusesCreateRequestAfterLostResponse(t *testing.T) {
	var creates atomic.Int32
	var paymentID string
	p, db, record, hash := paypalRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v2/checkout/orders":
			if r.Header.Get("PayPal-Request-Id") != "create-"+paymentID {
				t.Error("wrong create idempotency key")
			}
			payload, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(payload), `"reference_id":"`+paymentID+`"`) ||
				!strings.Contains(string(payload), `"value":"12.30"`) || !strings.Contains(string(payload), `"currency_code":"USD"`) {
				t.Error("creation did not use the original PayPal context and amount")
			}
			if creates.Add(1) == 1 {
				http.Error(w, `{"name":"INTERNAL_SERVER_ERROR"}`, http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, `{"id":"ORDER-1","status":"CREATED"}`)
		case r.Method == "GET" && r.URL.Path == "/v2/checkout/orders/ORDER-1":
			fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "CREATED", "", paymentID))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	paymentID = hash
	first, err := p.createPreparedOrder(t.Context(), db, hash, record)
	if err != nil || first.Status != paymenttypes.PaymentProcessing {
		t.Fatalf("lost create response = %+v, %v", first, err)
	}
	second, err := p.createPreparedOrder(t.Context(), db, hash, record)
	if err != nil || second.Status != paymenttypes.PaymentApprovalRequired || second.ExternalID != "ORDER-1" || second.ApprovalURL == "" {
		t.Fatalf("same prepared order not recovered: %+v, %v", second, err)
	}
	if err := db.First(&record, record.ID).Error; err != nil || record.ExternalOrderID != "ORDER-1" || creates.Load() != 2 {
		t.Fatalf("local record or replay count changed: %+v, %v", record, err)
	}
}

type recoveryPaymentEventHandler struct {
	events.EventHandler
	completionCalls int
	fail            bool
}

func (e *recoveryPaymentEventHandler) OnPaymentCompleted(event *shoptypes.PaymentCompletedEvent) error {
	e.completionCalls++
	if e.fail {
		return errors.New("forced local settlement rollback")
	}
	if event.ExternalOrderID != "ORDER-1" || event.ExternalCaptureID != "CAPTURE-1" || event.Currency != "USD" {
		return errors.New("wrong PSP receipt")
	}
	return nil
}

type decliningCaptureGuard struct {
	fakeCaptureGuard
	declines int
}

func (g *decliningCaptureGuard) Decline(_ context.Context) error {
	g.declines++
	return nil
}

func TestPayPalRecoveryReadOnlyThenLostCaptureAndLocalSettlementFailure(t *testing.T) {
	var captures atomic.Int32
	var paymentID string
	p, db, record, hash := paypalRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v2/checkout/orders/ORDER-1":
			if captures.Load() > 0 {
				fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "COMPLETED", "COMPLETED", paymentID))
			} else {
				fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "APPROVED", "", paymentID))
			}
		case r.Method == "POST" && r.URL.Path == "/v2/checkout/orders/ORDER-1/capture":
			if r.Header.Get("PayPal-Request-Id") != "capture-"+paymentID {
				t.Error("capture did not use the original idempotency key")
			}
			captures.Add(1)
			http.Error(w, `{"name":"INTERNAL_SERVER_ERROR"}`, http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	paymentID = hash
	record.ExternalOrderID, record.Status = "ORDER-1", "created"
	if err := db.Save(&record).Error; err != nil {
		t.Fatal(err)
	}
	event := &recoveryPaymentEventHandler{fail: true}
	events.SetEventHandler(event)
	t.Cleanup(func() { events.SetEventHandler(nil) })
	guard := &decliningCaptureGuard{}
	try := func(resume bool) (paymenttypes.RecoveryResult, error) {
		return p.reconcileGuardedOrder(t.Context(), db, hash, record, guard, resume, false)
	}
	query, err := try(false)
	if err != nil || query.Status != paymenttypes.PaymentProcessing || captures.Load() != 0 || guard.authorizations != 0 {
		t.Fatalf("read-only request changed funds: %+v, %v", query, err)
	}
	first, err := try(true)
	if err == nil || first.Status != paymenttypes.PaymentProcessing || captures.Load() != 1 {
		t.Fatalf("lost capture response must remain unresolved: %+v, %v", first, err)
	}
	query, err = try(false)
	if err != nil || query.Status != paymenttypes.PaymentSettlementPending || guard.records != 0 || event.completionCalls != 0 {
		t.Fatalf("closed-window read settled or lost the receipt: %+v, %v", query, err)
	}
	failedLocal, err := try(true)
	if err == nil || failedLocal.Status != paymenttypes.PaymentSettlementPending || guard.records != 1 || event.completionCalls != 1 {
		t.Fatalf("local settlement failure was not recoverable: %+v, %v", failedLocal, err)
	}
	event.fail = false
	for range 2 {
		success, err := try(true)
		if err != nil || success.Status != paymenttypes.PaymentSucceeded {
			t.Fatalf("original capture not settled: %+v, %v", success, err)
		}
	}
	if captures.Load() != 1 || event.completionCalls != 2 || guard.authorizations != 1 {
		t.Fatal("recovery duplicated capture or a completed local transaction")
	}
	if err := db.First(&record, record.ID).Error; err != nil || record.Status != "completed" || record.CompletedAt == nil {
		t.Fatalf("completion state not persisted: %+v %v", record, err)
	}
}

func TestPayPalVerifiedDeclineOnlyReleasesOnResume(t *testing.T) {
	var paymentID string
	p, db, record, hash := paypalRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v2/checkout/orders/ORDER-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "COMPLETED", "DECLINED", paymentID))
	})
	paymentID = hash
	record.Status, record.ExternalOrderID = "created", "ORDER-1"
	if err := db.Save(&record).Error; err != nil {
		t.Fatal(err)
	}
	guard := &decliningCaptureGuard{}
	query, err := p.reconcileGuardedOrder(t.Context(), db, hash, record, guard, false, false)
	if err != nil || query.Status != paymenttypes.PaymentProcessing || guard.declines != 0 {
		t.Fatalf("query prematurely released decline: %+v, %v", query, err)
	}
	result, err := p.reconcileGuardedOrder(t.Context(), db, hash, record, guard, true, false)
	if err != nil || result.Status != paymenttypes.PaymentFailed || guard.declines != 1 || guard.authorizations != 0 {
		t.Fatalf("provider decline was not closed safely: %+v, %v", result, err)
	}
}

func TestGuardedPayPalApprovalURLRejectsUntrustedProviderLink(t *testing.T) {
	for _, raw := range []string{"http://paypal.com/", "https://paypal.com.evil.test/", "https://paypal.com@evil.test/"} {
		order := &psp.Order{Links: []psp.Link{{Rel: "approve", Href: raw}}}
		if got := guardedPayPalApprovalURL(order); got != "" {
			t.Fatal("untrusted approval link")
		}
	}
}

func TestPayPalCaptureHTTPDeclineConfirmsOriginalOrderBeforeRelease(t *testing.T) {
	for _, tc := range []struct {
		name, issue string
		captured    bool
		want        string
	}{
		{"verified refusal", "INSTRUMENT_DECLINED", false, paymenttypes.PaymentFailed},
		{"other 422 remains unknown", "ORDER_ALREADY_CAPTURED", false, paymenttypes.PaymentProcessing},
		{"successful receipt takes precedence", "INSTRUMENT_DECLINED", true, paymenttypes.PaymentSucceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var captures, reads atomic.Int32
			var paymentID string
			p, db, record, hash := paypalRecoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/v2/checkout/orders/ORDER-1":
					reads.Add(1)
					if tc.captured && captures.Load() > 0 {
						fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "COMPLETED", "COMPLETED", paymentID))
					} else {
						fmt.Fprint(w, guardedPayPalOrderJSON("ORDER-1", "APPROVED", "", paymentID))
					}
				case r.Method == "POST" && r.URL.Path == "/v2/checkout/orders/ORDER-1/capture":
					captures.Add(1)
					w.WriteHeader(422)
					fmt.Fprintf(w, `{"name":"UNPROCESSABLE_ENTITY","details":[{"issue":%q}]}`, tc.issue)
				default:
					t.Error("unexpected PayPal request")
					http.NotFound(w, r)
				}
			})
			paymentID = hash
			record.Status, record.ExternalOrderID = "created", "ORDER-1"
			if err := db.Save(&record).Error; err != nil {
				t.Fatal(err)
			}
			guard := &decliningCaptureGuard{}
			result, err := p.reconcileGuardedOrder(t.Context(), db, hash, record, guard, true, false)
			if result.Status != tc.want || captures.Load() != 1 || (tc.issue == "INSTRUMENT_DECLINED" && reads.Load() != 2) ||
				(tc.want == paymenttypes.PaymentFailed && guard.declines != 1) ||
				(tc.want != paymenttypes.PaymentFailed && guard.declines != 0) {
				t.Fatalf("wrong disposition of PayPal capture error: %+v %v", result, err)
			}
		})
	}
}
