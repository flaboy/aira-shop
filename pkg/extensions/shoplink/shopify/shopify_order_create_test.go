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

func TestHandleOrderCreateDoesNotOwnPaidBusinessRule(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if strings.Contains(content, `order.FinancialStatus != goshopify.OrderFinancialStatusPaid`) {
		t.Fatal("orders/create paid filtering should be handled by the host system")
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

func TestShopifyConsumerKeepsFailedMessagesForRetry(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	failureGate := strings.Index(content, `if err := p.ProcessWebhook(`)
	deleteMessage := strings.Index(content, `client.DeleteMessage`)
	if failureGate < 0 || deleteMessage < 0 || failureGate > deleteMessage {
		t.Fatal("Shopify consumer must stop failed events before DeleteMessage")
	}
	if !strings.Contains(content[failureGate:deleteMessage], "continue") {
		t.Fatal("failed Shopify events must remain in SQS for retry and DLQ redrive")
	}
}

func TestShopifyConsumerRequiresDLQRedrivePolicy(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}
	content := string(source)
	if !strings.Contains(content, `sqstypes.QueueAttributeNameRedrivePolicy`) {
		t.Fatal("Shopify consumer must verify the queue redrive policy before consuming")
	}
	if !strings.Contains(content, `queue redrive policy and DLQ are required`) {
		t.Fatal("Shopify consumer must stop when no DLQ is configured")
	}
}

func TestHandleOrderCreateRejectsIncompleteProductMappings(t *testing.T) {
	source, err := os.ReadFile("shopify.go")
	if err != nil {
		t.Fatal(err)
	}

	content := string(source)
	if !strings.Contains(content, `return fmt.Errorf("shop product mapping not found for Shopify product %d"`) {
		t.Fatal("orders with an unmapped Shopify product must fail")
	}
	if !strings.Contains(content, `return fmt.Errorf("variant mapping not found for Shopify variant %d:`) {
		t.Fatal("orders with an unmapped Shopify variant must fail")
	}
}
