package shopify

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flaboy/aira-shop/pkg/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newShopifyEventTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.ShopifyEvent{}, &models.ShopifyEventAttempt{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestShopifyEventDuplicateDeliveryUsesSingleActiveLease(t *testing.T) {
	db := newShopifyEventTestDB(t)
	now := time.Unix(1000, 0).UTC()
	first, terminal, busy, err := beginShopifyEventWithDB(db, "event-1", "message-1", "orders/create", "shop-1", json.RawMessage(`{"id":1}`), now)
	if err != nil || terminal || busy {
		t.Fatalf("首次接收失败：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
	_, terminal, busy, err = beginShopifyEventWithDB(db, "event-1", "message-2", "orders/create", "shop-1", json.RawMessage(`{"id":1}`), now.Add(time.Minute))
	if err != nil || terminal || !busy {
		t.Fatalf("租约内重复投递必须 busy：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
	if err := finishShopifyEventWithDB(db, "event-1", first.ProcessingToken, "success", nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, terminal, busy, err = beginShopifyEventWithDB(db, "event-1", "message-3", "orders/create", "shop-1", json.RawMessage(`{"id":1}`), now.Add(3*time.Minute))
	if err != nil || !terminal || busy {
		t.Fatalf("成功事件重复投递必须为终态：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
}

func TestShopifyEventExpiredLeaseRejectsOldOwnerFinish(t *testing.T) {
	db := newShopifyEventTestDB(t)
	now := time.Unix(2000, 0).UTC()
	first, _, _, err := beginShopifyEventWithDB(db, "event-2", "message-1", "orders/create", "shop-2", json.RawMessage(`{"id":2}`), now)
	if err != nil {
		t.Fatal(err)
	}
	second, terminal, busy, err := beginShopifyEventWithDB(db, "event-2", "message-2", "orders/create", "shop-2", json.RawMessage(`{"id":2}`), now.Add(16*time.Minute))
	if err != nil || terminal || busy {
		t.Fatalf("过期租约应被新 owner 接管：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
	if err := finishShopifyEventWithDB(db, "event-2", first.ProcessingToken, "success", nil, now.Add(17*time.Minute)); err == nil {
		t.Fatal("旧 owner 丢失租约后不得提交成功")
	}
	if err := finishShopifyEventWithDB(db, "event-2", second.ProcessingToken, "failed", gorm.ErrInvalidData, now.Add(18*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestResolveShopifyEventUsesStableExternalShopIdentity(t *testing.T) {
	db := newShopifyEventTestDB(t)
	if err := db.AutoMigrate(&models.ShopLink{}, &models.ShopProduct{}); err != nil {
		t.Fatal(err)
	}
	shop := &models.ShopLink{Platform: "shopify", Url: "mutable-domain.myshopify.com", ExternalShopID: "69754355755"}
	if err := db.Create(shop).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ShopProduct{ShopID: shop.ID, Platform: "shopify", OuterID: "7992651972651"}).Error; err != nil {
		t.Fatal(err)
	}
	externalShopID, err := resolveShopifyEventExternalShopIDWithDB(db, json.RawMessage(`{"line_items":[{"product_id":7992651972651}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if externalShopID != "69754355755" {
		t.Fatalf("期望稳定店铺 ID 69754355755，实际为 %s", externalShopID)
	}
}

func TestResolveShopifyEventRejectsUnmappedProduct(t *testing.T) {
	db := newShopifyEventTestDB(t)
	if err := db.AutoMigrate(&models.ShopLink{}, &models.ShopProduct{}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveShopifyEventExternalShopIDWithDB(db, json.RawMessage(`{"line_items":[{"product_id":7992651972651}]}`)); err == nil {
		t.Fatal("未映射商品不能通过 URL 或其他候选身份继续处理")
	}
}
