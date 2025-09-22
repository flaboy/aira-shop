package errors

import "github.com/flaboy/pin/usererrors"

// Shop相关错误
var (
	ErrShopNameEmpty            = usererrors.New("shop.name_empty", "Shop name is empty")
	ErrNonceGeneration          = usererrors.New("shop.nonce_generation_failed", "Failed to generate nonce")
	ErrAuthURLGeneration        = usererrors.New("shop.auth_url_generation_failed", "Failed to generate authorization URL")
	ErrInvalidCallbackSignature = usererrors.New("shop.invalid_callback_signature", "Invalid callback signature")
	ErrAccessTokenFailed        = usererrors.New("shop.access_token_failed", "Failed to get access token")
	ErrShopifyClientCreation    = usererrors.New("shop.shopify_client_creation_failed", "Failed to create Shopify client")
	ErrShopInfoFailed           = usererrors.New("shop.shop_info_failed", "Failed to get shop info")
	ErrWebhookSubscription      = usererrors.New("shop.webhook_subscription_failed", "Failed to subscribe webhooks")
	ErrCredentialsMarshal       = usererrors.New("shop.credentials_marshal_failed", "Failed to marshal credentials")
	ErrShopCreation             = usererrors.New("shop.creation_failed", "Failed to create shop")
	ErrPlatformNotSupported     = usererrors.New("shop.platform_not_supported", "Unsupported platform")
	ErrPlatformNotFound         = usererrors.New("shop.platform_not_found", "Platform not found")
)
