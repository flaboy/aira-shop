package serviceaction

import (
	"context"
	"encoding/json"
)

type Executor interface {
	Execute(ctx context.Context, args json.RawMessage) error
	GetType() ActionType
	Validate(args json.RawMessage) error
}

type ActionType string

type ExecutionContext struct {
	UserID      uint
	StaffID     uint
	Transaction interface{} // 抽象事务接口
}
