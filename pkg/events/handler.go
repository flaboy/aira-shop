package events

import "github.com/flaboy/aira/aira-shop/pkg/types"

type EventHandler interface {
	OnShopConnected(event *types.ShopConnectedEvent) error
	OnProductPublished(event *types.ProductPublishedEvent) error
	OnOrderReceived(event *types.OrderReceivedEvent) error
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

func EmitOrderReceived(event *types.OrderReceivedEvent) error {
	if handler != nil {
		return handler.OnOrderReceived(event)
	}
	return nil
}

func EmitPaymentCompleted(event *types.PaymentCompletedEvent) error {
	if handler != nil {
		return handler.OnPaymentCompleted(event)
	}
	return nil
}
