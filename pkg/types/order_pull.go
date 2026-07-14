package types

import (
	"time"
)

type OrderPullRequest struct {
	ShopLinkID uint      `json:"shop_link_id"`
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until"`
}

type OrderPullFailure struct {
	OrderID   string `json:"order_id"`
	OrderName string `json:"order_name"`
	Reason    string `json:"reason"`
}

type OrderPullResult struct {
	Orders   []OrderData        `json:"orders"`
	Failures []OrderPullFailure `json:"failures"`
	Total    int                `json:"total"`
}
