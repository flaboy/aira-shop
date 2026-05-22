package shopify

import (
	"os"
	"strings"
	"testing"
)

func TestHandleOrderCreateTreatsCustomerAsOptional(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if !strings.Contains(content, `if order.Customer != nil`) {
		t.Fatal("Shopify order customer should only be mapped when present")
	}
	if !strings.Contains(content, `orderData.Customer = &types.OrderCustomer`) {
		t.Fatal("Shopify order customer should be assigned after the nil check")
	}
}

func TestHandleOrderCreateReturnsOrderReceivedError(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if !strings.Contains(content, `if err := events.EmitOrderReceived`) {
		t.Fatal("orders/create should return the order received handler error")
	}
}

func TestHandleOrderCreateSkipsUnpaidOrders(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if !strings.Contains(content, `if order.FinancialStatus != goshopify.OrderFinancialStatusPaid`) {
		t.Fatal("orders/create should skip Shopify orders that are not paid")
	}
}

func TestHandleOrderPaidReusesOrderCreateFlow(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if !strings.Contains(content, `return p.handleOrderCreate(event)`) {
		t.Fatal("orders/paid should reuse the orders/create processing flow")
	}
}
