package payment

import (
	"github.com/flaboy/aira/aira-shop/pkg/extensions/payment/paypal"
	"github.com/flaboy/aira/aira-shop/pkg/extensions/payment/types"
	"github.com/flaboy/pin"
)

type PaymentChannel interface {
	// 创建支付订单 - 移除对具体业务模型的依赖
	// businessContext 是 interface{}，由业务系统提供，支付系统只负责存储和传递
	CreatePayment(businessContext interface{}, amount int64, currency string) (*types.CreatePaymentResult, error)

	// 处理外部请求（回调和webhook）
	HandleRequest(c *pin.Context, path string) error

	// 资源初始化
	Init() error

	// 获取渠道名称
	GetChannelName() string
}

func Get(channel string) PaymentChannel {
	return paymentChannels[channel]
}

var paymentChannels map[string]PaymentChannel

func Init() {
	paymentChannels = make(map[string]PaymentChannel)
	paymentChannels["paypal"] = &paypal.PayPal{}

	for _, channel := range paymentChannels {
		if err := channel.Init(); err != nil {
			panic(err)
		}
	}
}

// GetAvailableChannels 获取所有可用的支付渠道
func GetAvailableChannels() []string {
	channels := make([]string, 0, len(paymentChannels))
	for name := range paymentChannels {
		channels = append(channels, name)
	}
	return channels
}
