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
	first, terminal, busy, err := beginShopifyEventWithDB(db, "event-1", "message-1", "orders/create", "shop-1", "one.myshopify.com", "https", json.RawMessage(`{"id":1}`), now)
	if err != nil || terminal || busy {
		t.Fatalf("首次接收失败：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
	_, terminal, busy, err = beginShopifyEventWithDB(db, "event-1", "message-2", "orders/create", "shop-1", "one.myshopify.com", "https", json.RawMessage(`{"id":1}`), now.Add(time.Minute))
	if err != nil || terminal || !busy {
		t.Fatalf("租约内重复投递必须 busy：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
	if err := finishShopifyEventWithDB(db, "event-1", first.ProcessingToken, "success", nil, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, terminal, busy, err = beginShopifyEventWithDB(db, "event-1", "message-3", "orders/create", "shop-1", "one.myshopify.com", "https", json.RawMessage(`{"id":1}`), now.Add(3*time.Minute))
	if err != nil || !terminal || busy {
		t.Fatalf("成功事件重复投递必须为终态：terminal=%v busy=%v err=%v", terminal, busy, err)
	}
}

func TestShopifyEventExpiredLeaseRejectsOldOwnerFinish(t *testing.T) {
	db := newShopifyEventTestDB(t)
	now := time.Unix(2000, 0).UTC()
	first, _, _, err := beginShopifyEventWithDB(db, "event-2", "message-1", "orders/create", "shop-2", "two.myshopify.com", "https", json.RawMessage(`{"id":2}`), now)
	if err != nil {
		t.Fatal(err)
	}
	second, terminal, busy, err := beginShopifyEventWithDB(db, "event-2", "message-2", "orders/create", "shop-2", "two.myshopify.com", "https", json.RawMessage(`{"id":2}`), now.Add(16*time.Minute))
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

// 用真实SQLite触发器模拟MySQL严格日期下界，防止内部释放租约写零日期。
func TestShopifyEventFinishReleasesLeaseWithValidDate(t *testing.T) {
	for _, state := range []string{"ignored", "failed"} {
		t.Run(state, func(t *testing.T) {
			db := newShopifyEventTestDB(t)
			if err := db.Exec(`CREATE TRIGGER reject_zero_lease BEFORE UPDATE OF lease_until ON ar_shopify_events WHEN NEW.lease_until < '1000-01-01' BEGIN SELECT RAISE(ABORT,'strict datetime rejects zero lease'); END;`).Error; err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
			event, _, _, err := beginShopifyEventWithDB(db, "event-valid-lease", "message-valid-lease", "e2e/unsupported", "", "owned.myshopify.com", "https", json.RawMessage(`{"owned":true}`), now)
			if err != nil {
				t.Fatal(err)
			}
			var processErr error
			if state == "failed" {
				processErr = gorm.ErrInvalidData
			}
			finished := now.Add(time.Second)
			if err := finishShopifyEventWithDB(db, event.EventID, event.ProcessingToken, state, processErr, finished); err != nil {
				t.Fatal(err)
			}
			var actual models.ShopifyEvent
			if err := db.Where("event_id = ?", event.EventID).First(&actual).Error; err != nil {
				t.Fatal(err)
			}
			var attempt models.ShopifyEventAttempt
			if err := db.Where("event_id = ?", event.EventID).First(&attempt).Error; err != nil {
				t.Fatal(err)
			}
			if actual.Status != state || actual.ProcessingToken != "" || !actual.LeaseUntil.Equal(finished) || actual.LeaseUntil.After(finished) || actual.AttemptCount != 1 || attempt.Status != state || attempt.FinishedAt == nil || !attempt.FinishedAt.Equal(finished) {
				t.Fatalf("finish must atomically release valid lease and finish one owned attempt: %+v / %+v", actual, attempt)
			}
			if state == "ignored" && actual.CompletedAt == nil || state == "failed" && (actual.CompletedAt != nil || actual.LastError != gorm.ErrInvalidData.Error()) {
				t.Fatal("original completed/error contract changed")
			}
		})
	}
}
