package evaluate

import (
	"context"
	"fmt"
)

// CustomEvaluator 自定义评估器
//
// 允许用户通过函数创建评估器，无需实现完整接口。
// 适用于快速原型验证或简单的评估逻辑。
//
// 使用示例：
//
//	eval := NewCustomEvaluator("length_check", "检查响应长度",
//	    func(ctx context.Context, input EvalInput) (*EvalResult, error) {
//	        score := float64(len(input.Response)) / 1000.0
//	        if score > 1.0 {
//	            score = 1.0
//	        }
//	        return &EvalResult{
//	            Name:  "length_check",
//	            Score: score,
//	        }, nil
//	    },
//	)
type CustomEvaluator struct {
	name        string
	description string
	evalFn      func(ctx context.Context, input EvalInput) (*EvalResult, error)
	requiresLLM bool
}

// CustomOption 自定义评估器选项
type CustomOption func(*CustomEvaluator)

// WithRequiresLLM 设置是否需要 LLM
func WithRequiresLLM(requires bool) CustomOption {
	return func(c *CustomEvaluator) {
		c.requiresLLM = requires
	}
}

// NewCustomEvaluator 创建自定义评估器
//
// 参数：
//   - name: 评估器名称，用于标识和报告
//   - description: 评估器描述
//   - fn: 评估函数，接收 EvalInput 返回 EvalResult
//   - opts: 可选配置项
//
// 如果 name 为空或 fn 为 nil，将会 panic。
func NewCustomEvaluator(name, description string, fn func(ctx context.Context, input EvalInput) (*EvalResult, error), opts ...CustomOption) *CustomEvaluator {
	if name == "" {
		panic("evaluate: custom evaluator name must not be empty")
	}
	if fn == nil {
		panic("evaluate: custom evaluator function must not be nil")
	}

	c := &CustomEvaluator{
		name:        name,
		description: description,
		evalFn:      fn,
		requiresLLM: false,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Name 返回评估器名称
func (c *CustomEvaluator) Name() string {
	return c.name
}

// Description 返回评估器描述
func (c *CustomEvaluator) Description() string {
	return c.description
}

// Evaluate 执行自定义评估
//
// 委托给用户提供的评估函数执行。
// 如果评估函数返回的结果中 Name 为空，会自动填充为评估器名称。
func (c *CustomEvaluator) Evaluate(ctx context.Context, input EvalInput) (*EvalResult, error) {
	result, err := c.evalFn(ctx, input)

	// 自动填充结果名称
	if result != nil && result.Name == "" {
		cloned := *result
		cloned.Name = c.name
		result = &cloned
	}
	if err != nil {
		return result, fmt.Errorf("custom evaluator %q: %w", c.name, err)
	}

	return result, nil
}

// RequiresLLM 返回是否需要 LLM
func (c *CustomEvaluator) RequiresLLM() bool {
	return c.requiresLLM
}

// 编译时检查 CustomEvaluator 实现了 Evaluator 接口
var _ Evaluator = (*CustomEvaluator)(nil)
