package tracking

import (
	"errors"
	"regexp"

	"github.com/flaboy/aira/aira-shop/pkg/extensions/tracking/the17track"
	"github.com/flaboy/aira/aira-shop/pkg/extensions/tracking/utils"
)

type TrackRoute struct {
	Provider    utils.TrackingProvider `json:"provider"`
	Regex       string                 `json:"regex"`
	CompiledReg *regexp.Regexp         `json:"-"`
}

var providers map[string]TrackRoute

func Get(name string) utils.TrackingProvider {
	if providers == nil {
		return nil
	}
	route, exists := providers[name]
	if !exists {
		return nil
	}
	return route.Provider
}

func RegisterTrackRoute(name, regex string, provider utils.TrackingProvider) {
	if providers == nil {
		providers = make(map[string]TrackRoute)
	}
	if _, exists := providers[name]; exists {
		panic("Track route already registered: " + name)
	}

	// 预编译正则表达式
	compiledReg, err := regexp.Compile(regex)
	if err != nil {
		panic("Invalid regex for provider " + name + ": " + err.Error())
	}

	providers[name] = TrackRoute{
		Provider:    provider,
		Regex:       regex,
		CompiledReg: compiledReg,
	}
}

func Init() {
	// Initialize all registered providers
	the17TrackProvider := &the17track.The17Track{}
	RegisterTrackRoute("17track", ".*", the17TrackProvider)
}

// TODO: 当订单包裹号被添加时，调用此函数开始追踪
// tracking.StartTracking("YT2301234567890")
func StartTracking(trackingNumber string) error {
	if providers == nil {
		return errors.New("no tracking providers registered")
	}

	// 遍历所有注册的providers，使用正则匹配
	for name, route := range providers {
		if route.CompiledReg.MatchString(trackingNumber) {
			// 找到匹配的provider，调用其StartTracking方法
			err := route.Provider.StartTracking(trackingNumber)
			if err != nil {
				return errors.New("failed to start tracking with provider " + name + ": " + err.Error())
			}
			return nil
		}
	}

	// 没有找到匹配的provider
	return errors.New("no matching tracking provider found for: " + trackingNumber)
}
