package types

import "encoding/json"

// PublicationTarget 使用平台稳定身份；Store 归属由调用方 service 校验。
type PublicationTarget struct {
	ExternalShopID string `json:"external_shop_id"`
	ListingID      string `json:"listing_id,omitempty"`
}

// PublicationRequest 只描述当前一步；调用方必须先持久化草稿 ID，再调用后续步骤。
// Description 是纯文本；Configuration 由具体平台按严格版本化结构解释。
type PublicationRequest struct {
	Target        PublicationTarget `json:"target"`
	Product       *ProductData      `json:"product"`
	Description   string            `json:"description"`
	Configuration json.RawMessage   `json:"configuration"`
}

type PublicationSnapshot struct {
	SchemaVersion   int                         `json:"schema_version"`
	ExternalShopID  string                      `json:"external_shop_id"`
	ListingID       string                      `json:"listing_id"`
	State           string                      `json:"state"`
	URL             string                      `json:"url"`
	Images          []PublicationImage          `json:"images,omitempty"`
	VariantMappings []PublicationVariantMapping `json:"variant_mappings,omitempty"`
}

type PublicationImage struct {
	ImageID string `json:"image_id"`
	Rank    int    `json:"rank"`
}

// PublicationImageRequest 不接收远端 URL；内容由调用方可信文件边界读取。
type PublicationImageRequest struct {
	Target   PublicationTarget `json:"target"`
	Filename string            `json:"filename"`
	Rank     int               `json:"rank"`
	AltText  string            `json:"alt_text"`
	Content  []byte            `json:"-"`
}

type PublicationVariantMapping struct {
	LocalVariantID uint            `json:"local_variant_id"`
	ProductID      string          `json:"product_id"`
	OfferingID     string          `json:"offering_id"`
	SKU            string          `json:"sku"`
	Properties     json.RawMessage `json:"properties"`
}

type PublicationConfigurationGroup string

const (
	PublicationConfigurationShop               PublicationConfigurationGroup = "shop"
	PublicationConfigurationTaxonomy           PublicationConfigurationGroup = "taxonomy"
	PublicationConfigurationProperties         PublicationConfigurationGroup = "properties"
	PublicationConfigurationShippingProfiles   PublicationConfigurationGroup = "shipping_profiles"
	PublicationConfigurationReadinessStates    PublicationConfigurationGroup = "readiness_states"
	PublicationConfigurationReturnPolicies     PublicationConfigurationGroup = "return_policies"
	PublicationConfigurationProductionPartners PublicationConfigurationGroup = "production_partners"
)

type PublicationConfigurationQuery struct {
	ExternalShopID string                        `json:"external_shop_id"`
	Group          PublicationConfigurationGroup `json:"group"`
	TaxonomyID     string                        `json:"taxonomy_id,omitempty"`
	Limit          int                           `json:"limit,omitempty"`
	Offset         int                           `json:"offset,omitempty"`
}

// Data 保留适配器的配置结构，不直接作为 Portal API 响应；service 负责生成可选择读模型。
type PublicationConfigurationResult struct {
	Data                json.RawMessage `json:"data"`
	Count               int             `json:"count"`
	PaginationSupported bool            `json:"pagination_supported"`
	HasMore             bool            `json:"has_more"`
}
