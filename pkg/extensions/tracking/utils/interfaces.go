package utils

import "github.com/flaboy/pin"

// TrackingProvider 定义追踪服务提供商的接口
type TrackingProvider interface {
	// 初始化服务
	Init() error

	// 开始追踪指定的追踪号
	StartTracking(trackingNumber string) error

	// 处理来自服务商的webhook请求
	HandleRequest(c *pin.Context, path string) error

	GetTrackingUrl(trackingNumber string) string

	// 获取服务商名称
	GetProviderName() string
}
