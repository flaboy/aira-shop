package shoplink

import (
	"fmt"

	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/shopify"
)

var platforms map[string]Platform

var _ ShopPlatform = (*shopify.Shopify)(nil)

func Init() error {
	platforms = make(map[string]Platform)

	// 注册Shopify平台
	return Register(&shopify.Shopify{})
}

func Register(platform Platform) error {
	if platform == nil || platform.GetPlatformName() == "" {
		return fmt.Errorf("shop platform registration is incomplete")
	}
	if platforms == nil {
		platforms = make(map[string]Platform)
	}
	name := platform.GetPlatformName()
	if _, exists := platforms[name]; exists {
		return fmt.Errorf("shop platform already registered: %s", name)
	}
	if err := platform.Init(); err != nil {
		return fmt.Errorf("initialize shop platform %s: %w", name, err)
	}
	platforms[name] = platform
	return nil
}

func getPlatform(platformName string) Platform {
	return platforms[platformName]
}

func GetAuthorizationProvider(platformName string) AuthorizationProvider {
	provider, _ := getPlatform(platformName).(AuthorizationProvider)
	return provider
}

func GetOAuthProvider(platformName string) OAuthProvider {
	provider, _ := getPlatform(platformName).(OAuthProvider)
	return provider
}

func GetScopedOAuthProvider(platformName string) ScopedOAuthProvider {
	provider, _ := getPlatform(platformName).(ScopedOAuthProvider)
	return provider
}

func GetProductProvider(platformName string) ProductProvider {
	provider, _ := getPlatform(platformName).(ProductProvider)
	return provider
}

func GetPublicationProvider(platformName string) PublicationProvider {
	provider, _ := getPlatform(platformName).(PublicationProvider)
	return provider
}

func GetPublicationConfigurationProvider(platformName string) PublicationConfigurationProvider {
	provider, _ := getPlatform(platformName).(PublicationConfigurationProvider)
	return provider
}

func GetOrderProvider(platformName string) OrderProvider {
	provider, _ := getPlatform(platformName).(OrderProvider)
	return provider
}

func GetFulfillmentProvider(platformName string) FulfillmentProvider {
	provider, _ := getPlatform(platformName).(FulfillmentProvider)
	return provider
}

func GetWebhookProvider(platformName string) WebhookProvider {
	provider, _ := getPlatform(platformName).(WebhookProvider)
	return provider
}

func GetSupportedPlatforms() []string {
	var names []string
	for name := range platforms {
		names = append(names, name)
	}
	return names
}
