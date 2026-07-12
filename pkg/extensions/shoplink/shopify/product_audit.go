package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	goshopify "github.com/bold-commerce/go-shopify/v4"
	"github.com/flaboy/aira-core/pkg/database"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"gorm.io/gorm"
)

func findProductsByPublishOperation(ctx context.Context, client *goshopify.Client, operationID string) ([]goshopify.Product, error) {
	products, err := client.Product.ListAll(ctx, nil)
	if err != nil {
		return nil, err
	}
	matches := make([]goshopify.Product, 0)
	for _, product := range products {
		metafields, err := client.Product.ListMetafields(ctx, product.Id, nil)
		if err != nil {
			return nil, err
		}
		for _, metafield := range metafields {
			if metafield.Namespace == "effiprint" && metafield.Key == "publish_operation_id" && fmt.Sprint(metafield.Value) == operationID {
				matches = append(matches, product)
				break
			}
		}
	}
	return matches, nil
}

func deleteAndVerifyShopifyProduct(ctx context.Context, client *goshopify.Client, productID uint64) error {
	if err := client.Product.Delete(ctx, productID); err != nil {
		var responseErr goshopify.ResponseError
		if errors.As(err, &responseErr) && responseErr.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	if _, err := client.Product.Get(ctx, productID, nil); err != nil {
		var responseErr goshopify.ResponseError
		if errors.As(err, &responseErr) && responseErr.Status == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("verify Shopify product %d deletion: %w", productID, err)
	}
	return fmt.Errorf("Shopify product %d still exists after deletion", productID)
}

func cleanupCreatedShopifyProduct(client *goshopify.Client, productID uint64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return deleteAndVerifyShopifyProduct(ctx, client, productID)
}

func (p *Shopify) AuditProducts(credential *types.ShopCredential, externalShopID string) ([]types.ProductAuditIssue, error) {
	var creds ShopifyCredential
	credData, err := json.Marshal(credential.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal Shopify credentials: %w", err)
	}
	if err := json.Unmarshal(credData, &creds); err != nil {
		return nil, fmt.Errorf("unmarshal Shopify credentials: %w", err)
	}
	client, err := goshopify.NewClient(*app, creds.Url, creds.AccessToken, goshopify.WithHTTPClient(p.httpClient))
	if err != nil {
		return nil, fmt.Errorf("create Shopify audit client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	remoteProducts, err := client.Product.ListAll(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list Shopify products for audit: %w", err)
	}
	remoteIDs := make(map[string]bool, len(remoteProducts))
	originProducts := map[string][]string{}
	issues := make([]types.ProductAuditIssue, 0)
	for _, remote := range remoteProducts {
		outerID := strconv.FormatUint(remote.Id, 10)
		remoteIDs[outerID] = true
		metafields, err := client.Product.ListMetafields(ctx, remote.Id, nil)
		if err != nil {
			return nil, fmt.Errorf("list Shopify product %s metafields: %w", outerID, err)
		}
		identity := map[string]string{}
		for _, metafield := range metafields {
			if metafield.Namespace == "effiprint" {
				identity[metafield.Key] = fmt.Sprint(metafield.Value)
			}
		}
		if identity["origin_product_id"] == "" && identity["publish_operation_id"] == "" {
			continue
		}
		if identity["external_shop_id"] != externalShopID {
			issues = append(issues, types.ProductAuditIssue{Type: "wrong_external_shop", ShopifyProductID: outerID, OriginProductID: identity["origin_product_id"], OperationID: identity["publish_operation_id"], Message: "Shopify product identity does not match the selected store."})
			continue
		}
		originProducts[identity["origin_product_id"]] = append(originProducts[identity["origin_product_id"]], outerID)
		var local models.ShopProduct
		err = database.Database().Where("platform = ? AND outer_id = ?", "shopify", outerID).First(&local).Error
		if err == gorm.ErrRecordNotFound {
			issues = append(issues, types.ProductAuditIssue{Type: "orphan_remote", ShopifyProductID: outerID, OriginProductID: identity["origin_product_id"], OperationID: identity["publish_operation_id"], Message: "Shopify product has EffiPrint identity but no local mapping."})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("query local Shopify product %s: %w", outerID, err)
		}
		if local.ShopID != creds.ShopLinkID {
			issues = append(issues, types.ProductAuditIssue{Type: "wrong_shoplink", ShopifyProductID: outerID, ShopProductID: local.ID, OriginProductID: identity["origin_product_id"], OperationID: identity["publish_operation_id"], Message: "Local product belongs to a different Shopify authorization record."})
		}
		var remoteData ShopifyRemoteData
		mappingComplete := json.Unmarshal(local.RemoteData, &remoteData) == nil && len(remoteData.VariantMapper) == len(remote.Variants)
		var normalizedMappings []models.ShopifyVariantMapping
		if err := database.Database().Where("shop_product_id = ?", local.ID).Find(&normalizedMappings).Error; err != nil {
			return nil, fmt.Errorf("query normalized Shopify variant mappings for product %s: %w", outerID, err)
		}
		mappingComplete = mappingComplete && len(normalizedMappings) == len(remote.Variants)
		normalizedByRemoteID := make(map[uint64]uint, len(normalizedMappings))
		for _, mapping := range normalizedMappings {
			normalizedByRemoteID[mapping.ShopifyVariantID] = mapping.LocalVariantID
		}
		if mappingComplete {
			for _, variant := range remote.Variants {
				if remoteData.VariantMapper[variant.Id] == 0 || normalizedByRemoteID[variant.Id] != remoteData.VariantMapper[variant.Id] {
					mappingComplete = false
					break
				}
			}
		}
		if !mappingComplete {
			issues = append(issues, types.ProductAuditIssue{Type: "variant_mapping_incomplete", ShopifyProductID: outerID, ShopProductID: local.ID, OriginProductID: identity["origin_product_id"], OperationID: identity["publish_operation_id"], Message: "Shopify variant identities do not match the local variant mapping."})
		}
	}
	for originProductID, productIDs := range originProducts {
		if originProductID != "" && len(productIDs) > 1 {
			for _, productID := range productIDs {
				issues = append(issues, types.ProductAuditIssue{Type: "duplicate_origin", ShopifyProductID: productID, OriginProductID: originProductID, Message: "Multiple Shopify products share the same EffiPrint origin product identity."})
			}
		}
	}
	var localProducts []models.ShopProduct
	if err := database.Database().Where("shop_id = ? AND platform = ?", creds.ShopLinkID, "shopify").Find(&localProducts).Error; err != nil {
		return nil, fmt.Errorf("list local Shopify products for audit: %w", err)
	}
	for _, local := range localProducts {
		if !remoteIDs[local.OuterID] {
			issues = append(issues, types.ProductAuditIssue{Type: "remote_missing", ShopifyProductID: local.OuterID, ShopProductID: local.ID, Message: "Local mapping points to a Shopify product that no longer exists."})
		}
	}
	return issues, nil
}

func (p *Shopify) CleanupPublishOperation(credential *types.ShopCredential, operationID string) error {
	var creds ShopifyCredential
	credData, err := json.Marshal(credential.Data)
	if err != nil {
		return fmt.Errorf("marshal Shopify credentials: %w", err)
	}
	if err := json.Unmarshal(credData, &creds); err != nil {
		return fmt.Errorf("unmarshal Shopify credentials: %w", err)
	}
	client, err := goshopify.NewClient(*app, creds.Url, creds.AccessToken, goshopify.WithHTTPClient(p.httpClient))
	if err != nil {
		return fmt.Errorf("create Shopify cleanup client: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	matches, err := findProductsByPublishOperation(ctx, client, operationID)
	if err != nil {
		return fmt.Errorf("find Shopify publish operation %s: %w", operationID, err)
	}
	for _, product := range matches {
		if err := deleteAndVerifyShopifyProduct(ctx, client, product.Id); err != nil {
			return fmt.Errorf("clean Shopify publish operation %s: %w", operationID, err)
		}
	}
	return nil
}
