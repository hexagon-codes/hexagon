package graph

import (
	"context"
	"fmt"
)

// NodeType 节点类型
type NodeType int

const (
	// NodeTypeNormal 普通节点
	NodeTypeNormal NodeType = iota
	// NodeTypeStart 起始节点
	NodeTypeStart
	// NodeTypeEnd 结束节点
	NodeTypeEnd
	// NodeTypeConditional 条件节点
	NodeTypeConditional
	// NodeTypeParallel 并行节点
	NodeTypeParallel
	// NodeTypeSubgraph 子图节点
	NodeTypeSubgraph
	// NodeTypeCatch 错误捕获节点
	NodeTypeCatch
	// NodeTypeRetry 重试节点
	NodeTypeRetry
	// NodeTypeFallback 降级节点
	NodeTypeFallback
	// NodeTypeLoop 循环节点
	NodeTypeLoop
)

// Node 图节点
type Node[S State] struct {
	// Name 节点名称
	Name string

	// Type 节点类型
	Type NodeType

	// Handler 节点处理函数
	Handler NodeHandler[S]

	// Metadata 节点元数据
	Metadata map[string]any

	// RetryPolicy 重试策略
	RetryPolicy *RetryPolicy

	// Timeout 超时时间（毫秒）
	Timeout int64
	// IO 保存异构组件的真实类型与显式字段映射。
	IO *NodeIO
	// commandHandler 只保存不可变绑定，命令结果由本次节点执行持有。
	commandHandler CommandHandler[S]
}

// NodeHandler 节点处理函数类型
type NodeHandler[S State] func(ctx context.Context, state S) (S, error)

// NodeResult 节点执行结果
type NodeResult[S State] struct {
	// State 更新后的状态
	State S

	// NextNodes 下一个要执行的节点（用于条件路由）
	NextNodes []string

	// Error 执行错误
	Error error

	// Metadata 执行元数据
	Metadata map[string]any
}

// RetryPolicy 重试策略
type RetryPolicy struct {
	// MaxRetries 最大重试次数
	MaxRetries int

	// InitialDelay 初始延迟（毫秒）
	InitialDelay int64

	// MaxDelay 最大延迟（毫秒）
	MaxDelay int64

	// Multiplier 延迟倍数
	Multiplier float64

	// RetryOn 重试条件（返回 true 表示需要重试）
	RetryOn func(error) bool
}

// DefaultRetryPolicy 默认重试策略
func DefaultRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxRetries:   3,
		InitialDelay: 100,
		MaxDelay:     5000,
		Multiplier:   2.0,
		RetryOn: func(err error) bool {
			return err != nil
		},
	}
}

// NodeBuilder 节点构建器
type NodeBuilder[S State] struct {
	node *Node[S]
}

// NewNode 创建节点构建器
func NewNode[S State](name string, handler NodeHandler[S]) *NodeBuilder[S] {
	return &NodeBuilder[S]{
		node: &Node[S]{
			Name:     name,
			Type:     NodeTypeNormal,
			Handler:  handler,
			Metadata: make(map[string]any),
		},
	}
}

// WithType 设置节点类型
func (b *NodeBuilder[S]) WithType(t NodeType) *NodeBuilder[S] {
	b.node.Type = t
	return b
}

// WithMetadata 设置元数据
func (b *NodeBuilder[S]) WithMetadata(key string, value any) *NodeBuilder[S] {
	b.node.Metadata[key] = value
	return b
}

// WithRetry 设置重试策略
func (b *NodeBuilder[S]) WithRetry(policy *RetryPolicy) *NodeBuilder[S] {
	b.node.RetryPolicy = policy
	return b
}

// WithTimeout 设置超时时间（毫秒）
func (b *NodeBuilder[S]) WithTimeout(ms int64) *NodeBuilder[S] {
	b.node.Timeout = ms
	return b
}

// Build 构建节点
func (b *NodeBuilder[S]) Build() *Node[S] {
	return b.node
}

// 特殊节点名称常量
const (
	// START 起始节点名称
	START = "__start__"
	// END 结束节点名称
	END = "__end__"
)

// StartNode 创建起始节点
func StartNode[S State]() *Node[S] {
	return &Node[S]{
		Name: START,
		Type: NodeTypeStart,
		Handler: func(ctx context.Context, state S) (S, error) {
			return state, nil
		},
	}
}

// EndNode 创建结束节点
func EndNode[S State]() *Node[S] {
	return &Node[S]{
		Name: END,
		Type: NodeTypeEnd,
		Handler: func(ctx context.Context, state S) (S, error) {
			return state, nil
		},
	}
}

// ParallelNode 创建并行执行节点
// 将多个节点的处理函数并行执行，并合并结果
//
// 多个完整状态结果必须通过 ParallelNodeWithMerger 声明合并规则。
func ParallelNode[S State](name string, handlers ...NodeHandler[S]) *Node[S] {
	return ParallelNodeWithMerger(name, nil, handlers...)
}

// ParallelNodeWithMerger 创建带自定义状态合并器的并行执行节点
//
// 参数:
//   - name: 节点名称
//   - merger: 状态合并函数，用于合并所有 handler 的执行结果。
//     多个处理器时不能为 nil，单个处理器可直接返回结果。
//   - handlers: 要并行执行的处理函数列表
//
// StateMerger 定义在 subgraph.go 中：func(original S, outputs []S) S
// 错误返回前收敛全部已派发分支；ParallelExecutionError 保留各结果和完整错误树。
// 已成功或结果未知的分支阻止整组重试；全部普通失败仍服从节点重试策略。
func ParallelNodeWithMerger[S State](name string, merger StateMerger[S], handlers ...NodeHandler[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeParallel,
		Handler: func(ctx context.Context, state S) (S, error) {
			if len(handlers) == 0 {
				return state, nil
			}

			// 并行执行所有处理函数
			type result struct {
				index int
				state S
				err   error
			}
			results := make(chan result, len(handlers))

			for i, h := range handlers {
				i, h := i, h // 捕获变量
				go func() {
					s, err := runIsolatedNode(ctx, fmt.Sprintf("%s[%d]", name, i), h, state)
					results <- result{index: i, state: s, err: err}
				}()
			}

			// 收集所有结果，保持顺序
			states := make([]S, len(handlers))
			outcomes := make([]ParallelResult[S], len(handlers))
			for range handlers {
				r := <-results
				outcomes[r.index] = ParallelResult[S]{Name: fmt.Sprint(r.index), State: r.state, Error: r.err}
				states[r.index] = r.state
			}
			if err := parallelFailure(name, outcomes); err != nil {
				return state, err
			}

			// 使用合并器合并所有状态
			if merger == nil {
				if len(states) > 1 {
					return state, &ParallelExecutionError[S]{Name: name, Results: outcomes, Cause: fmt.Errorf("parallel node %q requires an explicit state merger", name)}
				}
				return states[len(states)-1], nil
			}
			return merger(state, states), nil
		},
		Metadata: map[string]any{
			"handler_count": len(handlers),
		},
	}
}

// ConditionalHandler 条件处理函数
// 返回下一个要执行的节点名称
type ConditionalHandler[S State] func(ctx context.Context, state S) string

// ConditionalNode 创建条件节点
func ConditionalNode[S State](name string, router ConditionalHandler[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeConditional,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 条件节点不修改状态，只决定路由
			return state, nil
		},
		Metadata: map[string]any{
			"router": router,
		},
	}
}

// ============== 错误处理节点 ==============

// ErrorHandler 错误处理函数类型
// 接收原始状态和错误，返回处理后的状态和是否已处理
type ErrorHandler[S State] func(ctx context.Context, state S, err error) (S, bool, error)

// CatchNode 创建错误捕获节点
// 用于捕获上游节点的错误并进行处理
//
// 参数:
//   - name: 节点名称
//   - handler: 错误处理函数，返回 (新状态, 是否继续执行, 错误)
//   - errorTypes: 要捕获的错误类型（可选，nil 表示捕获所有错误）
func CatchNode[S State](name string, handler ErrorHandler[S], errorTypes ...error) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeCatch,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 获取上游错误
			nodeErr, hasErr := ctx.Value(nodeErrorKey{}).(error)
			if !hasErr || nodeErr == nil {
				return state, nil
			}

			// 检查是否匹配错误类型
			if len(errorTypes) > 0 {
				matched := false
				for _, et := range errorTypes {
					if et == nodeErr || fmt.Sprintf("%T", et) == fmt.Sprintf("%T", nodeErr) {
						matched = true
						break
					}
				}
				if !matched {
					return state, nodeErr // 不匹配，继续传播错误
				}
			}

			// 执行错误处理
			newState, handled, err := handler(ctx, state, nodeErr)
			if err != nil {
				return newState, err
			}
			if !handled {
				return newState, nodeErr // 未处理，继续传播错误
			}
			return newState, nil // 错误已处理
		},
		Metadata: map[string]any{
			"error_handler": handler,
			"error_types":   errorTypes,
		},
	}
}

// FallbackNode 创建降级节点
// 当主节点执行失败时，执行降级逻辑
//
// 参数:
//   - name: 节点名称
//   - primaryHandler: 主处理函数
//   - fallbackHandler: 降级处理函数
func FallbackNode[S State](name string, primaryHandler, fallbackHandler NodeHandler[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeFallback,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 尝试主处理函数
			newState, err := runIsolatedNode(ctx, name+" primary", primaryHandler, state)
			if err == nil {
				return newState, nil
			}
			if nodeControlOrUnknown(err) {
				return state, err
			}

			// 主处理失败，执行降级
			fallbackState, fallbackErr := runIsolatedNode(ctx, name+" fallback", fallbackHandler, state)
			if fallbackErr != nil {
				// 降级也失败，返回原始错误
				return state, fmt.Errorf("primary error: %v, fallback error: %v", err, fallbackErr)
			}
			return fallbackState, nil
		},
		Metadata: map[string]any{
			"primary_handler":  primaryHandler,
			"fallback_handler": fallbackHandler,
		},
	}
}

// RetryNode 创建带重试的节点
// 包装一个普通节点，添加重试能力
//
// 参数:
//   - name: 节点名称
//   - handler: 节点处理函数
//   - policy: 重试策略
func RetryNode[S State](name string, handler NodeHandler[S], policy *RetryPolicy) *Node[S] {
	if policy == nil {
		policy = DefaultRetryPolicy()
	}

	return &Node[S]{
		Name:        name,
		Type:        NodeTypeRetry,
		RetryPolicy: policy,
		Handler:     handler,
		Metadata: map[string]any{
			"retry_policy": policy,
		},
	}
}

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	// FailureThreshold 失败阈值（连续失败多少次后熔断）
	FailureThreshold int
	// SuccessThreshold 成功阈值（连续成功多少次后恢复）
	SuccessThreshold int
	// Timeout 熔断超时时间（毫秒）
	Timeout int64
	// HalfOpenRequests 半开状态允许的请求数
	HalfOpenRequests int
}

// DefaultCircuitBreakerConfig 默认熔断器配置
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          30000,
		HalfOpenRequests: 3,
	}
}

// CircuitBreakerNode 创建熔断器节点
// 当失败次数达到阈值时，自动熔断，一段时间后进入半开状态尝试恢复
//
// 参数:
//   - name: 节点名称
//   - handler: 节点处理函数
//   - config: 熔断器配置
func CircuitBreakerNode[S State](name string, handler NodeHandler[S], config *CircuitBreakerConfig) *Node[S] {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}

	return &Node[S]{
		Name:    name,
		Type:    NodeTypeNormal,
		Handler: handler,
		Metadata: map[string]any{
			"circuit_breaker": config,
		},
	}
}

// TimeoutNode 创建带超时的节点
// 包装一个普通节点，添加超时控制
//
// 参数:
//   - name: 节点名称
//   - handler: 节点处理函数
//   - timeoutMs: 超时时间（毫秒）
func TimeoutNode[S State](name string, handler NodeHandler[S], timeoutMs int64) *Node[S] {
	return &Node[S]{
		Name:    name,
		Type:    NodeTypeNormal,
		Handler: handler,
		Timeout: timeoutMs,
	}
}

// BulkheadConfig 舱壁配置（并发隔离）
type BulkheadConfig struct {
	// MaxConcurrent 最大并发数
	MaxConcurrent int
	// MaxWait 最大等待时间（毫秒）
	MaxWait int64
}

// BulkheadNode 创建舱壁节点
// 限制并发执行数量，实现资源隔离
func BulkheadNode[S State](name string, handler NodeHandler[S], config *BulkheadConfig) *Node[S] {
	return &Node[S]{
		Name:    name,
		Type:    NodeTypeNormal,
		Handler: handler,
		Metadata: map[string]any{
			"bulkhead": config,
		},
	}
}

// nodeErrorKey 用于在 context 中传递错误的 key
type nodeErrorKey struct{}

// ContextWithNodeError 将节点错误添加到 context
func ContextWithNodeError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, nodeErrorKey{}, err)
}

// NodeErrorFromContext 从 context 获取节点错误
func NodeErrorFromContext(ctx context.Context) error {
	if err, ok := ctx.Value(nodeErrorKey{}).(error); ok {
		return err
	}
	return nil
}
