package events

import "github.com/flaboy/aira-shop/pkg/types"

import "gorm.io/gorm"

type EventHandler interface {
	OnShopConnected(event *types.ShopConnectedEvent) error
	OnProductPublished(event *types.ProductPublishedEvent) error
	OnProductPublishedTx(tx *gorm.DB, event *types.ProductPublishedEvent) error
	OnOrderReceived(event *types.OrderReceivedEvent) error
	OnOrderUpdated(event *types.OrderStatusChangedEvent) error
	OnOrderCancelled(event *types.OrderStatusChangedEvent) error
	OnOrderFulfilled(event *types.OrderStatusChangedEvent) error
	OnPaymentCompleted(event *types.PaymentCompletedEvent) error
}

var handler EventHandler

func SetEventHandler(h EventHandler) {
	handler = h
}

func EmitShopConnected(event *types.ShopConnectedEvent) error {
	if handler != nil {
		return handler.OnShopConnected(event)
	}
	return nil
}

func EmitProductPublished(event *types.ProductPublishedEvent) error {
	if handler != nil {
		return handler.OnProductPublished(event)
	}
	return nil
}

func EmitProductPublishedTx(tx *gorm.DB, event *types.ProductPublishedEvent) error {
	if handler != nil {
		return handler.OnProductPublishedTx(tx, event)
	}
	return nil
}

func EmitOrderReceived(event *types.OrderReceivedEvent) error {
	if handler != nil {
		return handler.OnOrderReceived(event)
	}
	return nil
}

func EmitOrderUpdated(event *types.OrderStatusChangedEvent) error {
	if handler != nil {
		return handler.OnOrderUpdated(event)
	}
	return nil
}

func EmitOrderCancelled(event *types.OrderStatusChangedEvent) error {
	if handler != nil {
		return handler.OnOrderCancelled(event)
	}
	return nil
}

func EmitOrderFulfilled(event *types.OrderStatusChangedEvent) error {
	if handler != nil {
		return handler.OnOrderFulfilled(event)
	}
	return nil
}

func EmitPaymentCompleted(event *types.PaymentCompletedEvent) error {
	if handler != nil {
		return handler.OnPaymentCompleted(event)
	}
	return nil
}
