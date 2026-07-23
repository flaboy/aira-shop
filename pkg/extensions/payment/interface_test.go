package payment

import (
	"testing"

	"github.com/flaboy/aira-shop/pkg/config"
)

func TestInitSkipsDisabledPayPal(t *testing.T) {
	originalConfig := config.Config
	t.Cleanup(func() {
		config.Config = originalConfig
	})

	config.Config = &config.CommenceConfig{}
	config.Config.PayPal.Enabled = false

	Init()

	if len(GetAvailableChannels()) != 0 {
		t.Fatal("禁用 PayPal 后不应注册支付渠道")
	}
	if Get("paypal") != nil {
		t.Fatal("禁用 PayPal 后不应返回 PayPal 渠道")
	}
}
