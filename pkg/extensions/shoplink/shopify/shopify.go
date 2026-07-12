package shopify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/config"
	"github.com/flaboy/aira-shop/pkg/errors"
	"github.com/flaboy/aira-shop/pkg/events"
	"github.com/flaboy/aira-shop/pkg/extensions/shoplink/utils"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/flaboy/pin"
	"github.com/flaboy/pin/usererrors"
	"github.com/shopspring/decimal"
	"github.com/spf13/cast"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	goshopify "github.com/bold-commerce/go-shopify/v4"
	shopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-web/pkg/helper"
	"gorm.io/gorm"
)

var app *shopify.App

var dec100 = decimal.NewFromInt(100)

const (
	sizeGuideMetafieldNamespace = "effiprint"
	sizeGuideMetafieldKey       = "size_guide"
)

func (p *Shopify) Init() error {
	if !config.Config.Shopify.Enabled {
		return nil
	}

	// 创建一个自定义的HTTP客户端，增加超时时间
	p.httpClient = &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			TLSHandshakeTimeout:   60 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			ExpectContinueTimeout: 30 * time.Second,
			DisableKeepAlives:     false,
			MaxIdleConnsPerHost:   10,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
		},
	}

	app = &shopify.App{
		ApiKey:      config.Config.Shopify.ApiKey,
		ApiSecret:   config.Config.Shopify.ApiSecret,
		RedirectUrl: helper.BuildUrl("stores/callback/shopify"),
		Scope:       "read_products,write_products,read_orders,write_orders",
	}

	go p.StartEventListener()
	return nil
}

func (p *Shopify) GetPlatformName() string {
	return "shopify"
}

type Shopify struct {
	httpClient *http.Client
}

// subscribeWebhooks 为店铺订阅所需的webhook
func (p *Shopify) subscribeWebhooks(client *shopify.Client) error {
	ctx := context.Background()
	topics := []string{
		"orders/create",
		"orders/updated",
		"orders/paid",
		"orders/cancelled",
		"orders/fulfilled",
	}

	// 先获取现有的webhooks
	existingWebhooks, err := client.Webhook.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list existing webhooks: %v", err)
	}

	// 创建已存在的webhook映射，便于快速查找
	existingWebhookMap := make(map[string]bool)
	for _, webhook := range existingWebhooks {
		if webhook.Address == config.Config.Shopify.EventBridgeARN {
			existingWebhookMap[webhook.Topic] = true
		}
	}

	for _, topic := range topics {
		// 检查webhook是否已存在
		if existingWebhookMap[topic] {
			fmt.Printf("Webhook for topic %s already exists, skipping...\n", topic)
			continue
		}

		webhook := shopify.Webhook{
			Topic:   topic,
			Address: config.Config.Shopify.EventBridgeARN,
			Format:  "json",
		}

		result, err := client.Webhook.Create(ctx, webhook)
		if err != nil {
			// 即使检查了现有webhook，仍可能因为并发或其他原因失败
			// 如果是地址已存在的错误，记录警告但不中断流程
			if strings.Contains(strings.ToLower(err.Error()), "address") &&
				strings.Contains(strings.ToLower(err.Error()), "taken") {
				fmt.Printf("Warning: Webhook for topic %s already exists: %v\n", topic, err)
				continue
			}
			return fmt.Errorf("failed to create webhook for %s: %v", topic, err)
		}

		jsonData, _ := json.MarshalIndent(result, "", "  ")
		fmt.Printf("Webhook created for topic %s: \n%s\n", topic, string(jsonData))
	}
	return nil
}

type ShopifyCredential struct {
	Url         string
	AccessToken string
	ShopLinkID  uint
}

func (p *Shopify) HandleCallback(c *pin.Context, businessContext json.RawMessage, callbackUrl *url.URL) (*types.CallbackResponse, error) {
	if ok, _ := app.VerifyAuthorizationURL(callbackUrl); !ok {
		return nil, errors.ErrInvalidCallbackSignature
	}

	query := callbackUrl.Query()
	shopUrl := query.Get("shop")
	code := query.Get("code")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // 增加超时时间到120秒
	defer cancel()

	// 获取access token
	token, err := app.GetAccessToken(ctx, shopUrl, code)
	if err != nil {
		return nil, errors.ErrAccessTokenFailed
	}

	// 使用自定义HTTP客户端创建Shopify客户端
	client, err := shopify.NewClient(*app, shopUrl, token, shopify.WithHTTPClient(p.httpClient))
	if err != nil {
		return nil, errors.ErrShopifyClientCreation
	}

	shopInfo, err := client.Shop.Get(ctx, nil)
	if err != nil {
		return nil, errors.ErrShopInfoFailed
	}

	// 订阅webhook
	if err := p.subscribeWebhooks(client); err != nil {
		fmt.Printf("Shopify webhook subscription failed for shop %s: %v\n", shopUrl, err)
		return nil, errors.ErrWebhookSubscription
	}

	shopName := shopInfo.Name
	externalShopID := strconv.FormatUint(shopInfo.Id, 10)

	credentials := ShopifyCredential{
		Url:         shopUrl,
		AccessToken: token,
	}

	credentialsJson, err := json.Marshal(credentials)
	if err != nil {
		return nil, errors.ErrCredentialsMarshal
	}

	// 直接创建ShopLink模型
	shopLink := &models.ShopLink{
		Platform:       "shopify",
		Name:           shopName,
		Url:            "https://" + shopUrl,
		ExternalShopID: externalShopID,
		Credentials:    credentialsJson,
	}

	db := database.Database()

	// 检查是否已存在
	var existing models.ShopLink
	err = db.Where("external_shop_id = ? AND platform = ?", externalShopID, "shopify").First(&existing).Error
	if err == nil {
		// Shopify 店铺名称和域名可变，授权复用只以 Shopify shop id 为准。
		existing.Name = shopName
		existing.Credentials = credentialsJson
		existing.Url = "https://" + shopUrl
		existing.ExternalShopID = externalShopID
		if err := db.Save(&existing).Error; err != nil {
			return nil, errors.ErrShopCreation
		}
		shopLink = &existing
	} else if err != gorm.ErrRecordNotFound {
		return nil, errors.ErrShopCreation
	} else {
		// 创建新记录
		if err := db.Create(shopLink).Error; err != nil {
			return nil, errors.ErrShopCreation
		}
	}

	// 触发店铺连接事件
	shopData := map[string]interface{}{
		"name": shopName,
		"url":  "https://" + shopUrl,
	}

	events.EmitShopConnected(&types.ShopConnectedEvent{
		ShopID:          shopLink.ID,
		Platform:        "shopify",
		ShopData:        shopData,
		BusinessContext: businessContext,
		CreatedAt:       time.Now(),
	})

	return &types.CallbackResponse{
		Type: types.CallbackResponseTypeShopLinked,
		ShopLinkedData: &types.ShopLinkedData{
			ShopLink: shopLink,
		},
	}, nil
}

func generateNonce() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return hex.EncodeToString(nonce), nil
}

func (p *Shopify) HandleRequest(c *pin.Context, path string) (*types.HandleRequestResult, error) {
	shopName := c.Query("shop")
	if shopName == "" {
		return nil, errors.ErrShopNameEmpty
	}
	state, err := generateNonce()
	if err != nil {
		return nil, errors.ErrNonceGeneration
	}
	authUrl, err := app.AuthorizeUrl(shopName, state)
	if err != nil {
		return nil, errors.ErrAuthURLGeneration
	}

	return &types.HandleRequestResult{
		AuthURL: authUrl,
	}, nil
}

type ShopifyRemoteData struct {
	VariantMapper map[uint64]uint
}

func (p *Shopify) PutProduct(credential *types.ShopCredential, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error) {
	if product.OriginProductID == "" || product.PublishOperationID == "" || product.ExternalShopID == "" {
		return nil, usererrors.New("Shopify product publish identity is incomplete")
	}
	// Unmarshal the credentials
	var creds ShopifyCredential
	credData, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal credentials: %s", err.Error()))
	}

	if err := json.Unmarshal(credData, &creds); err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to unmarshal credentials: %s", err.Error()))
	}

	// Create a new Shopify client
	client, err := shopify.NewClient(*app, creds.Url, creds.AccessToken)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to create Shopify client: %s", err.Error()))
	}

	// Create a new product
	newProduct, err := p.toShopifyProduct(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to convert product: %s", err.Error()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // 增加超时时间到120秒
	defer cancel()

	variantMap := map[string]uint{}
	for _, variant := range newProduct.Variants {
		optionUniqId := p.variantUniqId(&variant)
		var originVariantId uint
		for _, meta := range variant.Metafields {
			if meta.Namespace == "aira-shop" && meta.Key == "origin" {
				originVariantId = cast.ToUint(meta.Value)
				break
			}
		}

		if originVariantId > 0 {
			variantMap[optionUniqId] = originVariantId
		}
	}

	productResp, err := client.Product.Create(ctx, newProduct)
	if err != nil {
		recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer recoveryCancel()
		matches, auditErr := findProductsByPublishOperation(recoveryCtx, client, product.PublishOperationID)
		if auditErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("Shopify product creation result is uncertain and cannot be audited: %w", auditErr)}
		}
		if len(matches) > 1 {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("Shopify product creation produced %d products for operation %s", len(matches), product.PublishOperationID)}
		}
		if len(matches) == 0 {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("Shopify product creation result is uncertain for operation %s: %w", product.PublishOperationID, err)}
		}
		if len(matches) == 1 {
			if deleteErr := deleteAndVerifyShopifyProduct(recoveryCtx, client, matches[0].Id); deleteErr != nil {
				return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to remove uncertain Shopify product %d: %w", matches[0].Id, deleteErr)}
			}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to create product: %s", err.Error()))
	}
	verifiedProduct, err := client.Product.Get(ctx, productResp.Id, nil)
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete unverified Shopify product %d: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to verify created Shopify product %d: %s", productResp.Id, err.Error()))
	}
	metafields, err := client.Product.ListMetafields(ctx, productResp.Id, nil)
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d after identity verification failure: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to read Shopify product %d identity: %s", productResp.Id, err.Error()))
	}
	verifiedIdentity := map[string]string{}
	for _, metafield := range metafields {
		if metafield.Namespace == "effiprint" {
			verifiedIdentity[metafield.Key] = fmt.Sprint(metafield.Value)
		}
	}
	if verifiedIdentity["origin_product_id"] != product.OriginProductID || verifiedIdentity["publish_operation_id"] != product.PublishOperationID || verifiedIdentity["external_shop_id"] != product.ExternalShopID {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d with invalid identity: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Shopify product %d identity verification failed", productResp.Id))
	}

	ShopifyRemoteData := &ShopifyRemoteData{
		VariantMapper: make(map[uint64]uint),
	}

	for _, variant := range verifiedProduct.Variants {
		optionUniqId := p.variantUniqId(&variant)
		if variantId, ok := variantMap[optionUniqId]; ok {
			ShopifyRemoteData.VariantMapper[variant.Id] = variantId
		}
	}
	if len(ShopifyRemoteData.VariantMapper) != len(newProduct.Variants) {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete incomplete Shopify product %d: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Shopify variant mapping is incomplete: expected %d, mapped %d", len(newProduct.Variants), len(ShopifyRemoteData.VariantMapper)))
	}

	// 获取店铺ID
	var shop models.ShopLink
	db := database.Database()
	err = db.Where("id = ? AND platform = ?", creds.ShopLinkID, "shopify").First(&shop).Error
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d after authorization lookup failure: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to find shop: %s", err.Error()))
	}

	// 保存产品信息
	shopProduct := models.ShopProduct{
		ShopID:             shop.ID,
		PublishOperationID: product.PublishOperationID,
		OuterID:            fmt.Sprintf("%d", productResp.Id),
		Status:             "active",
		Url:                fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
		Name:               productResp.Title,
		Platform:           "shopify",
	}

	// 序列化产品数据
	productData, err := json.Marshal(product)
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d after product data failure: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal product data: %s", err.Error()))
	}
	shopProduct.Data = productData

	// 序列化远程数据
	remoteData, err := json.Marshal(ShopifyRemoteData)
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d after variant data failure: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal remote data: %s", err.Error()))
	}
	shopProduct.RemoteData = remoteData

	// 触发产品发布事件
	productDataMap := map[string]interface{}{
		"product_name": product.ProductName,
		"body_html":    product.BodyHTML,
		"tags":         product.Tags,
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&shopProduct).Error; err != nil {
			return err
		}
		variantMappings := make([]models.ShopifyVariantMapping, 0, len(ShopifyRemoteData.VariantMapper))
		for shopifyVariantID, localVariantID := range ShopifyRemoteData.VariantMapper {
			variantMappings = append(variantMappings, models.ShopifyVariantMapping{
				ShopID: shop.ID, ShopProductID: shopProduct.ID,
				ShopifyVariantID: shopifyVariantID, LocalVariantID: localVariantID,
			})
		}
		if len(variantMappings) != len(newProduct.Variants) {
			return fmt.Errorf("Shopify variant mapping transaction is incomplete")
		}
		if err := tx.Create(&variantMappings).Error; err != nil {
			return err
		}
		return events.EmitProductPublishedTx(tx, &types.ProductPublishedEvent{
			ShopProductID: shopProduct.ID, ShopID: shop.ID, Platform: "shopify", OuterID: fmt.Sprintf("%d", productResp.Id),
			ProductData: productDataMap, BusinessContext: businessContext, CreatedAt: time.Now(),
		})
	}); err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete Shopify product %d after mapping failure: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to save Shopify product mapping: %s", err.Error()))
	}

	return &types.PutProductResult{
		CommandResult: types.CommandResult{
			Success: true,
			Message: "Product created successfully",
		},
		OuterID:       fmt.Sprintf("%d", productResp.Id),
		ShopProductID: shopProduct.ID,
		Url:           fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
		RemoteData:    ShopifyRemoteData,
	}, nil
}

func (p *Shopify) UpdateProduct(credential *types.ShopCredential, outerID string, product *types.ProductData, businessContext json.RawMessage) (*types.PutProductResult, error) {
	var creds ShopifyCredential
	credData, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal credentials: %s", err.Error()))
	}

	if err := json.Unmarshal(credData, &creds); err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to unmarshal credentials: %s", err.Error()))
	}

	client, err := shopify.NewClient(*app, creds.Url, creds.AccessToken)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to create Shopify client: %s", err.Error()))
	}

	shopifyProduct, err := p.toShopifyProduct(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to convert product: %s", err.Error()))
	}

	productID := cast.ToUint64(outerID)
	if productID == 0 {
		return nil, usererrors.New("Invalid Shopify product id")
	}
	shopifyProduct.Id = productID

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	productResp, err := client.Product.Update(ctx, shopifyProduct)
	if err != nil {
		return nil, normalizeProductUpdateError(err)
	}

	productData, err := json.Marshal(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal product data: %s", err.Error()))
	}

	db := database.Database()
	if err := db.Model(&models.ShopProduct{}).
		Where("platform = ? AND outer_id = ?", "shopify", outerID).
		Updates(map[string]interface{}{
			"name":       productResp.Title,
			"status":     "active",
			"url":        fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
			"data":       productData,
			"updated_at": time.Now(),
		}).Error; err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to update shop product: %s", err.Error()))
	}

	return &types.PutProductResult{
		CommandResult: types.CommandResult{
			Success: true,
			Message: "Product updated successfully",
		},
		OuterID: fmt.Sprintf("%d", productResp.Id),
		Url:     fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
	}, nil
}

// normalizeProductUpdateError 将 Shopify 404 转为稳定业务文案，供异步发布状态安全返回给商城端。
func normalizeProductUpdateError(err error) error {
	var responseErr goshopify.ResponseError
	if stderrors.As(err, &responseErr) && responseErr.Status == http.StatusNotFound {
		return stderrors.New("This Shopify product no longer exists. Publish it as a new product instead.")
	}
	return fmt.Errorf("Failed to update product: %w", err)
}

func (p *Shopify) DeleteProduct(credential *types.ShopCredential, outerID string) (*types.DeleteProductResult, error) {
	var creds ShopifyCredential
	credData, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal credentials: %s", err.Error()))
	}

	if err := json.Unmarshal(credData, &creds); err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to unmarshal credentials: %s", err.Error()))
	}

	client, err := shopify.NewClient(*app, creds.Url, creds.AccessToken)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to create Shopify client: %s", err.Error()))
	}

	productID := cast.ToUint64(outerID)
	if productID == 0 {
		return nil, usererrors.New("Invalid Shopify product id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if err := deleteAndVerifyShopifyProduct(ctx, client, productID); err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to delete product: %s", err.Error()))
	}

	return &types.DeleteProductResult{
		CommandResult: types.CommandResult{
			Success: true,
			Message: "Product deleted successfully",
		},
		OuterID: outerID,
	}, nil
}

func (p *Shopify) variantUniqId(v *shopify.Variant) string {
	return strings.Join([]string{
		v.Option1,
		v.Option2,
		v.Option3,
	}, "-")
}

func convertDecimal(v int64) *decimal.Decimal {
	v2 := decimal.NewFromInt(v).Div(dec100)
	return &v2
}

func (p *Shopify) toShopifyProduct(product *types.ProductData) (shopify.Product, error) {
	// 创建发布时间
	publishedAt := time.Now()

	options := []shopify.ProductOption{}
	for _, opt := range product.Options {
		option := shopify.ProductOption{
			Name:   opt.Name,
			Values: opt.Values,
		}
		options = append(options, option)
	}

	// 转换变体信息
	var variants []shopify.Variant
	for _, v := range product.Variants {
		variant := shopify.Variant{
			Sku:             v.Sku,
			Title:           v.Title,
			RequireShipping: true,
			Price:           v.Price,
			CompareAtPrice:  v.CompareAtPrice,
			Weight:          v.Weight,
			WeightUnit:      v.WeightUnit,
			Metafields: []shopify.Metafield{
				{
					Namespace: "aira-shop",
					Key:       "origin",
					Type:      shopify.MetafieldTypeSingleLineTextField,
					Value:     v.ID,
				},
			},
		}

		// 设置选项
		if v.Option1 != "" {
			variant.Option1 = v.Option1
		}
		if v.Option2 != "" {
			variant.Option2 = v.Option2
		}
		if v.Option3 != "" {
			variant.Option3 = v.Option3
		}

		variants = append(variants, variant)
	}

	// 转换图片信息
	var images []shopify.Image
	if product.Image.Src != "" {
		images = append(images, shopify.Image{
			Width:    product.Image.Width,
			Height:   product.Image.Height,
			Src:      product.Image.Src,
			Alt:      product.Image.Alt,
			Filename: product.Image.Filename,
		})
	}
	for _, img := range product.Images {
		if img.Src == "" {
			continue
		}
		images = append(images, shopify.Image{
			Width:    img.Width,
			Height:   img.Height,
			Src:      img.Src,
			Alt:      img.Alt,
			Filename: img.Filename,
		})
	}

	shopifyProduct := shopify.Product{
		Title:          product.ProductName,
		BodyHTML:       product.BodyHTML,
		Status:         shopify.ProductStatusActive,
		PublishedAt:    &publishedAt,
		PublishedScope: "web",
		Options:        options,
		Tags:           product.Tags,
		Variants:       variants,
		Images:         images,
	}
	identityMetafields := []struct {
		key   string
		value string
	}{
		{key: "origin_product_id", value: product.OriginProductID},
		{key: "publish_operation_id", value: product.PublishOperationID},
		{key: "external_shop_id", value: product.ExternalShopID},
	}
	for _, identity := range identityMetafields {
		if identity.value == "" {
			continue
		}
		shopifyProduct.Metafields = append(shopifyProduct.Metafields, shopify.Metafield{
			Namespace: "effiprint",
			Key:       identity.key,
			Type:      shopify.MetafieldTypeSingleLineTextField,
			Value:     identity.value,
		})
	}

	if product.SizeGuideEnabled {
		// 将商品 Size Guide 写入 Shopify 产品 metafield，店铺主题可据此渲染独立 tab。
		shopifyProduct.Metafields = append(shopifyProduct.Metafields, shopify.Metafield{
			Namespace: sizeGuideMetafieldNamespace,
			Key:       sizeGuideMetafieldKey,
			Type:      shopify.MetafieldTypeMultiLineTextField,
			Value:     product.SizeGuideHTML,
		})
	}

	return shopifyProduct, nil
}

func (p *Shopify) StartEventListener() {
	// 创建 AWS 配置和 SQS 客户端
	fmt.Println("Starting Shopify event listener...")
	ctx := context.Background()

	// 使用Shopify专用的AWS凭证
	var cfg aws.Config
	var err error

	if config.Config.Shopify.AWSAccessKey != "" && config.Config.Shopify.AWSSecret != "" {
		// 使用Shopify专用的AWS凭证
		fmt.Printf("Using Shopify-specific AWS credentials for region: %s\n", config.Config.Shopify.AWSRegion)
		cfg, err = awsConfig.LoadDefaultConfig(ctx,
			awsConfig.WithRegion(config.Config.Shopify.AWSRegion),
			awsConfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				config.Config.Shopify.AWSAccessKey,
				config.Config.Shopify.AWSSecret,
				"",
			)),
		)
	} else {
		// 回退到默认配置
		fmt.Printf("Using default AWS credentials for region: %s\n", config.Config.Shopify.AWSRegion)
		cfg, err = awsConfig.LoadDefaultConfig(ctx,
			awsConfig.WithRegion(config.Config.Shopify.AWSRegion),
		)
	}

	if err != nil {
		fmt.Printf("Error loading AWS config: %v\n", err)
		return
	}

	client := sqs.NewFromConfig(cfg)
	fmt.Printf("AWS SQS client created successfully for queue: %s\n", config.Config.Shopify.SQSQueueURL)
	queueAttributes, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(config.Config.Shopify.SQSQueueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameRedrivePolicy},
	})
	if err != nil {
		fmt.Printf("Error reading Shopify SQS redrive policy: %v\n", err)
		return
	}
	if queueAttributes.Attributes[string(sqstypes.QueueAttributeNameRedrivePolicy)] == "" {
		fmt.Println("Shopify SQS consumer stopped: queue redrive policy and DLQ are required")
		return
	}

	for {
		// 接收消息
		output, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(config.Config.Shopify.SQSQueueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // 使用长轮询
			VisibilityTimeout:   900,
		})

		if err != nil {
			fmt.Printf("Error receiving message from SQS: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		if len(output.Messages) > 0 {
			fmt.Printf("Received %d messages from SQS\n", len(output.Messages))
		}

		// 处理接收到的消息
		for _, message := range output.Messages {
			fmt.Printf("Processing message: %s\n", *message.MessageId)

			// AWS EventBridge 消息结构
			var eventBridgeMessage struct {
				Version    string        `json:"version"`
				ID         string        `json:"id"`
				DetailType string        `json:"detail-type"`
				Source     string        `json:"source"`
				Account    string        `json:"account"`
				Time       string        `json:"time"`
				Region     string        `json:"region"`
				Resources  []interface{} `json:"resources"`
				Detail     struct {
					Payload  json.RawMessage `json:"payload"`
					Metadata struct {
						ShopifyTopic string `json:"X-Shopify-Topic"`
					} `json:"metadata"`
				} `json:"detail"`
			}

			if err := json.Unmarshal([]byte(*message.Body), &eventBridgeMessage); err != nil {
				fmt.Printf("Error unmarshaling EventBridge message %s: %v\n", *message.MessageId, err)
				fmt.Printf("Message body: %s\n", *message.Body)
				if storedEvent, _, busy, storeErr := beginShopifyEvent(*message.MessageId, *message.MessageId, "invalid", "", json.RawMessage(*message.Body)); storeErr != nil {
					fmt.Printf("Error recording invalid Shopify event %s: %v\n", *message.MessageId, storeErr)
				} else if !busy {
					if storeErr := finishShopifyEvent(*message.MessageId, storedEvent.ProcessingToken, "failed", err); storeErr != nil {
						fmt.Printf("Error recording invalid Shopify event result %s: %v\n", *message.MessageId, storeErr)
					}
				}
				continue
			}

			topic := eventBridgeMessage.Detail.Metadata.ShopifyTopic
			payload := eventBridgeMessage.Detail.Payload

			fmt.Printf("Processing EventBridge webhook event - Topic: %s, Source: %s, EventID: %s\n",
				topic, eventBridgeMessage.Source, eventBridgeMessage.ID)
			fmt.Printf("EventBridge message details - Version: %s, Time: %s, Region: %s\n",
				eventBridgeMessage.Version, eventBridgeMessage.Time, eventBridgeMessage.Region)

			eventID := eventBridgeMessage.ID
			if eventID == "" {
				eventID = *message.MessageId
			}
			externalShopID := ""
			var identityErr error
			if isHandledShopifyOrderTopic(topic) {
				externalShopID, identityErr = resolveShopifyEventExternalShopID(payload)
			}
			storedEvent, terminal, busy, err := beginShopifyEvent(eventID, *message.MessageId, topic, externalShopID, json.RawMessage(*message.Body))
			if err != nil {
				fmt.Printf("Error starting Shopify event %s: %v\n", eventID, err)
				continue
			}
			if busy {
				continue
			}
			if !terminal {
				if identityErr != nil {
					if err := finishShopifyEvent(eventID, storedEvent.ProcessingToken, "failed", identityErr); err != nil {
						fmt.Printf("Error recording Shopify event identity failure %s: %v\n", eventID, err)
					}
					continue
				}
				status, processErr := p.handleWebhookTopic(topic, payload)
				if err := finishShopifyEvent(eventID, storedEvent.ProcessingToken, status, processErr); err != nil {
					fmt.Printf("Error finishing Shopify event %s: %v\n", eventID, err)
					continue
				}
				if processErr != nil {
					fmt.Printf("Error handling Shopify event %s (%s): %v\n", eventID, topic, processErr)
					continue
				}
			}

			// 只有成功或明确忽略的终态事件才能确认 SQS 消息。
			fmt.Printf("Deleting processed message: %s\n", *message.MessageId)
			_, err = client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(config.Config.Shopify.SQSQueueURL),
				ReceiptHandle: message.ReceiptHandle,
			})

			if err != nil {
				fmt.Printf("Error deleting message %s: %v\n", *message.MessageId, err)
			} else {
				fmt.Printf("Successfully deleted message: %s\n", *message.MessageId)
			}
		}
	}
}

func (p *Shopify) handleWebhookTopic(topic string, payload json.RawMessage) (string, error) {
	var err error
	switch topic {
	case "orders/create":
		err = p.handleOrderCreate(payload)
	case "orders/updated":
		err = p.handleOrderUpdate(payload)
	case "orders/paid":
		err = p.handleOrderPaid(payload)
	case "orders/cancelled":
		err = p.handleOrderCancelled(payload)
	case "orders/fulfilled":
		err = p.handleOrderFulfilled(payload)
	default:
		return "ignored", nil
	}
	if err != nil {
		return "failed", err
	}
	return "success", nil
}

func isHandledShopifyOrderTopic(topic string) bool {
	switch topic {
	case "orders/create", "orders/updated", "orders/paid", "orders/cancelled", "orders/fulfilled":
		return true
	default:
		return false
	}
}

func convertFinancialStatus(status goshopify.OrderFinancialStatus) types.OrderFinancialStatus {
	return types.OrderFinancialStatus(cast.ToString(status))
}

func convertFulfillmentStatus(status goshopify.OrderFulfillmentStatus) types.OrderFulfillmentStatus {
	return types.OrderFulfillmentStatus(cast.ToString(status))
}

// 处理订单创建事件
func (p *Shopify) handleOrderCreate(event json.RawMessage) error {
	fmt.Printf("Starting to process order creation event - Payload size: %d bytes\n", len(event))

	order := shopify.Order{}
	if err := json.Unmarshal(event, &order); err != nil {
		fmt.Printf("Error unmarshaling order data: %v\n", err)
		fmt.Printf("Raw payload: %s\n", string(event))
		return fmt.Errorf("error unmarshaling order: %v", err)
	}

	fmt.Printf("Processing Shopify order: %s (ID: %d)\n", order.Name, order.Id)

	totalShipping := decimal.NewFromInt(0)
	hasTotalShipping := false
	for _, s := range order.ShippingLines {
		if s.Price != nil {
			hasTotalShipping = true
			totalShipping = totalShipping.Add(*s.Price)
		}
	}

	fmt.Printf("Order details - Total: %s, Currency: %s, Email: %s\n",
		order.TotalPrice.String(), order.Currency, order.Email)

	orderData := types.OrderData{
		ID:                fmt.Sprintf("%d", order.Id),
		Name:              order.Name,
		Email:             order.Email,
		Phone:             order.Phone,
		FinancialStatus:   convertFinancialStatus(order.FinancialStatus),
		FulfillmentStatus: convertFulfillmentStatus(order.FulfillmentStatus),
		CreatedAt:         order.CreatedAt,
		UpdatedAt:         order.UpdatedAt,
		TotalPrice:        order.TotalPrice,
		SubtotalPrice:     order.SubtotalPrice,
		TotalTax:          order.TotalTax,
		Currency:          order.Currency,

		// 原始数据存储完整的订单信息，以防需要访问更详细的信息
		RawData: map[string]interface{}{
			"source_name": "shopify",
			"order":       order,
		},
	}

	if order.Customer != nil {
		orderData.Customer = &types.OrderCustomer{
			ID:        fmt.Sprintf("%d", order.Customer.Id),
			Email:     order.Customer.Email,
			FirstName: order.Customer.FirstName,
			LastName:  order.Customer.LastName,
			Phone:     order.Customer.Phone,
		}
	}

	if hasTotalShipping {
		orderData.TotalShipping = &totalShipping
		fmt.Printf("Total shipping cost: %s\n", totalShipping.String())
	}

	// 处理收货地址
	if order.ShippingAddress != nil {
		fmt.Printf("Processing shipping address for order %s\n", order.Name)
		orderData.ShippingAddress = &types.OrderAddress{
			FirstName:    order.ShippingAddress.FirstName,
			LastName:     order.ShippingAddress.LastName,
			Address1:     order.ShippingAddress.Address1,
			Address2:     order.ShippingAddress.Address2,
			City:         order.ShippingAddress.City,
			Province:     order.ShippingAddress.Province,
			ProvinceCode: order.ShippingAddress.ProvinceCode,
			Country:      order.ShippingAddress.Country,
			CountryCode:  order.ShippingAddress.CountryCode,
			Zip:          order.ShippingAddress.Zip,
			Phone:        order.ShippingAddress.Phone,
			Company:      order.ShippingAddress.Company,
		}
	}

	// 处理账单地址
	if order.BillingAddress != nil {
		fmt.Printf("Processing billing address for order %s\n", order.Name)
		orderData.BillingAddress = &types.OrderAddress{
			FirstName:    order.BillingAddress.FirstName,
			LastName:     order.BillingAddress.LastName,
			Address1:     order.BillingAddress.Address1,
			Address2:     order.BillingAddress.Address2,
			City:         order.BillingAddress.City,
			Province:     order.BillingAddress.Province,
			ProvinceCode: order.BillingAddress.ProvinceCode,
			Country:      order.BillingAddress.Country,
			CountryCode:  order.BillingAddress.CountryCode,
			Zip:          order.BillingAddress.Zip,
			Phone:        order.BillingAddress.Phone,
			Company:      order.BillingAddress.Company,
		}
	}

	shopID := uint(0)
	fmt.Printf("Processing %d line items for order %s\n", len(order.LineItems), order.Name)

	for _, item := range order.LineItems {
		fmt.Printf("Processing line item: %s (Product ID: %d, Variant ID: %d)\n",
			item.Title, item.ProductId, item.VariantId)

		properties := make(map[string]string)
		for _, property := range item.Properties {
			properties[property.Name] = fmt.Sprintf("%v", property.Value)
		}

		product, ok, err := utils.GetShopProduct("shopify", cast.ToString(item.ProductId))
		if err != nil {
			fmt.Printf("Error getting shop product for product ID %d: %v\n", item.ProductId, err)
			return err
		}

		if !ok {
			return fmt.Errorf("shop product mapping not found for Shopify product %d", item.ProductId)
		}

		fmt.Printf("Found matching shop product: %s (Shop ID: %d)\n", product.Name, product.ShopID)

		var variantMapping models.ShopifyVariantMapping
		if err := database.Database().Where("shop_id = ? AND shop_product_id = ? AND shopify_variant_id = ?", product.ShopID, product.ID, item.VariantId).First(&variantMapping).Error; err != nil {
			return fmt.Errorf("variant mapping not found for Shopify variant %d: %w", item.VariantId, err)
		}
		variantId := variantMapping.LocalVariantID

		fmt.Printf("Mapped Shopify variant ID %d to internal variant ID %d\n", item.VariantId, variantId)

		if shopID == 0 {
			shopID = product.ShopID
			fmt.Printf("Set shop ID to %d from first line item\n", shopID)
		} else if shopID != product.ShopID {
			fmt.Printf("Error: Line item belongs to different shop (expected: %d, got: %d)\n", shopID, product.ShopID)
			return fmt.Errorf("line items belong to different shops, cannot process order")
		}

		lineItem := types.OrderLineItem{
			ID:           fmt.Sprintf("%d", item.Id),
			ProductID:    fmt.Sprintf("%d", item.ProductId),
			VariantID:    variantId,
			Title:        item.Title,
			SKU:          item.SKU,
			Quantity:     item.Quantity,
			Price:        item.Price,
			Properties:   properties,
			VariantTitle: item.VariantTitle,
			RawData:      map[string]interface{}{"line_item": item},
		}

		orderData.LineItems = append(orderData.LineItems, lineItem)
		fmt.Printf("Added line item: %s (Quantity: %d, Price: %s)\n",
			item.Title, item.Quantity, item.Price.String())
	}

	// 处理配送方式
	fmt.Printf("Processing %d shipping lines for order %s\n", len(order.ShippingLines), order.Name)
	for _, shipping := range order.ShippingLines {
		fmt.Printf("Processing shipping line: %s (Code: %s, Price: %s)\n",
			shipping.Title, shipping.Code, shipping.Price.String())

		shippingLine := types.OrderShippingLine{
			Code:      shipping.Code,
			Title:     shipping.Title,
			Price:     shipping.Price,
			Source:    shipping.Source,
			Carrier:   shipping.Source, // 使用Source作为carrier名称
			CarrierID: shipping.Code,   // 使用Code作为carrier ID
		}

		orderData.ShippingLines = append(orderData.ShippingLines, shippingLine)
	}

	// 触发订单接收事件
	fmt.Printf("Emitting order received event for shop ID %d\n", shopID)
	if err := events.EmitOrderReceived(&types.OrderReceivedEvent{
		Platform:  "shopify",
		OrderData: orderData,
		ShopID:    shopID,
		CreatedAt: time.Now(),
	}); err != nil {
		return err
	}

	fmt.Printf("Successfully processed Shopify order %s (Total line items: %d)\n",
		order.Name, len(orderData.LineItems))
	return nil
}

// 处理订单更新事件
func (p *Shopify) handleOrderUpdate(event json.RawMessage) error {
	fmt.Printf("Handling order update event - Payload size: %d bytes\n", len(event))

	order, err := p.parseOrderStatusChangedEvent(event)
	if err != nil {
		return err
	}

	fmt.Printf("Order update for: %s (ID: %s)\n", order.Name, order.OrderID)
	if order.FinancialStatus == types.OrderFinancialStatusPaid {
		return p.handleOrderCreate(event)
	}
	return events.EmitOrderUpdated(order)
}

// 处理订单支付事件
func (p *Shopify) handleOrderPaid(event json.RawMessage) error {
	fmt.Printf("Handling order paid event - Payload size: %d bytes\n", len(event))

	return p.handleOrderCreate(event)
}

// 处理订单取消事件
func (p *Shopify) handleOrderCancelled(event json.RawMessage) error {
	fmt.Printf("Handling order cancelled event - Payload size: %d bytes\n", len(event))

	order, err := p.parseOrderStatusChangedEvent(event)
	if err != nil {
		return err
	}

	fmt.Printf("Order cancelled for: %s (ID: %s)\n", order.Name, order.OrderID)
	return events.EmitOrderCancelled(order)
}

// 处理订单完成事件
func (p *Shopify) handleOrderFulfilled(event json.RawMessage) error {
	fmt.Printf("Handling order fulfilled event - Payload size: %d bytes\n", len(event))

	order, err := p.parseOrderStatusChangedEvent(event)
	if err != nil {
		return err
	}

	fmt.Printf("Order fulfilled for: %s (ID: %s)\n", order.Name, order.OrderID)
	return events.EmitOrderFulfilled(order)
}

func (p *Shopify) parseOrderStatusChangedEvent(event json.RawMessage) (*types.OrderStatusChangedEvent, error) {
	order := shopify.Order{}
	if err := json.Unmarshal(event, &order); err != nil {
		return nil, fmt.Errorf("error unmarshaling order status event: %v", err)
	}

	return &types.OrderStatusChangedEvent{
		Platform:          "shopify",
		OrderID:           fmt.Sprintf("%d", order.Id),
		Name:              order.Name,
		FinancialStatus:   convertFinancialStatus(order.FinancialStatus),
		FulfillmentStatus: convertFulfillmentStatus(order.FulfillmentStatus),
		RawData:           map[string]interface{}{"order": order},
		CreatedAt:         time.Now(),
	}, nil
}
