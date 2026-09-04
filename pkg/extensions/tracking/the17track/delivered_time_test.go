package the17track

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flaboy/aira-shop/pkg/extensions/tracking/utils"
	"github.com/flaboy/pin"
	"github.com/gin-gonic/gin"
)

func TestDeliveredMilestoneTime(t *testing.T) {
	for _, tt := range []struct {
		name, status string
		milestones   []Milestone
		want         int64
		wantError    bool
	}{
		{"历史签收", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "2026-08-19T13:17:39Z"}}, 1787145459, false},
		{"带时区", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "2026-08-19T21:17:39+08:00"}}, 1787145459, false},
		{"缺失", "Delivered", nil, 0, true},
		{"无UTC时间", "Delivered", []Milestone{{KeyStage: "Delivered", TimeISO: "2026-08-19T13:17:39Z"}}, 0, true},
		{"非法", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "invalid"}}, 0, true},
		{"未来", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "9999-01-01T00:00:00Z"}}, 0, true},
		{"零时间", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "1970-01-01T00:00:00Z"}}, 0, true},
		{"重复里程碑", "Delivered", []Milestone{{KeyStage: "Delivered", TimeUTC: "2026-08-19T13:17:39Z"}, {KeyStage: "Delivered", TimeUTC: "2026-08-19T13:17:39Z"}}, 0, true},
		{"非签收忽略时间", "InTransit", []Milestone{{KeyStage: "Delivered", TimeUTC: "invalid"}}, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			info := TrackInfo{LatestStatus: Status{Status: tt.status}, Milestone: tt.milestones, LatestEvent: Event{TimeUTC: "2026-09-03T06:31:53Z"}}
			got, err := info.DeliveredAt()
			if (err != nil) != tt.wantError || got != tt.want {
				t.Fatalf("签收时间=%d，错误=%v，期望=%d，期望错误=%v", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestWebhookDeliveredTimeAndErrors(t *testing.T) {
	const valid = `{"event":"TRACKING_UPDATED","data":{"number":"TRACK","track_info":{"latest_status":{"status":"Delivered"},"milestone":[{"key_stage":"Delivered","time_utc":"2026-08-19T13:17:39Z"}],"latest_event":{"time_utc":"2026-09-03T06:31:53Z"}}}}`
	for _, tt := range []struct {
		name, body            string
		callbackError         error
		wantStatus, wantCalls int
	}{
		{"完整贯通", valid, nil, http.StatusOK, 1},
		{"错误必须向上传递", valid, errors.New("数据库写入失败"), http.StatusInternalServerError, 1},
		{"非法JSON", `{`, nil, http.StatusBadRequest, 0},
		{"缺签收时间", `{"event":"TRACKING_UPDATED","data":{"number":"TRACK","track_info":{"latest_status":{"status":"Delivered"}}}}`, nil, http.StatusBadRequest, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			utils.ClearCallbacks()
			t.Cleanup(utils.ClearCallbacks)
			calls := 0
			utils.RegisterStatusUpdateCallback(func(number string, status utils.TrackingStatus, deliveredAt int64) error {
				calls++
				if number != "TRACK" || status != utils.StatusDelivered || deliveredAt != 1787145459 {
					t.Fatalf("回调参数不符：%s %s %d", number, status, deliveredAt)
				}
				return tt.callbackError
			})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			if err := (&The17Track{}).HandleRequest(&pin.Context{Context: c}, "webhook"); err != nil {
				t.Fatal(err)
			}
			if w.Code != tt.wantStatus || calls != tt.wantCalls {
				t.Fatalf("HTTP=%d，回调次数=%d；期望HTTP=%d，次数=%d", w.Code, calls, tt.wantStatus, tt.wantCalls)
			}
		})
	}
}
