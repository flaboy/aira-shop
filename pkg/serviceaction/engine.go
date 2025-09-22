package serviceaction

import (
	"context"
	"encoding/json"
	"fmt"
)

// Engine 服务动作引擎
type Engine struct {
	executors map[ActionType]Executor
}

// NewEngine 创建新的引擎实例
func NewEngine() *Engine {
	return &Engine{
		executors: make(map[ActionType]Executor),
	}
}

// Register 注册执行器
func (e *Engine) Register(executor Executor) {
	e.executors[executor.GetType()] = executor
}

// Execute 执行动作
func (e *Engine) Execute(actionType ActionType, ctx context.Context, args json.RawMessage) error {
	executor, exists := e.executors[actionType]
	if !exists {
		return fmt.Errorf("executor for action type %s not found", actionType)
	}

	// 验证参数
	if err := executor.Validate(args); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// 执行动作
	return executor.Execute(ctx, args)
}

// GetRegisteredTypes 获取已注册的动作类型
func (e *Engine) GetRegisteredTypes() []ActionType {
	types := make([]ActionType, 0, len(e.executors))
	for actionType := range e.executors {
		types = append(types, actionType)
	}
	return types
}
