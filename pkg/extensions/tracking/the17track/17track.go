package the17track

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flaboy/aira-shop/pkg/config"
	"github.com/flaboy/aira-shop/pkg/extensions/tracking/utils"

	"github.com/flaboy/pin"
	"github.com/valyala/fasthttp"
)

type The17Track struct {
}

func (t *The17Track) Init() error {
	// Initialization logic for 17track
	return nil
}

func (t *The17Track) StartTracking(trackingNumber string) error {
	log.Println("Starting tracking for:", trackingNumber)

	// 检查 API key 是否配置
	apiKey := config.Config.The17TrackSecretKey
	if apiKey == "" {
		return fmt.Errorf("17track API key is not configured")
	}

	// 构建请求数据
	registerData := []RegisterRequest{
		{Number: trackingNumber},
	}

	requestBody, err := json.Marshal(registerData)
	if err != nil {
		return err
	}

	// 创建 fasthttp 请求
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	// 设置请求参数
	req.SetRequestURI("https://api.17track.net/track/v2.2/register")
	req.Header.SetMethod("POST")
	req.Header.Set("17token", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.SetBody(requestBody)

	// 发送请求
	if err := fasthttp.Do(req, resp); err != nil {
		return err
	}

	// 检查响应状态
	if resp.StatusCode() != 200 {
		return fmt.Errorf("failed to register tracking number %s, status code: %d", trackingNumber, resp.StatusCode())
	}

	// 解析响应
	var registerResp RegisterResponse
	if err := json.Unmarshal(resp.Body(), &registerResp); err != nil {
		return err
	}

	// 检查 API 响应代码
	if registerResp.Code != 0 {
		return err
	}

	// 检查是否有被拒绝的追踪号
	if len(registerResp.Data.Rejected) > 0 {
		rejected := registerResp.Data.Rejected[0]
		return fmt.Errorf("failed to register tracking number %s: %s (code: %d)", rejected.Number, rejected.Error.Message, rejected.Error.Code)
	}

	// 检查是否成功接受
	if len(registerResp.Data.Accepted) == 0 {
		return err
	}

	log.Printf("Successfully registered tracking number %s with 17Track", trackingNumber)
	log.Printf("Webhook URL: %s", utils.GetPublicUrl("17track", "webhook"))

	return nil
}

func (t *The17Track) GetTrackingUrl(trackingNumber string) string {
	// 返回17track的追踪URL
	return fmt.Sprintf("https://www.17track.net/en/track?nums=%s", trackingNumber)
}

func (t *The17Track) HandleRequest(c *pin.Context, path string) error {
	if path == "webhook" {

		event := TrackEvent{}
		if err := c.Bind(&event); err != nil {
			fmt.Println("Error binding request data:", err)
		}

		if event.Event == "TRACKING_UPDATED" {
			err := utils.UpdateStatus(event.Data.Number, t.convertStatus(event.Data.TrackInfo.LatestStatus.Status))
			if err != nil {
				log.Printf("Error updating status for %s: %v", event.Data.Number, err)
				c.JSON(500, map[string]string{
					"error": "Failed to update tracking status",
				})
				return nil
			}
		}
	}
	return c.Render("Request handled by 17trackProvider")
}

func (t *The17Track) GetProviderName() string {
	return "17track"
}

func (t *The17Track) Convert(localdata *TrackResponse) (*utils.TrackingResponse, error) {
	if localdata == nil {
		return nil, fmt.Errorf("local data is nil")
	}

	response := &utils.TrackingResponse{
		TrackingNumber: localdata.Number,
		Carrier:        "17track",
		CarrierCode:    fmt.Sprintf("%d", localdata.Carrier),
		RawData:        localdata,
	}

	// 转换状态信息
	response.Status = t.convertStatus(localdata.TrackInfo.LatestStatus.Status)
	response.StatusCode = localdata.TrackInfo.LatestStatus.SubStatus
	response.StatusMessage = localdata.TrackInfo.LatestStatus.SubStatusDesc

	// 转换地址信息
	response.Origin = t.convertAddress(&localdata.TrackInfo.ShippingInfo.ShipperAddress)
	response.Destination = t.convertAddress(&localdata.TrackInfo.ShippingInfo.RecipientAddress)

	// 转换时间信息
	if localdata.TrackInfo.LatestEvent.TimeISO != "" {
		if timestamp, err := time.Parse(time.RFC3339, localdata.TrackInfo.LatestEvent.TimeISO); err == nil {
			response.LastUpdated = &timestamp
		}
	}

	// 转换预计送达时间
	if localdata.TrackInfo.TimeMetrics.EstimatedDeliveryDate.From != "" {
		if timestamp, err := time.Parse("2006-01-02", localdata.TrackInfo.TimeMetrics.EstimatedDeliveryDate.From); err == nil {
			response.EstimatedDeliveryDate = &timestamp
		}
	}

	// 转换包裹信息
	if localdata.TrackInfo.MiscInfo.WeightKg != "" || localdata.TrackInfo.MiscInfo.Dimensions != "" {
		response.Package = &utils.PackageInfo{}

		if localdata.TrackInfo.MiscInfo.WeightKg != "" {
			if weight, err := strconv.ParseFloat(localdata.TrackInfo.MiscInfo.WeightKg, 64); err == nil {
				response.Package.Weight = &utils.Weight{
					Value: weight,
					Unit:  "kg",
				}
			}
		}

		if localdata.TrackInfo.MiscInfo.Pieces != "" {
			if pieces, err := strconv.Atoi(localdata.TrackInfo.MiscInfo.Pieces); err == nil {
				response.Package.Pieces = pieces
			}
		}

		response.Package.Reference = localdata.TrackInfo.MiscInfo.ReferenceNumber
	}

	// 转换服务类型
	response.ServiceType = localdata.TrackInfo.MiscInfo.ServiceType

	// 转换事件历史
	response.Events = t.convertEvents(localdata.TrackInfo.Tracking.Providers)

	return response, nil
}

func (t *The17Track) convertStatus(status string) utils.TrackingStatus {
	switch strings.ToLower(status) {
	case "delivered":
		return utils.StatusDelivered
	case "in_transit", "transit":
		return utils.StatusInTransit
	case "out_for_delivery":
		return utils.StatusOutForDelivery
	case "pre_transit":
		return utils.StatusPreTransit
	case "return_to_sender":
		return utils.StatusReturnToSender
	case "exception":
		return utils.StatusException
	case "cancelled":
		return utils.StatusCancelled
	default:
		return utils.StatusUnknown
	}
}

func (t *The17Track) convertAddress(addr *Address) *utils.Address {
	if addr == nil {
		return nil
	}

	result := &utils.Address{
		Country:    addr.Country,
		State:      addr.State,
		City:       addr.City,
		Street:     addr.Street,
		PostalCode: addr.PostalCode,
	}

	if addr.Coordinates.Longitude != "" && addr.Coordinates.Latitude != "" {
		if lng, err := strconv.ParseFloat(addr.Coordinates.Longitude, 64); err == nil {
			if lat, err := strconv.ParseFloat(addr.Coordinates.Latitude, 64); err == nil {
				result.Coordinates = &utils.Coordinates{
					Longitude: lng,
					Latitude:  lat,
				}
			}
		}
	}

	return result
}

func (t *The17Track) convertEvents(providers []Provider) []utils.TrackingEvent {
	var events []utils.TrackingEvent

	for _, provider := range providers {
		for _, event := range provider.Events {
			trackingEvent := utils.TrackingEvent{
				Status:      event.Stage,
				StatusCode:  event.SubStatus,
				Description: event.Description,
				Details:     event.DescriptionTranslate.Description,
			}

			// 解析时间
			if event.TimeISO != "" {
				if timestamp, err := time.Parse(time.RFC3339, event.TimeISO); err == nil {
					trackingEvent.Timestamp = timestamp
				}
			}

			// 转换地址
			trackingEvent.Location = t.convertAddress(&event.Address)

			events = append(events, trackingEvent)
		}
	}

	// 按时间排序（最新的在前）
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	return events
}
