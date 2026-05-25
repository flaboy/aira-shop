package paypal

import (
	"testing"

	coreconfig "github.com/flaboy/aira-core/pkg/config"
	"github.com/flaboy/aira-shop/pkg/extensions/payment/utils"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMarkWalletPaymentOrderFailed(t *testing.T) {
	coreconfig.Config = &coreconfig.InfraConfig{AppSecret: "test-secret"}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.Exec(`CREATE TABLE wallet_payment_orders (
		id integer primary key autoincrement,
		payment_id text,
		status text,
		failure_reason text
	)`).Error; err != nil {
		t.Fatalf("create wallet_payment_orders failed: %v", err)
	}

	paymentHashID := utils.EncodePaymentID(1001)
	if err := db.Exec(`INSERT INTO wallet_payment_orders (payment_id, status) VALUES (?, ?)`, "pay-rc-"+paymentHashID, "processing").Error; err != nil {
		t.Fatalf("insert wallet order failed: %v", err)
	}
	if err := db.Exec(`INSERT INTO wallet_payment_orders (payment_id, status) VALUES (?, ?)`, "pay-cr-"+paymentHashID, "processing").Error; err != nil {
		t.Fatalf("insert credit repayment order failed: %v", err)
	}

	if err := markWalletPaymentOrderFailed(db, 1001, "Payment cancelled by user"); err != nil {
		t.Fatalf("mark wallet payment order failed: %v", err)
	}

	var row struct {
		Status        string
		FailureReason string
	}
	if err := db.Table("wallet_payment_orders").Where("payment_id = ?", "pay-rc-"+paymentHashID).First(&row).Error; err != nil {
		t.Fatalf("load wallet order failed: %v", err)
	}
	if row.Status != "failed" || row.FailureReason != "Payment cancelled by user" {
		t.Fatalf("wallet order mismatch: %+v", row)
	}
	if err := db.Table("wallet_payment_orders").Where("payment_id = ?", "pay-cr-"+paymentHashID).First(&row).Error; err != nil {
		t.Fatalf("load credit repayment order failed: %v", err)
	}
	if row.Status != "failed" || row.FailureReason != "Payment cancelled by user" {
		t.Fatalf("credit repayment order mismatch: %+v", row)
	}
}
