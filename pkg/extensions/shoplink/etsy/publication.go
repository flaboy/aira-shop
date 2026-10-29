package etsy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flaboy/aira-shop/pkg/types"
)

const (
	publicationTimeout       = 30 * time.Second
	maxPublicationImageBytes = 10 << 20
)

var ErrPublicationScopeMissing = errors.New("Etsy publication requires additional authorization")

// PublicationHTTPError 保留状态供权限分类，不保存可能含令牌或客户数据的原始响应。
type PublicationHTTPError struct {
	Operation  string
	StatusCode int
}

func (e *PublicationHTTPError) Error() string {
	return fmt.Sprintf("Etsy %s failed with HTTP %d", e.Operation, e.StatusCode)
}

func publicationCredential(credential *types.ShopCredential, shopID string, scopes ...string) (string, error) {
	if credential == nil || credential.Platform != PlatformName || !validPublicationID(shopID) {
		return "", fmt.Errorf("Etsy publication credential or shop identity is invalid")
	}
	boundShop, _ := credential.Data["ExternalShopID"].(string)
	token, _ := credential.Data["AccessToken"].(string)
	tokenType, _ := credential.Data["TokenType"].(string)
	if boundShop != shopID || strings.TrimSpace(token) == "" || !strings.EqualFold(tokenType, "Bearer") {
		return "", fmt.Errorf("Etsy publication credential does not match the shop")
	}
	granted, _ := credential.Data["GrantedScopes"].(string)
	available := make(map[string]bool)
	for _, scope := range strings.Fields(granted) {
		available[scope] = true
	}
	for _, scope := range scopes {
		if !available[scope] {
			return "", fmt.Errorf("%w: %s", ErrPublicationScopeMissing, scope)
		}
	}
	return token, nil
}

func validPublicationID(value string) bool {
	id, err := strconv.ParseUint(value, 10, 63)
	return err == nil && id > 0 && strconv.FormatUint(id, 10) == value
}

func uncertainPublication(err error) error {
	return &types.PublishResultUncertainError{Cause: err}
}

// publicationJSON 一次请求不重试。取消不能证明远端未写入，写入后的协议错误均保留为未知。
func (p *Etsy) publicationJSON(ctx context.Context, method, path, token, contentType string, body io.Reader, target any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	requestCtx, cancel := context.WithTimeout(ctx, publicationTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, method, strings.TrimRight(p.config.APIBaseURL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("build Etsy publication request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("x-api-key", p.config.APIKey+":"+p.config.SharedSecret)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	// 禁止凭据随重定向转发，也不允许 307/308 重新发送有副作用的 body。
	client := *p.config.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	write := method != http.MethodGet
	if err != nil {
		// url.Error 可包含被注入的传输错误详情；只保留安全错误分类和取消原因。
		cause := requestCtx.Err()
		if cause == nil {
			cause = errors.New("Etsy publication transport failed")
		}
		failure := fmt.Errorf("Etsy publication request failed: %w", cause)
		if write {
			return uncertainPublication(failure)
		}
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := &PublicationHTTPError{Operation: "publication request", StatusCode: response.StatusCode}
		if write && (response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || (response.StatusCode >= 300 && response.StatusCode < 400)) {
			return uncertainPublication(failure)
		}
		return failure
	}
	if err := decodeJSON(response.Body, target); err != nil {
		// 不回显 JSON 错误内容，避免把原始远端 body 带入任务日志。
		failure := errors.New("Etsy publication response is incomplete or invalid")
		if write {
			return uncertainPublication(failure)
		}
		return failure
	}
	return nil
}

type publicationListing struct {
	ListingID json.Number `json:"listing_id"`
	ShopID    json.Number `json:"shop_id"`
	State     string      `json:"state"`
	URL       string      `json:"url"`
	Images    []struct {
		ImageID json.Number `json:"listing_image_id"`
		Rank    int         `json:"rank"`
	} `json:"images"`
}

func listingSnapshot(listing publicationListing, target types.PublicationTarget) (*types.PublicationSnapshot, error) {
	listingID := listing.ListingID.String()
	if !validPublicationID(listingID) || !validPublicationID(listing.ShopID.String()) || listing.ShopID.String() != target.ExternalShopID || (target.ListingID != "" && target.ListingID != listingID) {
		return nil, errors.New("Etsy listing identity does not match the publication target")
	}
	switch listing.State {
	case "draft", "active", "inactive", "sold_out", "expired", "removed":
	default:
		return nil, errors.New("Etsy listing state is missing or unsupported")
	}
	if listing.URL != "" {
		parsed, err := url.Parse(listing.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || (parsed.Hostname() != "etsy.com" && !strings.HasSuffix(parsed.Hostname(), ".etsy.com")) {
			return nil, errors.New("Etsy listing URL is invalid")
		}
	}
	result := &types.PublicationSnapshot{SchemaVersion: 1, ExternalShopID: target.ExternalShopID, ListingID: listingID, State: listing.State, URL: listing.URL}
	for _, image := range listing.Images {
		if !validPublicationID(image.ImageID.String()) || image.Rank <= 0 {
			return nil, errors.New("Etsy listing image response is incomplete")
		}
		result.Images = append(result.Images, types.PublicationImage{ImageID: image.ImageID.String(), Rank: image.Rank})
	}
	return result, nil
}

func (p *Etsy) InspectListing(ctx context.Context, credential *types.ShopCredential, target types.PublicationTarget) (*types.PublicationSnapshot, error) {
	token, err := publicationCredential(credential, target.ExternalShopID, "listings_r")
	if err != nil {
		return nil, err
	}
	if !validPublicationID(target.ListingID) {
		return nil, errors.New("Etsy inspection requires a known listing ID")
	}
	listing := publicationListing{}
	if err := p.publicationJSON(ctx, http.MethodGet, "/v3/application/listings/"+target.ListingID+"?includes=Images", token, "", nil, &listing); err != nil {
		return nil, err
	}
	return listingSnapshot(listing, target)
}

func (p *Etsy) CreateDraft(ctx context.Context, credential *types.ShopCredential, request types.PublicationRequest) (*types.PublicationSnapshot, error) {
	token, err := publicationCredential(credential, request.Target.ExternalShopID, "listings_w")
	if err != nil {
		return nil, err
	}
	if request.Target.ListingID != "" {
		return nil, errors.New("Etsy draft creation must not replace a known listing")
	}
	configuration, variants, err := validatePublicationRequest(request)
	if err != nil {
		return nil, err
	}
	form := listingForm(request, configuration)
	quantity := 0
	price := variants[0].Price
	for _, variant := range variants {
		quantity += variant.Quantity
		if variant.Price.LessThan(price) {
			price = variant.Price
		}
	}
	if quantity <= 0 {
		return nil, errors.New("Etsy draft requires positive available quantity")
	}
	form.Set("quantity", strconv.Itoa(quantity))
	form.Set("price", price.String())
	form.Set("readiness_state_id", configuration.ReadinessStateID)
	form.Set("type", "physical")
	listing := publicationListing{}
	if err := p.publicationJSON(ctx, http.MethodPost, "/v3/application/shops/"+request.Target.ExternalShopID+"/listings", token, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &listing); err != nil {
		return nil, err
	}
	result, err := listingSnapshot(listing, request.Target)
	if err != nil {
		return nil, uncertainPublication(err)
	}
	if result.State != "draft" {
		return result, uncertainPublication(errors.New("Etsy draft creation returned an unexpected state"))
	}
	return result, nil
}

func (p *Etsy) UpdateListing(ctx context.Context, credential *types.ShopCredential, request types.PublicationRequest) (*types.PublicationSnapshot, error) {
	token, err := publicationCredential(credential, request.Target.ExternalShopID, "listings_r", "listings_w")
	if err != nil {
		return nil, err
	}
	configuration, _, err := validatePublicationRequest(request)
	if err != nil {
		return nil, err
	}
	if _, err := p.InspectListing(ctx, credential, request.Target); err != nil {
		return nil, err
	}
	form := listingForm(request, configuration)
	listing := publicationListing{}
	if err := p.publicationJSON(ctx, http.MethodPatch, listingPath(request.Target), token, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), &listing); err != nil {
		return nil, err
	}
	result, err := listingSnapshot(listing, request.Target)
	if err != nil {
		return nil, uncertainPublication(err)
	}
	return result, nil
}

func (p *Etsy) ActivateListing(ctx context.Context, credential *types.ShopCredential, target types.PublicationTarget) (*types.PublicationSnapshot, error) {
	token, err := publicationCredential(credential, target.ExternalShopID, "listings_r", "listings_w")
	if err != nil {
		return nil, err
	}
	current, err := p.InspectListing(ctx, credential, target)
	if err != nil {
		return nil, err
	}
	if current.State != "draft" || len(current.Images) == 0 {
		return nil, errors.New("Etsy activation requires a draft with a listing image")
	}
	listing := publicationListing{}
	if err := p.publicationJSON(ctx, http.MethodPatch, listingPath(target), token, "application/x-www-form-urlencoded", strings.NewReader("state=active"), &listing); err != nil {
		return nil, err
	}
	result, err := listingSnapshot(listing, target)
	if err != nil {
		return nil, uncertainPublication(err)
	}
	if result.State != "active" {
		return result, uncertainPublication(errors.New("Etsy activation returned an unexpected state"))
	}
	return result, nil
}

func listingPath(target types.PublicationTarget) string {
	return "/v3/application/shops/" + target.ExternalShopID + "/listings/" + target.ListingID
}

func (p *Etsy) UploadListingImage(ctx context.Context, credential *types.ShopCredential, request types.PublicationImageRequest) (*types.PublicationImage, error) {
	token, err := publicationCredential(credential, request.Target.ExternalShopID, "listings_r", "listings_w")
	if err != nil {
		return nil, err
	}
	contentType := http.DetectContentType(request.Content)
	if len(request.Content) == 0 || len(request.Content) > maxPublicationImageBytes || (contentType != "image/png" && contentType != "image/jpeg") || request.Rank <= 0 || request.Rank > 20 || utf8.RuneCountInString(request.AltText) > 500 {
		return nil, errors.New("Etsy image must be JPEG/PNG up to 10 MiB, with rank 1-20 and alt text up to 500 characters")
	}
	if request.Filename == "" || filepath.Base(request.Filename) != request.Filename || strings.ContainsAny(request.Filename, "\r\n\x00/\\") {
		return nil, errors.New("Etsy image filename is invalid")
	}
	if _, err := p.InspectListing(ctx, credential, request.Target); err != nil {
		return nil, err
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", request.Filename)
	if err != nil {
		return nil, fmt.Errorf("build Etsy image form: %w", err)
	}
	if _, err := part.Write(request.Content); err != nil {
		return nil, fmt.Errorf("write Etsy image form: %w", err)
	}
	for key, value := range map[string]string{"rank": strconv.Itoa(request.Rank), "alt_text": request.AltText} {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write Etsy image field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close Etsy image form: %w", err)
	}
	image := struct {
		ListingID json.Number `json:"listing_id"`
		ImageID   json.Number `json:"listing_image_id"`
		Rank      int         `json:"rank"`
	}{}
	if err := p.publicationJSON(ctx, http.MethodPost, listingPath(request.Target)+"/images", token, writer.FormDataContentType(), body, &image); err != nil {
		return nil, err
	}
	if image.ListingID.String() != request.Target.ListingID || !validPublicationID(image.ImageID.String()) || image.Rank != request.Rank {
		return nil, uncertainPublication(errors.New("Etsy image response does not match the upload"))
	}
	return &types.PublicationImage{ImageID: image.ImageID.String(), Rank: image.Rank}, nil
}
