package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/hexagon-codes/ai-core/llm"
)

// PreparedRun 是宿主从同一次原子路由/pin 中派生的依赖集合。
// Request 为 nil 时保留输入；Middleware 为 nil 时保留 Runner 装配。
// Release 归 Runner 调用一次；返回错误前尚未交还的资源由准备器自行清理。
type PreparedRun struct {
	Request          *Request
	ProviderSelector ProviderSelector
	ToolExecutor     ToolExecutor
	Middleware       []Middleware
	References       []ContentReference
	Release          func()
}

type RunPreparer func(context.Context, Request) (PreparedRun, error)

type frozenRunSpecKey struct{}

func cloneReferences(references []ContentReference) []ContentReference {
	out := append([]ContentReference(nil), references...)
	for i := range out {
		out[i].Content = append([]byte(nil), out[i].Content...)
	}
	return out
}

// FrozenRunSpecFromContext 供准备器恢复原资源版本，不根据当前 latest 重新绑定旧 Run。
func FrozenRunSpecFromContext(ctx context.Context) (RunSpec, bool) {
	spec, ok := ctx.Value(frozenRunSpecKey{}).(RunSpec)
	spec.Request = append([]byte(nil), spec.Request...)
	spec.Resources = cloneReferences(spec.Resources)
	spec.StrategyReference.Content = append([]byte(nil), spec.StrategyReference.Content...)
	if spec.SelectedProvider != nil {
		route := *spec.SelectedProvider
		spec.SelectedProvider = &route
	}
	return spec, ok
}

func (r *DefaultRunner) prepareExecution(ctx context.Context, request Request) (*DefaultRunner, Request, []ContentReference, func(), error) {
	frozen, err := CloneRequest(request)
	if err != nil {
		return nil, Request{}, nil, nil, err
	}
	request = frozen
	if r.prepare == nil {
		return r, request, nil, nil, nil
	}
	var prepared PreparedRun
	err = invokeExtension("prepare_run", func() error {
		var err error
		prepared, err = r.prepare(ctx, request)
		return err
	})
	if err != nil {
		if prepared.Release != nil {
			err = errors.Join(err, invokeExtension("release_failed_prepare", func() error { prepared.Release(); return nil }))
		}
		return nil, Request{}, nil, nil, err
	}
	active := *r
	if prepared.Request != nil {
		if requestID(*prepared.Request) != requestID(request) {
			err := fmt.Errorf("runtime: prepared request identity differs")
			if prepared.Release != nil {
				err = errors.Join(err, invokeExtension("release_invalid_prepare", func() error { prepared.Release(); return nil }))
			}
			return nil, Request{}, nil, nil, err
		}
		request = *prepared.Request
	}
	if prepared.ProviderSelector != nil {
		active.selector = prepared.ProviderSelector
	}
	if prepared.ToolExecutor != nil {
		active.toolExecutor = prepared.ToolExecutor
	}
	if prepared.Middleware != nil {
		active.middleware = append([]Middleware(nil), prepared.Middleware...)
	}
	return &active, request, prepared.References, prepared.Release, nil
}

// CloneModelRequest 冻结传入 wrap/next 的数据；Provider 资源不在协议请求中复制。
func CloneModelRequest(request llm.CompletionRequest) (llm.CompletionRequest, error) {
	request.Messages = CloneOutputMessages(request.Messages)
	request.Metadata = cloneRuntimeMetadata(request.Metadata)
	tools, err := llm.CloneToolDefinitions(request.Tools)
	if err != nil {
		return llm.CompletionRequest{}, err
	}
	request.Tools = tools
	options := llm.GenerationOptions{ToolChoice: request.ToolChoice, MaxTokens: request.MaxTokens, Temperature: request.Temperature, TopP: request.TopP, Stop: request.Stop, User: request.User, ReasoningPolicyScope: request.ReasoningPolicyScope, ResponseFormat: request.ResponseFormat}
	return options.ApplyTo(request)
}
