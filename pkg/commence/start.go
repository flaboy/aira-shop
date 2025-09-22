package commence

import (
	"github.com/flaboy/aira-shop/pkg/config"
	"github.com/flaboy/aira-shop/pkg/events"
	"github.com/flaboy/aira-shop/pkg/extensions/payment"
	"github.com/flaboy/aira-shop/pkg/extensions/shoplink"
	"github.com/flaboy/aira-shop/pkg/extensions/tracking"
)

func Start(cfg *config.CommenceConfig) error {
	config.Config = cfg

	// 启动服务组件
	shoplink.Init()
	payment.Init()
	tracking.Init()

	return nil
}

// 注册业务系统的事件处理器
func RegisterEventHandler(handler events.EventHandler) {
	events.SetEventHandler(handler)
}
