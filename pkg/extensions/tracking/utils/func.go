package utils

import (
	"log/slog"

	"github.com/flaboy/aira-web/pkg/helper"
)

func GetPublicUrl(provider, path string) string {
	return helper.BuildUrl("/tracking/" + provider + "/" + path)
}

// UpdateStatus 更新追踪状态 - 现在使用回调机制
func UpdateStatus(trackingNumber string, status TrackingStatus) error {
	slog.Info("Updating status for tracking number", "trackingNumber", trackingNumber, "status", status)

	// 使用回调机制通知状态更新，具体的业务逻辑由主项目注册的回调处理
	// 这样tracking模块就不会依赖任何业务相关的代码
	return NotifyStatusUpdate(trackingNumber, status)
}

// StatusUpdateCallback 状态更新回调函数类型
type StatusUpdateCallback func(trackingNumber string, status TrackingStatus) error

// callbackRegistry 回调注册表
var callbackRegistry []StatusUpdateCallback

// RegisterStatusUpdateCallback 注册状态更新回调
func RegisterStatusUpdateCallback(callback StatusUpdateCallback) {
	callbackRegistry = append(callbackRegistry, callback)
	slog.Info("Registered tracking status update callback", "totalCallbacks", len(callbackRegistry))
}

// NotifyStatusUpdate 通知所有已注册的回调
func NotifyStatusUpdate(trackingNumber string, status TrackingStatus) error {
	slog.Info("Notifying callbacks for tracking number", "callbackCount", len(callbackRegistry), "trackingNumber", trackingNumber, "status", status)

	// 执行所有回调
	for i, callback := range callbackRegistry {
		if err := callback(trackingNumber, status); err != nil {
			slog.Error("Callback failed for tracking number", "callbackIndex", i, "trackingNumber", trackingNumber, "error", err)
			// 继续执行其他回调，不因一个失败而中断
		}
	}

	return nil
}

// ClearCallbacks 清除所有回调（主要用于测试）
func ClearCallbacks() {
	callbackRegistry = callbackRegistry[:0]
	slog.Info("Cleared all tracking status update callbacks")
}
