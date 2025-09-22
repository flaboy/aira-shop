package shopify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flaboy/aira/aira-core/pkg/database"
	"github.com/flaboy/aira/aira-shop/pkg/config"
	"github.com/flaboy/aira/aira-shop/pkg/errors"
	"github.com/flaboy/aira/aira-shop/pkg/events"
	"github.com/flaboy/aira/aira-shop/pkg/extensions/shoplink/utils"
	"github.com/flaboy/aira/aira-shop/pkg/models"
	"github.com/flaboy/aira/aira-shop/pkg/types"
	"github.com/flaboy/pin"
	"github.com/flaboy/pin/usererrors"
	"github.com/shopspring/decimal"
	"github.com/spf13/cast"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	goshopify "github.com/bold-commerce/go-shopify/v4"
	shopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira/aira-web/pkg/helper"
	"gorm.io/gorm"
)

var app *shopify.App

var dec100 = decimal.NewFromInt(100)

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
		return nil, errors.ErrWebhookSubscription
	}

	shopName := shopInfo.Name

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
		Platform:    "shopify",
		Name:        shopName,
		Url:         "https://" + shopUrl,
		Credentials: credentialsJson,
	}

	db := database.Database()

	// 检查是否已存在
	var existing models.ShopLink
	err = db.Where("name = ? AND platform = ?", shopName, "shopify").First(&existing).Error
	if err == nil {
		// 更新现有记录
		existing.Credentials = credentialsJson
		existing.Url = "https://" + shopUrl
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
		return nil, usererrors.New(fmt.Sprintf("Failed to create product: %s", err.Error()))
	}

	ShopifyRemoteData := &ShopifyRemoteData{
		VariantMapper: make(map[uint64]uint),
	}

	for _, variant := range productResp.Variants {
		optionUniqId := p.variantUniqId(&variant)
		if variantId, ok := variantMap[optionUniqId]; ok {
			ShopifyRemoteData.VariantMapper[variant.Id] = variantId
		}
	}

	// 获取店铺ID
	var shop models.ShopLink
	db := database.Database()
	err = db.Where("platform = ? AND url = ?", "shopify", "https://"+creds.Url).First(&shop).Error
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to find shop: %s", err.Error()))
	}

	// 保存产品信息
	shopProduct := models.ShopProduct{
		ShopID:   shop.ID,
		OuterID:  fmt.Sprintf("%d", productResp.Id),
		Status:   "active",
		Url:      fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
		Name:     productResp.Title,
		Platform: "shopify",
	}

	// 序列化产品数据
	productData, err := json.Marshal(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal product data: %s", err.Error()))
	}
	shopProduct.Data = productData

	// 序列化远程数据
	remoteData, err := json.Marshal(ShopifyRemoteData)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal remote data: %s", err.Error()))
	}
	shopProduct.RemoteData = remoteData

	if err := db.Create(&shopProduct).Error; err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to create shop product: %s", err.Error()))
	}

	// 触发产品发布事件
	productDataMap := map[string]interface{}{
		"product_name": product.ProductName,
		"body_html":    product.BodyHTML,
		"tags":         product.Tags,
	}

	events.EmitProductPublished(&types.ProductPublishedEvent{
		ShopProductID:   shopProduct.ID,
		ShopID:          shop.ID,
		Platform:        "shopify",
		OuterID:         fmt.Sprintf("%d", productResp.Id),
		ProductData:     productDataMap,
		BusinessContext: businessContext,
		CreatedAt:       time.Now(),
	})

	return &types.PutProductResult{
		CommandResult: types.CommandResult{
			Success: true,
			Message: "Product created successfully",
		},
		OuterID:    fmt.Sprintf("%d", productResp.Id),
		Url:        fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
		RemoteData: ShopifyRemoteData,
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

	for {
		// 接收消息
		output, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(config.Config.Shopify.SQSQueueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20, // 使用长轮询
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
				continue
			}

			topic := eventBridgeMessage.Detail.Metadata.ShopifyTopic
			payload := eventBridgeMessage.Detail.Payload

			fmt.Printf("Processing EventBridge webhook event - Topic: %s, Source: %s, EventID: %s\n",
				topic, eventBridgeMessage.Source, eventBridgeMessage.ID)
			fmt.Printf("EventBridge message details - Version: %s, Time: %s, Region: %s\n",
				eventBridgeMessage.Version, eventBridgeMessage.Time, eventBridgeMessage.Region)

			// 根据topic类型处理不同的webhook事件
			switch topic {
			case "orders/create":
				fmt.Println("Handling orders/create event")
				if err := p.handleOrderCreate(payload); err != nil {
					fmt.Printf("Error handling orders/create event: %v\n", err)
				} else {
					fmt.Println("Successfully handled orders/create event")
				}
			case "orders/updated":
				fmt.Println("Handling orders/updated event")
				if err := p.handleOrderUpdate(payload); err != nil {
					fmt.Printf("Error handling orders/updated event: %v\n", err)
				} else {
					fmt.Println("Successfully handled orders/updated event")
				}
			case "orders/paid":
				fmt.Println("Handling orders/paid event")
				if err := p.handleOrderPaid(payload); err != nil {
					fmt.Printf("Error handling orders/paid event: %v\n", err)
				} else {
					fmt.Println("Successfully handled orders/paid event")
				}
			case "orders/cancelled":
				fmt.Println("Handling orders/cancelled event")
				if err := p.handleOrderCancelled(payload); err != nil {
					fmt.Printf("Error handling orders/cancelled event: %v\n", err)
				} else {
					fmt.Println("Successfully handled orders/cancelled event")
				}
			case "orders/fulfilled":
				fmt.Println("Handling orders/fulfilled event")
				if err := p.handleOrderFulfilled(payload); err != nil {
					fmt.Printf("Error handling orders/fulfilled event: %v\n", err)
				} else {
					fmt.Println("Successfully handled orders/fulfilled event")
				}
			default:
				fmt.Printf("Unknown webhook topic: %s, skipping\n", topic)
			}

			// 删除消息
			fmt.Printf("Deleting processed message: %s\n", *message.MessageId)
			_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
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

		// 客户信息
		Customer: &types.OrderCustomer{
			ID:        fmt.Sprintf("%d", order.Customer.Id),
			Email:     order.Customer.Email,
			FirstName: order.Customer.FirstName,
			LastName:  order.Customer.LastName,
			Phone:     order.Customer.Phone,
		},

		// 原始数据存储完整的订单信息，以防需要访问更详细的信息
		RawData: map[string]interface{}{
			"source_name": "shopify",
			"order":       order,
		},
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
			fmt.Printf("Shop product not found for product ID %d, skipping line item %d\n", item.ProductId, item.Id)
			continue
		}

		fmt.Printf("Found matching shop product: %s (Shop ID: %d)\n", product.Name, product.ShopID)

		rm := ShopifyRemoteData{}
		if err = json.Unmarshal(product.RemoteData, &rm); err != nil {
			fmt.Printf("Error unmarshaling remote data for product %s: %v\n", product.Name, err)
			return err
		}

		variantId, ok := rm.VariantMapper[item.VariantId]
		if !ok {
			fmt.Printf("Variant ID %d not found in variant mapper, skipping line item\n", item.VariantId)
			continue
		}

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
	events.EmitOrderReceived(&types.OrderReceivedEvent{
		Platform:  "shopify",
		OrderData: orderData,
		ShopID:    shopID,
		CreatedAt: time.Now(),
	})

	fmt.Printf("Successfully processed Shopify order %s (Total line items: %d)\n",
		order.Name, len(orderData.LineItems))
	return nil
}

// 处理订单更新事件
func (p *Shopify) handleOrderUpdate(event json.RawMessage) error {
	fmt.Printf("Handling order update event - Payload size: %d bytes\n", len(event))

	// 解析订单数据以获取基本信息用于日志
	var orderBasic struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(event, &orderBasic); err == nil {
		fmt.Printf("Order update for: %s (ID: %d)\n", orderBasic.Name, orderBasic.ID)
	}

	// TODO: 实现具体的订单更新逻辑
	return nil
}

// 处理订单支付事件
func (p *Shopify) handleOrderPaid(event json.RawMessage) error {
	fmt.Printf("Handling order paid event - Payload size: %d bytes\n", len(event))

	// 解析订单数据以获取基本信息用于日志
	var orderBasic struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(event, &orderBasic); err == nil {
		fmt.Printf("Order paid for: %s (ID: %d)\n", orderBasic.Name, orderBasic.ID)
	}

	// TODO: 实现具体的订单支付逻辑
	return nil
}

// 处理订单取消事件
func (p *Shopify) handleOrderCancelled(event json.RawMessage) error {
	fmt.Printf("Handling order cancelled event - Payload size: %d bytes\n", len(event))

	// 解析订单数据以获取基本信息用于日志
	var orderBasic struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(event, &orderBasic); err == nil {
		fmt.Printf("Order cancelled for: %s (ID: %d)\n", orderBasic.Name, orderBasic.ID)
	}

	// TODO: 实现具体的订单取消逻辑
	return nil
}

// 处理订单完成事件
func (p *Shopify) handleOrderFulfilled(event json.RawMessage) error {
	fmt.Printf("Handling order fulfilled event - Payload size: %d bytes\n", len(event))

	// 解析订单数据以获取基本信息用于日志
	var orderBasic struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(event, &orderBasic); err == nil {
		fmt.Printf("Order fulfilled for: %s (ID: %d)\n", orderBasic.Name, orderBasic.ID)
	}

	// TODO: 实现具体的订单完成逻辑
	return nil
}
