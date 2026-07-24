package agent

import (
	"context"
	"log"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
)

// NewLoggingCallback 创建日志观测 Callback
// 记录工具调用事件，用于调试和审计
func NewLoggingCallback() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
			log.Printf("[Callback:OnStart] component=%s name=%s type=%s", info.Component, info.Name, info.Type)
			return ctx
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			log.Printf("[Callback:OnEnd] component=%s name=%s", info.Component, info.Name)
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			log.Printf("[Callback:OnError] component=%s name=%s error=%v", info.Component, info.Name, err)
			return ctx
		}).
		Build()
}

// Ensure components are referenced
var _ components.Component = components.ComponentOfTool
