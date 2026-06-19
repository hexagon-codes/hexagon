// Package guard 提供 Hexagon AI Agent 框架的安全守卫能力
//
// Guard 用于在 Agent 执行前后进行安全检查，包括：
//   - Prompt 注入检测：检测恶意的 prompt 注入攻击
//   - PII 检测：检测和脱敏个人身份信息
//   - 内容过滤：过滤有害或敏感内容
//   - 输出验证：验证 Agent 输出是否符合要求
//
// 主要类型：
//   - Guard: 守卫接口，执行安全检查
//   - GuardChain: 守卫链，按顺序执行多个守卫
//   - CheckResult: 检查结果，包含通过状态、风险分数和发现的问题
//
// 守卫链模式：
//   - ChainModeAll: 所有守卫都必须通过
//   - ChainModeAny: 任一守卫通过即可
//   - ChainModeFirst: 第一个失败就停止
//
// 使用示例：
//
//	guard := NewGuardChain(ChainModeAll,
//	    NewPromptInjectionGuard(),
//	    NewPIIGuard(),
//	)
//	result, err := guard.Check(ctx, userInput)
package guard

import (
	"context"
	"fmt"
	"sync"
)

// Guard 安全守卫接口
type Guard interface {
	// Name 返回守卫名称
	Name() string

	// Check 执行检查
	Check(ctx context.Context, input string) (*CheckResult, error)

	// Enabled 是否启用
	Enabled() bool
}

// CheckResult 检查结果
type CheckResult struct {
	// Passed 是否通过
	Passed bool `json:"passed"`

	// Score 风险分数 (0-1，越高风险越大)
	Score float64 `json:"score"`

	// Category 风险类别
	Category string `json:"category,omitempty"`

	// Reason 原因
	Reason string `json:"reason,omitempty"`

	// Findings 发现的问题
	Findings []Finding `json:"findings,omitempty"`

	// Metadata 额外元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Finding 发现的问题
type Finding struct {
	// Type 问题类型
	Type string `json:"type"`

	// Text 问题文本
	Text string `json:"text"`

	// Position 位置
	Position Position `json:"position,omitempty"`

	// Severity 严重程度: low, medium, high, critical
	Severity string `json:"severity"`

	// Suggestion 建议
	Suggestion string `json:"suggestion,omitempty"`
}

// Position 文本位置
type Position struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// GuardChain 守卫链
// 按顺序执行多个守卫
type GuardChain struct {
	guards []Guard
	mode   ChainMode
	mu     sync.RWMutex
}

// ChainMode 链模式
type ChainMode int

const (
	// ChainModeAll 所有守卫都必须通过
	ChainModeAll ChainMode = iota
	// ChainModeAny 任一守卫通过即可
	ChainModeAny
	// ChainModeFirst 第一个失败就停止
	ChainModeFirst
)

// NewGuardChain 创建守卫链
func NewGuardChain(mode ChainMode, guards ...Guard) *GuardChain {
	return &GuardChain{
		guards: guards,
		mode:   mode,
	}
}

// Add 添加守卫
func (c *GuardChain) Add(g Guard) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.guards = append(c.guards, g)
}

// Check 执行检查
//
// 根据链模式执行安全检查：
//   - ChainModeAll: 所有启用的守卫都必须通过
//   - ChainModeAny: 任一启用的守卫通过即可
//   - ChainModeFirst: 遇到第一个失败的守卫就停止
//
// 线程安全：在迭代前创建守卫列表的副本
func (c *GuardChain) Check(ctx context.Context, input string) (*CheckResult, error) {
	c.mu.RLock()
	guards := make([]Guard, len(c.guards))
	copy(guards, c.guards)
	c.mu.RUnlock()

	var allFindings []Finding
	var maxScore float64 = 0
	passedCount := 0
	enabledCount := 0
	var lastFailedResult *CheckResult

	for _, guard := range guards {
		if !guard.Enabled() {
			continue
		}
		enabledCount++

		result, err := guard.Check(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("guard %s failed: %w", guard.Name(), err)
		}

		allFindings = append(allFindings, result.Findings...)
		if result.Score > maxScore {
			maxScore = result.Score
		}

		if result.Passed {
			passedCount++
			if c.mode == ChainModeAny {
				// Any 模式：任一通过即可返回成功
				return &CheckResult{
					Passed:   true,
					Score:    maxScore,
					Findings: allFindings,
				}, nil
			}
		} else {
			lastFailedResult = result
			if c.mode == ChainModeFirst {
				// First 模式：第一个失败就停止
				return &CheckResult{
					Passed:   false,
					Score:    maxScore,
					Category: result.Category,
					Reason:   result.Reason,
					Findings: allFindings,
				}, nil
			}
		}
	}

	// 没有启用的守卫，默认通过
	if enabledCount == 0 {
		return &CheckResult{
			Passed:   true,
			Score:    0,
			Findings: allFindings,
		}, nil
	}

	// 根据模式判断最终结果
	passed := false
	var reason string
	var category string

	switch c.mode {
	case ChainModeAll:
		// All 模式：所有启用的守卫都必须通过
		passed = passedCount == enabledCount
		if !passed && lastFailedResult != nil {
			reason = lastFailedResult.Reason
			category = lastFailedResult.Category
		}
	case ChainModeAny:
		// Any 模式：至少一个通过（如果走到这里说明没有任何通过）
		passed = passedCount > 0
		if !passed && lastFailedResult != nil {
			reason = "all guards failed"
			category = lastFailedResult.Category
		}
	case ChainModeFirst:
		// First 模式：如果走到这里说明所有守卫都通过了
		passed = true
	}

	return &CheckResult{
		Passed:   passed,
		Score:    maxScore,
		Category: category,
		Reason:   reason,
		Findings: allFindings,
	}, nil
}

// Name 返回名称
func (c *GuardChain) Name() string {
	return "guard_chain"
}

// Enabled 返回守卫链是否启用
//
// 链的"启用"语义应与 Check 的 enabledCount 一致：当且仅当存在至少一个
// 已启用的子守卫时才视为启用。仅判断 len(guards)>0 会把"全部子守卫被禁用"
// 误报为启用，与 Check 默认放行的行为不一致（回归: B12）。
func (c *GuardChain) Enabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, g := range c.guards {
		if g.Enabled() {
			return true
		}
	}
	return false
}

// 确保实现了 Guard 接口
var _ Guard = (*GuardChain)(nil)

// InputGuard 输入守卫（在 Agent 执行前检查）
type InputGuard interface {
	Guard
	// IsInputGuard 标记为输入守卫
	IsInputGuard()
}

// OutputGuard 输出守卫（在 Agent 执行后检查）
type OutputGuard interface {
	Guard
	// IsOutputGuard 标记为输出守卫
	IsOutputGuard()
}

// GuardConfig 守卫配置
type GuardConfig struct {
	// Enabled 是否启用
	Enabled bool `json:"enabled" yaml:"enabled"`

	// Threshold 风险阈值（超过则拒绝）
	Threshold float64 `json:"threshold" yaml:"threshold"`

	// Action 触发后的动作
	Action GuardAction `json:"action" yaml:"action"`

	// Categories 要检查的类别
	Categories []string `json:"categories" yaml:"categories"`

	// Allowlist 允许列表
	Allowlist []string `json:"allowlist" yaml:"allowlist"`

	// Blocklist 阻止列表
	Blocklist []string `json:"blocklist" yaml:"blocklist"`
}

// GuardAction 守卫动作
type GuardAction string

const (
	// ActionBlock 阻止
	ActionBlock GuardAction = "block"
	// ActionWarn 警告
	ActionWarn GuardAction = "warn"
	// ActionLog 仅记录
	ActionLog GuardAction = "log"
	// ActionRedact 脱敏
	ActionRedact GuardAction = "redact"
)

// DefaultConfig 默认配置
func DefaultConfig() *GuardConfig {
	return &GuardConfig{
		Enabled:   true,
		Threshold: 0.8,
		Action:    ActionBlock,
	}
}

// Middleware 守卫中间件
type Middleware func(ctx context.Context, input string, next func(context.Context, string) (string, error)) (string, error)

// ToMiddleware 将守卫转换为中间件
func ToMiddleware(g Guard, action GuardAction) Middleware {
	return func(ctx context.Context, input string, next func(context.Context, string) (string, error)) (string, error) {
		result, err := g.Check(ctx, input)
		if err != nil {
			return "", fmt.Errorf("guard check failed: %w", err)
		}

		if !result.Passed {
			switch action {
			case ActionBlock:
				return "", fmt.Errorf("blocked by guard %s: %s", g.Name(), result.Reason)
			case ActionWarn:
				// 继续执行但记录警告
				fmt.Printf("Warning from guard %s: %s\n", g.Name(), result.Reason)
			case ActionLog:
				// 仅记录
			}
		}

		return next(ctx, input)
	}
}
