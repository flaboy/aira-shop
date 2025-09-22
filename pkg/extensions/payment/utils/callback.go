package utils

import (
	"encoding/json"
	"log"
	"time"

	"github.com/flaboy/aira-core/pkg/hashid"
	"github.com/flaboy/aira-shop/pkg/events"
	"github.com/flaboy/aira-shop/pkg/models"
	"github.com/flaboy/aira-shop/pkg/types"
	"github.com/shopspring/decimal"

	"gorm.io/gorm"
)

var HashIDTypePayment = hashid.NewType("pm-", "payment", 6)

// DecodePaymentHashID 解码支付HashID获取数据库ID
func DecodePaymentHashID(hashID string) (uint, error) {
	return hashid.Decode(HashIDTypePayment, hashID)
}

// EncodePaymentID 编码数据库ID为HashID
func EncodePaymentID(id uint) string {
	return hashid.Encode(HashIDTypePayment, id)
}

// SerializeBusinessContext 序列化业务上下文为JSON字符串
func SerializeBusinessContext(ctx interface{}) (string, error) {
	data, err := json.Marshal(ctx)
	return string(data), err
}

// DeserializeBusinessContext 反序列化JSON字符串为业务上下文
func DeserializeBusinessContext(data string, target interface{}) error {
	return json.Unmarshal([]byte(data), target)
}

var Dec100 = decimal.NewFromInt(100)

func ConvertIntToDecemel(v int64) *decimal.Decimal {
	v2 := decimal.NewFromInt(v).Div(Dec100)
	return &v2
}

// NotifyBusinessSystem 通知业务系统支付已完成
// tx 为当前事务，如果传入nil则使用新事务
func NotifyBusinessSystem(tx *gorm.DB, paymentRecord *models.PaymentRecord) error {
	log.Printf("[NotifyBusinessSystem] Starting notification - PaymentID: %d, Amount: %d",
		paymentRecord.ID, paymentRecord.Amount)

	// 将业务上下文反序列化为JSON格式的RawMessage
	var businessContextJSON json.RawMessage
	if paymentRecord.BusinessContext != "" {
		businessContextJSON = json.RawMessage(paymentRecord.BusinessContext)
	}

	// 创建支付完成事件
	event := &types.PaymentCompletedEvent{
		TX:              tx,
		PaymentHashID:   EncodePaymentID(paymentRecord.ID),
		Channel:         paymentRecord.Channel,
		Amount:          ConvertIntToDecemel(paymentRecord.Amount),
		Currency:        paymentRecord.Currency,
		ExternalOrderID: paymentRecord.ExternalOrderID,
		BusinessContext: businessContextJSON,
	}

	log.Printf("[NotifyBusinessSystem] Created event with amount: %s", event.Amount.String())

	// 设置完成时间
	if paymentRecord.CompletedAt != nil {
		event.CompletedAt = *paymentRecord.CompletedAt
	} else {
		event.CompletedAt = time.Now()
	}

	// 触发事件，通知业务系统
	return events.EmitPaymentCompleted(event)
}
