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

type ShopifyCredential struct {
	Url                   string `json:"Url"`
	AccessToken           string `json:"AccessToken"`
	RefreshToken          string `json:"RefreshToken,omitempty"`
	Scope                 string `json:"Scope,omitempty"`
	AccessTokenExpiresAt  int64  `json:"AccessTokenExpiresAt,omitempty"`
	RefreshTokenExpiresAt int64  `json:"RefreshTokenExpiresAt,omitempty"`
	ShopLinkID            uint   `json:"ShopLinkID,omitempty"`
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
	credentialCipher, err := NewCredentialCipher(config.Config.Shopify.CredentialEncryptionKey, config.Config.Shopify.CredentialKeyVersion)
	if err != nil {
		return nil, err
	}
	credentialCiphertext, err := credentialCipher.Encrypt(credentialsJson)
	if err != nil {
		return nil, err
	}
	authorizedAt := time.Now().UTC()

	// 直接创建ShopLink模型
	shopLink := &models.ShopLink{
		Platform:             "shopify",
		Name:                 shopName,
		Url:                  "https://" + shopUrl,
		ExternalShopID:       externalShopID,
		InstallationStatus:   models.ShopInstallationStatusActive,
		ShopDomain:           shopUrl,
		LastAuthorizedAt:     &authorizedAt,
		CredentialCiphertext: credentialCiphertext,
		CredentialKeyVersion: credentialCipher.KeyVersion(),
	}

	db := database.Database()

	// 检查是否已存在
	var existing models.ShopLink
	err = db.Where("external_shop_id = ? AND platform = ?", externalShopID, "shopify").First(&existing).Error
	if err == nil {
		// Shopify 店铺名称和域名可变，授权复用只以 Shopify shop id 为准。
		existing.Name = shopName
		existing.Url = "https://" + shopUrl
		existing.ExternalShopID = externalShopID
		existing.Credentials = nil
		existing.InstallationStatus = models.ShopInstallationStatusActive
		existing.ShopDomain = shopUrl
		existing.LastAuthorizedAt = &authorizedAt
		existing.UninstalledAt = nil
		existing.CredentialCiphertext = credentialCiphertext
		existing.CredentialKeyVersion = credentialCipher.KeyVersion()
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

	client, err := newShopifyGraphQLClient(creds.Url, creds.AccessToken, p.httpClient)
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

	productInput, err := shopifyProductSetInput(product, newProduct, true)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to convert Shopify GraphQL product: %s", err.Error()))
	}
	productResp, metafields, _, err := client.setProduct(ctx, 0, productInput)
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
			if deleteErr := deleteAndVerifyShopifyProduct(recoveryCtx, client, matches[0]); deleteErr != nil {
				return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to remove uncertain Shopify product %d: %w", matches[0], deleteErr)}
			}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to create product: %s", err.Error()))
	}
	verifiedProduct, verifiedMetafields, _, err := client.getProduct(ctx, productResp.Id)
	if err != nil {
		if deleteErr := cleanupCreatedShopifyProduct(client, productResp.Id); deleteErr != nil {
			return nil, &types.PublishResultUncertainError{Cause: fmt.Errorf("failed to delete unverified Shopify product %d: %w", productResp.Id, deleteErr)}
		}
		return nil, usererrors.New(fmt.Sprintf("Failed to verify created Shopify product %d: %s", productResp.Id, err.Error()))
	}
	metafields = verifiedMetafields
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

	client, err := newShopifyGraphQLClient(creds.Url, creds.AccessToken, p.httpClient)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to create Shopify client: %s", err.Error()))
	}

	productID := cast.ToUint64(outerID)
	if productID == 0 {
		return nil, usererrors.New("Invalid Shopify product id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	db := database.Database()
	var productResp *shopify.Product
	var shopProduct models.ShopProduct
	var variantMappings []models.ShopifyVariantMapping
	if product.SyncMode != "" && product.SyncMode != types.ProductSyncModeKeep && product.SyncMode != types.ProductSyncModeSelected {
		return nil, fmt.Errorf("invalid Shopify product sync mode: %s", product.SyncMode)
	}
	productResp, identityMetafields, _, err := client.getProduct(ctx, productID)
	if err != nil {
		return nil, normalizeProductUpdateError(err)
	}
	if err := validateShopifyProductIdentity(identityMetafields, product); err != nil {
		return nil, err
	}
	if err := db.Where(
		"shop_id = ? AND platform = ? AND outer_id = ? AND publish_operation_id = ?",
		creds.ShopLinkID,
		"shopify",
		outerID,
		product.PublishOperationID,
	).First(&shopProduct).Error; err != nil {
		return nil, fmt.Errorf("query EffiPrint Shopify product mapping: %w", err)
	}
	if err := db.Where("shop_product_id = ?", shopProduct.ID).
		Order("local_variant_id ASC").
		Find(&variantMappings).Error; err != nil {
		return nil, fmt.Errorf("query EffiPrint Shopify variant mappings: %w", err)
	}
	if err := validateShopifyVariantMappings(productResp.Variants, product.Variants, variantMappings); err != nil {
		return nil, err
	}
	if product.SyncMode == types.ProductSyncModeKeep {
		return persistVerifiedShopifyProduct(db, &shopProduct, productResp, creds.Url, product)
	}

	shopifyProduct, err := p.toShopifyProduct(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to convert product: %s", err.Error()))
	}
	updateFields := productUpdateFieldSet(product.UpdateFields)
	if hasShopifyProductFields(updateFields) {
		var input map[string]any
		if len(updateFields) == 0 {
			input, err = shopifyProductSetInput(product, shopifyProduct, false)
			if err == nil {
				err = applyShopifyProductSetVariantIDs(input, product.Variants, variantMappings)
			}
		} else {
			input, err = shopifyProductSelectiveSetInput(product, shopifyProduct, *productResp, variantMappings)
		}
		if err != nil {
			return nil, err
		}
		productResp, _, _, err = client.setProduct(ctx, productID, input)
		if err != nil {
			return nil, normalizeProductUpdateError(err)
		}
	} else if productResp == nil {
		productResp, _, _, err = client.getProduct(ctx, productID)
		if err != nil {
			return nil, normalizeProductUpdateError(err)
		}
	}
	if _, selected := updateFields[types.ProductUpdateFieldBrandServices]; selected || len(updateFields) == 0 {
		metafield, metafieldErr := shopifyBrandServicesMetafield(product.BrandServices)
		if metafieldErr != nil {
			return nil, metafieldErr
		}
		if _, err := client.setProductMetafields(ctx, productID, []map[string]any{metafield}); err != nil {
			return nil, err
		}
	}

	productData, err := json.Marshal(product)
	if err != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to marshal product data: %s", err.Error()))
	}

	updateResult := db.Model(&models.ShopProduct{}).
		Where(
			"shop_id = ? AND platform = ? AND outer_id = ? AND publish_operation_id = ?",
			creds.ShopLinkID,
			"shopify",
			outerID,
			product.PublishOperationID,
		).
		Updates(map[string]interface{}{
			"name":       productResp.Title,
			"status":     "active",
			"url":        fmt.Sprintf("https://%s/admin/products/%d", creds.Url, productResp.Id),
			"data":       productData,
			"updated_at": time.Now(),
		})
	if updateResult.Error != nil {
		return nil, usererrors.New(fmt.Sprintf("Failed to update shop product: %s", updateResult.Error.Error()))
	}
	if updateResult.RowsAffected != 1 {
		return nil, usererrors.New("EffiPrint Shopify product mapping changed concurrently")
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

func validateShopifyProductIdentity(metafields []shopify.Metafield, product *types.ProductData) error {
	actual := map[string]string{}
	for _, metafield := range metafields {
		if metafield.Namespace == "effiprint" {
			actual[metafield.Key] = fmt.Sprint(metafield.Value)
		}
	}
	expected := map[string]string{
		"origin_product_id":    product.OriginProductID,
		"publish_operation_id": product.PublishOperationID,
		"external_shop_id":     product.ExternalShopID,
	}
	for _, key := range []string{"origin_product_id", "publish_operation_id", "external_shop_id"} {
		if expected[key] == "" || actual[key] != expected[key] {
			return fmt.Errorf("Shopify product is not owned by this EffiPrint publication: %s mismatch", key)
		}
	}
	return nil
}

func validateShopifyVariantMappings(remote []shopify.Variant, local []types.ProductVariant, mappings []models.ShopifyVariantMapping) error {
	localIDs := make(map[uint]struct{}, len(local))
	for _, variant := range local {
		localIDs[variant.ID] = struct{}{}
	}
	remoteIDs := make(map[uint64]struct{}, len(remote))
	for _, variant := range remote {
		remoteIDs[variant.Id] = struct{}{}
	}
	if len(mappings) != len(localIDs) {
		return fmt.Errorf("EffiPrint Shopify variant mapping is incomplete")
	}
	for _, mapping := range mappings {
		if _, exists := localIDs[mapping.LocalVariantID]; !exists {
			return fmt.Errorf("EffiPrint local variant mapping is invalid: %d", mapping.LocalVariantID)
		}
		if _, exists := remoteIDs[mapping.ShopifyVariantID]; !exists {
			return fmt.Errorf("EffiPrint Shopify variant no longer exists: %d", mapping.ShopifyVariantID)
		}
	}
	return nil
}

func persistVerifiedShopifyProduct(db *gorm.DB, shopProduct *models.ShopProduct, remote *shopify.Product, shopURL string, product *types.ProductData) (*types.PutProductResult, error) {
	productData, err := json.Marshal(product)
	if err != nil {
		return nil, fmt.Errorf("marshal verified EffiPrint product data: %w", err)
	}
	url := fmt.Sprintf("https://%s/admin/products/%d", shopURL, remote.Id)
	result := db.Model(&models.ShopProduct{}).
		Where("id = ? AND publish_operation_id = ?", shopProduct.ID, product.PublishOperationID).
		Updates(map[string]interface{}{
			"name":       remote.Title,
			"status":     "active",
			"url":        url,
			"data":       productData,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return nil, fmt.Errorf("persist verified EffiPrint Shopify product: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return nil, fmt.Errorf("verified EffiPrint Shopify product mapping changed concurrently")
	}
	return &types.PutProductResult{
		CommandResult: types.CommandResult{
			Success: true,
			Message: "EffiPrint product identity verified successfully",
		},
		OuterID:       fmt.Sprintf("%d", remote.Id),
		ShopProductID: shopProduct.ID,
		Url:           url,
	}, nil
}

func productUpdateFieldSet(fields []string) map[string]struct{} {
	result := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		result[field] = struct{}{}
	}
	return result
}

func selectShopifyProductUpdate(product shopify.Product, fields map[string]struct{}) shopify.Product {
	selected := shopify.Product{Id: product.Id}
	if _, exists := fields[types.ProductUpdateFieldTitle]; exists {
		selected.Title = product.Title
	}
	if _, exists := fields[types.ProductUpdateFieldDescription]; exists {
		selected.BodyHTML = product.BodyHTML
	}
	if _, exists := fields[types.ProductUpdateFieldImages]; exists {
		selected.Images = product.Images
	}
	if _, exists := fields[types.ProductUpdateFieldVariantsPrices]; exists {
		selected.Options = product.Options
		selected.Variants = product.Variants
	}
	return selected
}

func hasShopifyProductFields(fields map[string]struct{}) bool {
	for _, field := range []string{
		types.ProductUpdateFieldTitle,
		types.ProductUpdateFieldDescription,
		types.ProductUpdateFieldImages,
		types.ProductUpdateFieldVariantsPrices,
	} {
		if _, exists := fields[field]; exists {
			return true
		}
	}
	return len(fields) == 0
}

func applyShopifyVariantMappings(source []shopify.Variant, local []types.ProductVariant, mappings []models.ShopifyVariantMapping, productID uint64) ([]shopify.Variant, error) {
	if len(source) != len(local) {
		return nil, fmt.Errorf("local Shopify variant payload is incomplete")
	}
	remoteByLocalID := make(map[uint]uint64, len(mappings))
	for _, mapping := range mappings {
		remoteByLocalID[mapping.LocalVariantID] = mapping.ShopifyVariantID
	}
	for index := range source {
		remoteID, exists := remoteByLocalID[local[index].ID]
		if !exists || remoteID == 0 {
			return nil, fmt.Errorf("Shopify variant mapping is missing for local variant %d", local[index].ID)
		}
		source[index].Id = remoteID
		source[index].ProductId = productID
		source[index].Metafields = nil
	}
	return source, nil
}

// normalizeProductUpdateError 将 Shopify 404 转为稳定业务文案，供异步发布状态安全返回给商城端。
func normalizeProductUpdateError(err error) error {
	if stderrors.Is(err, errShopifyProductNotFound) {
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

	client, err := newShopifyGraphQLClient(creds.Url, creds.AccessToken, p.httpClient)
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

	bodyHTML := product.BodyHTML
	if product.SizeGuideEnabled {
		// Size table 与商品正文使用同一展示路径，避免依赖店铺主题配置独立 Tab。
		bodyHTML += product.SizeGuideHTML
	}

	shopifyProduct := shopify.Product{
		Title:          product.ProductName,
		BodyHTML:       bodyHTML,
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
						ShopDomain   string `json:"X-Shopify-Shop-Domain"`
					} `json:"metadata"`
				} `json:"detail"`
			}

			if err := json.Unmarshal([]byte(*message.Body), &eventBridgeMessage); err != nil {
				fmt.Printf("Error unmarshaling EventBridge message %s: %v\n", *message.MessageId, err)
				fmt.Printf("Message body: %s\n", *message.Body)
				if storedEvent, _, busy, storeErr := beginShopifyEvent(*message.MessageId, *message.MessageId, "invalid", "", "", "eventbridge", json.RawMessage(*message.Body)); storeErr != nil {
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
			if err := p.ProcessWebhook(ctx, eventID, *message.MessageId, topic, eventBridgeMessage.Detail.Metadata.ShopDomain, "eventbridge", payload); err != nil {
				fmt.Printf("Error handling Shopify event %s (%s): %v\n", eventID, topic, err)
				continue
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

func (p *Shopify) ProcessWebhook(ctx context.Context, eventID, messageID, topic, shopDomain, deliveryMethod string, payload json.RawMessage) error {
	externalShopID := ""
	var identityErr error
	if isHandledShopifyOrderTopic(topic) {
		externalShopID, identityErr = resolveShopifyEventExternalShopID(payload)
	}
	storedEvent, terminal, busy, err := beginShopifyEvent(eventID, messageID, topic, externalShopID, shopDomain, deliveryMethod, payload)
	if err != nil {
		return fmt.Errorf("start Shopify event %s: %w", eventID, err)
	}
	if terminal || busy {
		return nil
	}
	if identityErr != nil {
		if err := finishShopifyEvent(eventID, storedEvent.ProcessingToken, "failed", identityErr); err != nil {
			return fmt.Errorf("record Shopify event identity failure %s: %w", eventID, err)
		}
		return identityErr
	}
	status, processErr := p.handleWebhookTopic(ctx, eventID, topic, shopDomain, payload)
	if err := finishShopifyEvent(eventID, storedEvent.ProcessingToken, status, processErr); err != nil {
		return fmt.Errorf("finish Shopify event %s: %w", eventID, err)
	}
	return processErr
}

func (p *Shopify) handleWebhookTopic(ctx context.Context, eventID string, topic string, shopDomain string, payload json.RawMessage) (string, error) {
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
	case "app/uninstalled":
		err = p.handleAppUninstalled(ctx, eventID, shopDomain, payload)
	case "customers/data_request", "customers/redact", "shop/redact":
		err = p.handlePrivacyWebhook(ctx, eventID, topic, shopDomain, payload)
	default:
		return "ignored", nil
	}
	if err != nil {
		return "failed", err
	}
	return "success", nil
}

func (p *Shopify) handleAppUninstalled(ctx context.Context, eventID string, shopDomain string, payload json.RawMessage) error {
	data := struct {
		Domain string `json:"domain"`
	}{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("parse Shopify app uninstall event: %w", err)
	}
	if shopDomain == "" || data.Domain != shopDomain {
		return fmt.Errorf("Shopify app uninstall shop domain does not match delivery metadata")
	}
	return events.EmitShopifyAppUninstalled(&types.ShopifyAppUninstalledEvent{
		WebhookID: eventID, ShopDomain: shopDomain, OccurredAt: time.Now().UTC(),
	})
}

func (p *Shopify) handlePrivacyWebhook(ctx context.Context, eventID string, topic string, shopDomain string, payload json.RawMessage) error {
	data := struct {
		ShopDomain string `json:"shop_domain"`
		Customer   struct {
			ID uint64 `json:"id"`
		} `json:"customer"`
		OrdersRequested []uint64 `json:"orders_requested"`
		OrdersToRedact  []uint64 `json:"orders_to_redact"`
	}{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("parse Shopify privacy event: %w", err)
	}
	if shopDomain == "" || data.ShopDomain != shopDomain {
		return fmt.Errorf("Shopify privacy shop domain does not match delivery metadata")
	}
	orderIDs := data.OrdersRequested
	if topic == "customers/redact" {
		orderIDs = data.OrdersToRedact
	}
	formattedOrderIDs := make([]string, 0, len(orderIDs))
	for _, orderID := range orderIDs {
		formattedOrderIDs = append(formattedOrderIDs, strconv.FormatUint(orderID, 10))
	}
	customerID := ""
	if data.Customer.ID != 0 {
		customerID = strconv.FormatUint(data.Customer.ID, 10)
	}
	return events.EmitShopifyPrivacy(&types.ShopifyPrivacyEvent{
		WebhookID: eventID, Topic: topic, ShopDomain: shopDomain, CustomerID: customerID,
		OrderIDs: formattedOrderIDs, OccurredAt: time.Now().UTC(),
	})
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
