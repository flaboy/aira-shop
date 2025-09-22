package usps

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/flaboy/aira-shop/pkg/extensions/tracking/utils"

	"github.com/flaboy/pin"
)

type USPS struct {
}

func (u *USPS) Init() error {
	// Initialization logic for USPS
	return nil
}

func (u *USPS) StartTracking(trackingNumber string) error {
	log.Println("Starting USPS tracking for:", trackingNumber)
	log.Println(utils.GetPublicUrl("usps", "/webhook"))
	return nil
}

func (u *USPS) HandleRequest(c *pin.Context) error {
	// Logic to handle requests for USPS
	return c.Render("Request handled by USPS")
}

func (u *USPS) GetProviderName() string {
	return "usps"
}

func (u *USPS) Convert(localdata *TrackingResponse) (*utils.TrackingResponse, error) {
	if localdata == nil || len(localdata.TrackInfo) == 0 {
		return nil, fmt.Errorf("local data is nil or empty")
	}

	// 取第一个TrackInfo
	trackInfo := localdata.TrackInfo[0]

	// 检查是否有错误
	if trackInfo.Error != nil {
		return nil, fmt.Errorf("USPS tracking error: %s", trackInfo.Error.Description)
	}

	response := &utils.TrackingResponse{
		TrackingNumber: trackInfo.ID,
		Carrier:        "usps",
		CarrierCode:    "usps",
		RawData:        localdata,
	}

	// 转换状态信息
	response.Status = u.convertStatus(trackInfo.Status)
	response.StatusCode = trackInfo.StatusCategory
	response.StatusMessage = trackInfo.StatusSummary

	// 转换地址信息
	response.Origin = u.convertOriginAddress(&trackInfo)
	response.Destination = u.convertDestinationAddress(&trackInfo)

	// 转换服务类型
	if len(trackInfo.Service) > 0 {
		response.ServiceType = trackInfo.Service[0]
	}

	// 转换时间信息
	if trackInfo.ExpectedDeliveryDate != "" {
		if timestamp, err := time.Parse("January 2, 2006", trackInfo.ExpectedDeliveryDate); err == nil {
			response.EstimatedDeliveryDate = &timestamp
		} else if timestamp, err := time.Parse("2006-01-02", trackInfo.ExpectedDeliveryDate); err == nil {
			response.EstimatedDeliveryDate = &timestamp
		}
	}

	if trackInfo.GuaranteedDeliveryDate != "" {
		if timestamp, err := time.Parse("January 2, 2006", trackInfo.GuaranteedDeliveryDate); err == nil {
			response.EstimatedDeliveryDate = &timestamp
		}
	}

	// 设置包裹信息（USPS的信息相对有限）
	response.Package = &utils.PackageInfo{
		Pieces: 1, // USPS通常是单件
	}

	// 转换最后更新时间
	if trackInfo.TrackSummary.EventDate != "" && trackInfo.TrackSummary.EventTime != "" {
		dateTimeStr := trackInfo.TrackSummary.EventDate + " " + trackInfo.TrackSummary.EventTime
		if timestamp, err := u.parseUSPSDateTime(dateTimeStr); err == nil {
			response.LastUpdated = &timestamp
		}
	}

	// 检查是否已送达
	if strings.ToLower(trackInfo.Status) == "delivered" && trackInfo.TrackSummary.EventDate != "" {
		if timestamp, err := u.parseUSPSDateTime(trackInfo.TrackSummary.EventDate + " " + trackInfo.TrackSummary.EventTime); err == nil {
			response.ActualDeliveryDate = &timestamp
		}
	}

	// 转换事件历史
	response.Events = u.convertEvents(&trackInfo)

	return response, nil
}

func (u *USPS) convertStatus(status string) utils.TrackingStatus {
	switch strings.ToLower(status) {
	case "delivered":
		return utils.StatusDelivered
	case "in transit", "in_transit":
		return utils.StatusInTransit
	case "out for delivery":
		return utils.StatusOutForDelivery
	case "pre-shipment", "acceptance", "usps in possession of item":
		return utils.StatusPreTransit
	case "return to sender":
		return utils.StatusReturnToSender
	case "exception", "notice left", "attempted delivery":
		return utils.StatusException
	default:
		return utils.StatusUnknown
	}
}

func (u *USPS) convertOriginAddress(trackInfo *TrackInfo) *utils.Address {
	if trackInfo.OriginCity == "" && trackInfo.OriginState == "" && trackInfo.OriginZip == "" {
		return nil
	}

	return &utils.Address{
		City:       trackInfo.OriginCity,
		State:      trackInfo.OriginState,
		PostalCode: trackInfo.OriginZip,
		Country:    "US", // USPS默认为美国
	}
}

func (u *USPS) convertDestinationAddress(trackInfo *TrackInfo) *utils.Address {
	if trackInfo.DestinationCity == "" && trackInfo.DestinationState == "" && trackInfo.DestinationZip == "" {
		return nil
	}

	return &utils.Address{
		City:       trackInfo.DestinationCity,
		State:      trackInfo.DestinationState,
		PostalCode: trackInfo.DestinationZip,
		Country:    "US", // USPS默认为美国
	}
}

func (u *USPS) convertEvents(trackInfo *TrackInfo) []utils.TrackingEvent {
	var events []utils.TrackingEvent

	// 添加TrackSummary作为最新事件
	if trackInfo.TrackSummary.Event != "" {
		event := utils.TrackingEvent{
			Status:      trackInfo.Status,
			Description: trackInfo.TrackSummary.Event,
		}

		// 解析时间
		if trackInfo.TrackSummary.EventDate != "" {
			dateTimeStr := trackInfo.TrackSummary.EventDate
			if trackInfo.TrackSummary.EventTime != "" {
				dateTimeStr += " " + trackInfo.TrackSummary.EventTime
			}
			if timestamp, err := u.parseUSPSDateTime(dateTimeStr); err == nil {
				event.Timestamp = timestamp
			}
		}

		// 解析位置
		if trackInfo.TrackSummary.EventCity != "" || trackInfo.TrackSummary.EventState != "" {
			event.Location = &utils.Address{
				City:       trackInfo.TrackSummary.EventCity,
				State:      trackInfo.TrackSummary.EventState,
				PostalCode: trackInfo.TrackSummary.EventZIPCode,
				Country:    trackInfo.TrackSummary.EventCountry,
			}
		}

		events = append(events, event)
	}

	// 添加所有详细事件
	for _, detail := range trackInfo.TrackDetail {
		event := utils.TrackingEvent{
			Status:      detail.Event,
			Description: detail.Event,
		}

		// 解析时间
		if detail.EventDate != "" {
			dateTimeStr := detail.EventDate
			if detail.EventTime != "" {
				dateTimeStr += " " + detail.EventTime
			}
			if timestamp, err := u.parseUSPSDateTime(dateTimeStr); err == nil {
				event.Timestamp = timestamp
			}
		}

		// 解析位置
		if detail.EventCity != "" || detail.EventState != "" {
			event.Location = &utils.Address{
				City:       detail.EventCity,
				State:      detail.EventState,
				PostalCode: detail.EventZIPCode,
				Country:    detail.EventCountry,
			}
		}

		events = append(events, event)
	}

	// 按时间排序（最新的在前）
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	return events
}

func (u *USPS) parseUSPSDateTime(dateTimeStr string) (time.Time, error) {
	// USPS的时间格式可能有多种，尝试不同的解析格式
	formats := []string{
		"January 2, 2006 3:04 pm",
		"January 2, 2006 15:04",
		"January 2, 2006",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04:05",
		"01/02/2006 15:04",
		"01/02/2006",
	}

	for _, format := range formats {
		if timestamp, err := time.Parse(format, dateTimeStr); err == nil {
			return timestamp, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse datetime: %s", dateTimeStr)
}
