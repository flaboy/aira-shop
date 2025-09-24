package ups

// 本包没有启用

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flaboy/aira-shop/pkg/extensions/tracking/utils"

	"github.com/flaboy/pin"
)

type UPS struct {
}

func (u *UPS) Init() error {
	// Initialization logic for UPS
	return nil
}

func (u *UPS) StartTracking(trackingNumber string) error {
	slog.Info("Starting UPS tracking", "trackingNumber", trackingNumber)
	slog.Info("UPS webhook URL", "url", utils.GetPublicUrl("ups", "/webhook"))
	return nil
}

func (u *UPS) HandleRequest(c *pin.Context) error {
	// Logic to handle requests for UPS
	return c.Render("Request handled by UPS")
}

func (u *UPS) GetProviderName() string {
	return "ups"
}

func (u *UPS) Convert(localdata *TrackingResponse) (*utils.TrackingResponse, error) {
	if localdata == nil || len(localdata.TrackResponse.Shipments) == 0 {
		return nil, fmt.Errorf("local data is nil or empty")
	}

	// 取第一个shipment
	shipment := localdata.TrackResponse.Shipments[0]
	if len(shipment.Packages) == 0 {
		return nil, fmt.Errorf("no packages found in shipment")
	}

	// 取第一个package
	pkg := shipment.Packages[0]

	response := &utils.TrackingResponse{
		TrackingNumber: pkg.TrackingNumber,
		Carrier:        "ups",
		CarrierCode:    "ups",
		RawData:        localdata,
	}

	// 转换状态信息
	response.Status = u.convertStatus(pkg.CurrentStatus.Code)
	response.StatusCode = pkg.CurrentStatus.Code
	response.StatusMessage = pkg.CurrentStatus.Description

	// 转换地址信息
	response.Origin = u.convertPackageAddresses(pkg.PackageAddresses, "origin")
	response.Destination = u.convertPackageAddresses(pkg.PackageAddresses, "destination")

	// 转换服务类型
	response.ServiceType = pkg.Service.Description

	// 转换时间信息
	if len(pkg.DeliveryDates) > 0 {
		for _, deliveryDate := range pkg.DeliveryDates {
			if deliveryDate.Date != nil && *deliveryDate.Date != "" {
				if timestamp, err := time.Parse("2006-01-02", *deliveryDate.Date); err == nil {
					response.EstimatedDeliveryDate = &timestamp
				}
			}
		}
	}

	// 转换包裹信息
	response.Package = &utils.PackageInfo{
		Pieces: pkg.PackageCount,
	}

	// 转换重量
	if pkg.Weight.Weight != "" {
		if weight, err := strconv.ParseFloat(pkg.Weight.Weight, 64); err == nil {
			response.Package.Weight = &utils.Weight{
				Value: weight,
				Unit:  strings.ToLower(pkg.Weight.UnitOfMeasurement),
			}
		}
	}

	// 转换尺寸
	if pkg.Dimension.Length != "" && pkg.Dimension.Width != "" && pkg.Dimension.Height != "" {
		if length, err := strconv.ParseFloat(pkg.Dimension.Length, 64); err == nil {
			if width, err := strconv.ParseFloat(pkg.Dimension.Width, 64); err == nil {
				if height, err := strconv.ParseFloat(pkg.Dimension.Height, 64); err == nil {
					response.Package.Dimensions = &utils.Dimensions{
						Length: length,
						Width:  width,
						Height: height,
						Unit:   strings.ToLower(pkg.Dimension.UnitOfDimension),
					}
				}
			}
		}
	}

	// 转换参考号码
	if len(pkg.ReferenceNumbers) > 0 && pkg.ReferenceNumbers[0].Number != nil {
		response.Package.Reference = *pkg.ReferenceNumbers[0].Number
	}

	// 转换送达信息
	if pkg.DeliveryInformation.ReceivedBy != "" ||
		pkg.DeliveryInformation.Signature.Image != nil ||
		pkg.DeliveryInformation.DeliveryPhoto.Photo != nil {
		response.DeliveryInfo = &utils.DeliveryInfo{
			DeliveredTo:      pkg.DeliveryInformation.ReceivedBy,
			DeliveryLocation: pkg.DeliveryInformation.Location,
		}

		if pkg.DeliveryInformation.Signature.Image != nil {
			response.DeliveryInfo.SignatureImage = *pkg.DeliveryInformation.Signature.Image
			response.DeliveryInfo.SignatureRequired = true
		}

		if pkg.DeliveryInformation.DeliveryPhoto.Photo != nil {
			response.DeliveryInfo.DeliveryPhoto = *pkg.DeliveryInformation.DeliveryPhoto.Photo
		}

		if pkg.DeliveryInformation.Pod.Content != nil {
			response.DeliveryInfo.DeliveryProof = *pkg.DeliveryInformation.Pod.Content
		}
	}

	// 转换事件历史
	response.Events = u.convertActivities(pkg.Activities)

	return response, nil
}

func (u *UPS) convertStatus(statusCode string) utils.TrackingStatus {
	switch strings.ToUpper(statusCode) {
	case "D", "DELIVERED":
		return utils.StatusDelivered
	case "I", "IN_TRANSIT":
		return utils.StatusInTransit
	case "O", "OUT_FOR_DELIVERY":
		return utils.StatusOutForDelivery
	case "P", "PICKUP", "LABEL_CREATED":
		return utils.StatusPreTransit
	case "X", "EXCEPTION":
		return utils.StatusException
	case "RS", "RETURN_TO_SENDER":
		return utils.StatusReturnToSender
	default:
		return utils.StatusUnknown
	}
}

func (u *UPS) convertPackageAddresses(addresses []PackageAddress, addressType string) *utils.Address {
	for _, addr := range addresses {
		if addr.Type != nil && strings.ToLower(*addr.Type) == addressType {
			result := &utils.Address{}

			if addr.Address != nil {
				// UPS的地址格式可能需要解析
				result.Street = *addr.Address
			}

			if addr.Name != nil {
				// 这里可能包含城市/州信息
				// 简单处理，实际可能需要更复杂的地址解析
			}

			return result
		}
	}
	return nil
}

func (u *UPS) convertActivities(activities []Activity) []utils.TrackingEvent {
	var events []utils.TrackingEvent

	for _, activity := range activities {
		event := utils.TrackingEvent{}

		// 转换状态
		if activity.Status != nil {
			event.Status = *activity.Status
		}

		// 转换描述
		if activity.Location != nil {
			event.Description = *activity.Location
		}

		// 转换时间
		if activity.Date != nil && activity.Time != nil {
			dateTimeStr := *activity.Date + " " + *activity.Time
			if timestamp, err := time.Parse("2006-01-02 15:04:05", dateTimeStr); err == nil {
				event.Timestamp = timestamp
			} else if timestamp, err := time.Parse("20060102 1504", dateTimeStr); err == nil {
				event.Timestamp = timestamp
			}
		}

		// 转换GMT时间（如果可用）
		if activity.GMTDate != nil && activity.GMTTime != nil {
			gmtDateTimeStr := *activity.GMTDate + " " + *activity.GMTTime
			if timestamp, err := time.Parse("2006-01-02 15:04:05", gmtDateTimeStr); err == nil {
				event.Timestamp = timestamp
			}
		}

		// 转换位置信息
		if activity.Location != nil {
			event.Location = &utils.Address{
				City: *activity.Location,
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
