package shopify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/config"
	"github.com/flaboy/aira-shop/pkg/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func beginShopifyEvent(eventID string, messageID string, topic string, externalShopID string, shopDomain string, deliveryMethod string, payload json.RawMessage) (*models.ShopifyEvent, bool, bool, error) {
	return beginShopifyEventWithDB(database.Database(), eventID, messageID, topic, externalShopID, shopDomain, deliveryMethod, payload, time.Now().UTC())
}

func beginShopifyEventWithDB(db *gorm.DB, eventID string, messageID string, topic string, externalShopID string, shopDomain string, deliveryMethod string, payload json.RawMessage, now time.Time) (*models.ShopifyEvent, bool, bool, error) {
	processingToken := messageID + "-" + strconv.FormatInt(now.UnixNano(), 10)
	event := &models.ShopifyEvent{}
	terminal := false
	busy := false
	err := db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ?", eventID).First(event).Error
		if err == nil && (event.Status == "success" || event.Status == "ignored") {
			terminal = true
			return nil
		}
		if err == nil && event.Status == "processing" && event.LeaseUntil.After(now) {
			busy = true
			return nil
		}
		if err == nil && event.Status == "requeueing" && event.LeaseUntil.After(now) {
			busy = true
			return nil
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == gorm.ErrRecordNotFound {
			event = &models.ShopifyEvent{EventID: eventID, ReceivedAt: now}
		}
		event.MessageID = messageID
		event.Topic = topic
		event.ExternalShopID = externalShopID
		event.ShopDomain = shopDomain
		event.DeliveryMethod = deliveryMethod
		event.Status = "processing"
		event.ProcessingToken = processingToken
		event.LeaseUntil = now.Add(15 * time.Minute)
		event.LastError = ""
		event.Payload = payload
		event.PayloadHash = fmt.Sprintf("%x", sha256.Sum256(payload))
		event.AttemptCount++
		event.LastAttemptAt = now
		event.CompletedAt = nil
		if err := tx.Save(event).Error; err != nil {
			return err
		}
		return tx.Create(&models.ShopifyEventAttempt{
			EventID: eventID, AttemptNo: event.AttemptCount, ProcessingToken: processingToken,
			Handler: topic, Status: "processing", StartedAt: now,
		}).Error
	})
	if err != nil {
		return nil, false, false, err
	}
	return event, terminal, busy, nil
}

func finishShopifyEvent(eventID string, processingToken string, status string, processErr error) error {
	return finishShopifyEventWithDB(database.Database(), eventID, processingToken, status, processErr, time.Now().UTC())
}

func finishShopifyEventWithDB(db *gorm.DB, eventID string, processingToken string, status string, processErr error, now time.Time) error {
	updates := map[string]interface{}{
		"status":           status,
		"completed_at":     &now,
		"processing_token": "",
		"lease_until":      time.Time{},
	}
	if processErr != nil {
		updates["last_error"] = processErr.Error()
		updates["completed_at"] = nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.ShopifyEvent{}).Where("event_id = ? AND processing_token = ?", eventID, processingToken).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("Shopify event %s processing lease was lost", eventID)
		}
		attemptUpdates := map[string]interface{}{"status": status, "finished_at": &now}
		if processErr != nil {
			attemptUpdates["error_message"] = processErr.Error()
		}
		return tx.Model(&models.ShopifyEventAttempt{}).Where("processing_token = ?", processingToken).Updates(attemptUpdates).Error
	})
}

func resolveShopifyEventExternalShopID(payload json.RawMessage) (string, error) {
	return resolveShopifyEventExternalShopIDWithDB(database.Database(), payload)
}

func resolveShopifyEventExternalShopIDWithDB(db *gorm.DB, payload json.RawMessage) (string, error) {
	var order struct {
		LineItems []struct {
			ProductID uint64 `json:"product_id"`
		} `json:"line_items"`
	}
	if err := json.Unmarshal(payload, &order); err != nil {
		return "", fmt.Errorf("parse Shopify event identity: %w", err)
	}
	if len(order.LineItems) == 0 {
		return "", fmt.Errorf("Shopify event has no product identity")
	}
	shopLinkID := uint(0)
	for _, line := range order.LineItems {
		var product models.ShopProduct
		if err := db.Where("platform = ? AND outer_id = ?", "shopify", strconv.FormatUint(line.ProductID, 10)).First(&product).Error; err != nil {
			return "", fmt.Errorf("resolve Shopify product %d store identity: %w", line.ProductID, err)
		}
		if shopLinkID != 0 && shopLinkID != product.ShopID {
			return "", fmt.Errorf("Shopify event contains products from multiple store identities")
		}
		shopLinkID = product.ShopID
	}
	var shop models.ShopLink
	if err := db.Where("id = ? AND platform = ?", shopLinkID, "shopify").First(&shop).Error; err != nil {
		return "", fmt.Errorf("resolve Shopify event authorization identity: %w", err)
	}
	if shop.ExternalShopID == "" {
		return "", fmt.Errorf("Shopify event authorization has no external shop identity")
	}
	return shop.ExternalShopID, nil
}

func RequeueEvent(eventID string) error {
	event := &models.ShopifyEvent{}
	now := time.Now().UTC()
	requeueToken := "admin-requeue-" + strconv.FormatInt(now.UnixNano(), 10)
	if err := database.Database().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ? AND status = ?", eventID, "failed").First(event).Error; err != nil {
			return err
		}
		return tx.Model(event).Updates(map[string]interface{}{
			"status": "requeueing", "processing_token": requeueToken, "lease_until": now.Add(5 * time.Minute),
		}).Error
	}); err != nil {
		return err
	}
	ctx := context.Background()
	cfg, err := awsConfig.LoadDefaultConfig(ctx,
		awsConfig.WithRegion(config.Config.Shopify.AWSRegion),
		awsConfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			config.Config.Shopify.AWSAccessKey,
			config.Config.Shopify.AWSSecret,
			"",
		)),
	)
	if err != nil {
		return fmt.Errorf("load Shopify SQS config: %w", err)
	}
	client := sqs.NewFromConfig(cfg)
	if _, err := client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(config.Config.Shopify.SQSQueueURL),
		MessageBody: aws.String(string(event.Payload)),
	}); err != nil {
		if restoreErr := database.Database().Model(event).Where("processing_token = ?", requeueToken).Updates(map[string]interface{}{
			"status": "failed", "processing_token": "", "lease_until": time.Time{},
		}).Error; restoreErr != nil {
			return fmt.Errorf("restore Shopify event after requeue failure: %w", restoreErr)
		}
		return fmt.Errorf("requeue Shopify event: %w", err)
	}
	return database.Database().Model(event).Where("processing_token = ?", requeueToken).Updates(map[string]interface{}{
		"status": "queued", "last_error": "", "processing_token": "", "lease_until": time.Time{},
	}).Error
}
