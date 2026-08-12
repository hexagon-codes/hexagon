// Package cost 提供 Hexagon AI Agent 框架的成本控制
//
// CostController 用于控制 Agent 的资源消耗，包括：
// - Token 使用限制
// - API 调用频率限制
// - 成本预算控制
package cost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"sync"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	hruntime "github.com/hexagon-codes/hexagon/runtime"
	"github.com/hexagon-codes/toolkit/util/rate"
)

// ErrInvalidControllerConfig 表示成本控制器配置无效。
var ErrInvalidControllerConfig = errors.New("invalid cost controller config")

// Controller 成本控制器
type Controller struct {
	mu sync.RWMutex

	// 预算相关
	budget    float64 // 总预算（美元）
	used      float64 // 已使用金额
	remaining float64 // 剩余金额

	// Token 相关
	maxTokensPerRequest int64 // 单次请求最大 Token，0 表示不限制
	maxTokensTotal      int64 // 总 Token 限制，0 表示不限制
	usedTokens          int64 // 已使用 Token

	// 速率限制 (使用 toolkit SlidingWindow)
	requestsPerMinute int                 // 每分钟请求数
	rateLimiter       *rate.SlidingWindow // 滑动窗口限流器

	// 回调
	onBudgetExceeded func(used, budget float64)
	onTokensExceeded func(used, limit int64)
	onRateExceeded   func(requests, limit int)

	// 定价表（每 1000 Token）
	pricing map[string]ModelPricing
}

// ModelPricing 模型定价
type ModelPricing struct {
	PromptPrice     float64 // 输入 Token 价格（每 1000 Token）
	CompletionPrice float64 // 输出 Token 价格（每 1000 Token）
}

// defaultPricing 是默认定价的私有真源，调用方只能通过快照读取。
var defaultPricing = map[string]ModelPricing{
	// OpenAI
	"gpt-4":         {PromptPrice: 0.03, CompletionPrice: 0.06},
	"gpt-4-turbo":   {PromptPrice: 0.01, CompletionPrice: 0.03},
	"gpt-4o":        {PromptPrice: 0.005, CompletionPrice: 0.015},
	"gpt-4o-mini":   {PromptPrice: 0.00015, CompletionPrice: 0.0006},
	"gpt-3.5-turbo": {PromptPrice: 0.0005, CompletionPrice: 0.0015},

	// Anthropic
	"claude-3-opus":   {PromptPrice: 0.015, CompletionPrice: 0.075},
	"claude-3-sonnet": {PromptPrice: 0.003, CompletionPrice: 0.015},
	"claude-3-haiku":  {PromptPrice: 0.00025, CompletionPrice: 0.00125},

	// DeepSeek
	"deepseek-chat":     {PromptPrice: 0.00014, CompletionPrice: 0.00028},
	"deepseek-reasoner": {PromptPrice: 0.00055, CompletionPrice: 0.00219},

	// 默认
	"default": {PromptPrice: 0.001, CompletionPrice: 0.002},
}

// DefaultPricing 返回默认定价表的独立快照。
func DefaultPricing() map[string]ModelPricing {
	return maps.Clone(defaultPricing)
}

type controllerConfig struct {
	budget              float64
	maxTokensPerRequest int64
	maxTokensTotal      int64
	requestsPerMinute   int
	onBudgetExceeded    func(used, budget float64)
	onTokensExceeded    func(used, limit int64)
	onRateExceeded      func(requests, limit int)
	pricing             map[string]ModelPricing
}

// ControllerOption 控制器选项
type ControllerOption func(*controllerConfig)

// NewController 创建成本控制器，并在返回前集中校验最终配置。
func NewController(opts ...ControllerOption) (*Controller, error) {
	config := controllerConfig{
		// 克隆私有默认定价真源，避免 WithPricing 的写入串扰其他 controller
		// （否则任一 controller 改价会泄漏到全局、串扰其他 controller）。
		pricing:             maps.Clone(defaultPricing),
		requestsPerMinute:   60,
		maxTokensPerRequest: 8000,
		maxTokensTotal:      1000000,
	}

	for index, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("%w: option %d must not be nil", ErrInvalidControllerConfig, index)
		}
		opt(&config)
	}

	if err := config.validate(); err != nil {
		return nil, err
	}

	// 初始化滑动窗口限流器
	limiter, err := rate.NewSlidingWindow(config.requestsPerMinute, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("%w: create rate limiter: %w", ErrInvalidControllerConfig, err)
	}

	return &Controller{
		budget:              config.budget,
		remaining:           config.budget,
		maxTokensPerRequest: config.maxTokensPerRequest,
		maxTokensTotal:      config.maxTokensTotal,
		requestsPerMinute:   config.requestsPerMinute,
		rateLimiter:         limiter,
		onBudgetExceeded:    config.onBudgetExceeded,
		onTokensExceeded:    config.onTokensExceeded,
		onRateExceeded:      config.onRateExceeded,
		pricing:             maps.Clone(config.pricing),
	}, nil
}

// validate 校验所有选项应用后的最终配置。
func (c controllerConfig) validate() error {
	if c.budget < 0 || math.IsNaN(c.budget) || math.IsInf(c.budget, 0) {
		return fmt.Errorf("%w: budget must be finite and non-negative", ErrInvalidControllerConfig)
	}
	if c.maxTokensPerRequest < 0 {
		return fmt.Errorf("%w: max tokens per request must not be negative", ErrInvalidControllerConfig)
	}
	if c.maxTokensTotal < 0 {
		return fmt.Errorf("%w: max tokens total must not be negative", ErrInvalidControllerConfig)
	}
	if c.requestsPerMinute <= 0 {
		return fmt.Errorf("%w: requests per minute %d: %w", ErrInvalidControllerConfig, c.requestsPerMinute, rate.ErrInvalidCapacity)
	}
	if c.pricing == nil {
		return fmt.Errorf("%w: pricing must not be nil", ErrInvalidControllerConfig)
	}
	if _, ok := c.pricing["default"]; !ok {
		return fmt.Errorf("%w: default pricing must be configured", ErrInvalidControllerConfig)
	}
	for model, pricing := range c.pricing {
		if pricing.PromptPrice < 0 || math.IsNaN(pricing.PromptPrice) || math.IsInf(pricing.PromptPrice, 0) {
			return fmt.Errorf("%w: prompt price for model %q must be finite and non-negative", ErrInvalidControllerConfig, model)
		}
		if pricing.CompletionPrice < 0 || math.IsNaN(pricing.CompletionPrice) || math.IsInf(pricing.CompletionPrice, 0) {
			return fmt.Errorf("%w: completion price for model %q must be finite and non-negative", ErrInvalidControllerConfig, model)
		}
	}
	return nil
}

// WithBudget 设置预算
func WithBudget(budget float64) ControllerOption {
	return func(c *controllerConfig) {
		c.budget = budget
	}
}

// WithMaxTokensPerRequest 设置单次请求最大 Token，0 表示不限制。
func WithMaxTokensPerRequest(tokens int64) ControllerOption {
	return func(c *controllerConfig) {
		c.maxTokensPerRequest = tokens
	}
}

// WithMaxTokensTotal 设置总 Token 限制，0 表示不限制。
func WithMaxTokensTotal(tokens int64) ControllerOption {
	return func(c *controllerConfig) {
		c.maxTokensTotal = tokens
	}
}

// WithRequestsPerMinute 设置每分钟请求数
func WithRequestsPerMinute(rpm int) ControllerOption {
	return func(c *controllerConfig) {
		c.requestsPerMinute = rpm
	}
}

// WithPricing 设置自定义定价表
func WithPricing(pricing map[string]ModelPricing) ControllerOption {
	return func(c *controllerConfig) {
		for k, v := range pricing {
			c.pricing[k] = v
		}
	}
}

// OnBudgetExceeded 设置预算超限回调
func OnBudgetExceeded(fn func(used, budget float64)) ControllerOption {
	return func(c *controllerConfig) {
		c.onBudgetExceeded = fn
	}
}

// OnTokensExceeded 设置 Token 超限回调
func OnTokensExceeded(fn func(used, limit int64)) ControllerOption {
	return func(c *controllerConfig) {
		c.onTokensExceeded = fn
	}
}

// OnRateExceeded 设置速率超限回调
func OnRateExceeded(fn func(requests, limit int)) ControllerOption {
	return func(c *controllerConfig) {
		c.onRateExceeded = fn
	}
}

// TokenUsage Token 使用量
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens 在存在拆分维度时可为零（自动采用拆分之和）或等于拆分之和；
	// 拆分维度均为零时可单独承载 aggregate-only 配额计数。
	TotalTokens int `json:"total_tokens"`
}

// validate 校验公开用量合同，并返回可安全写入账本的总 Token 数。
func (u TokenUsage) validate() (int64, error) {
	if u.PromptTokens < 0 {
		return 0, fmt.Errorf("invalid token usage: prompt tokens must not be negative")
	}
	if u.CompletionTokens < 0 {
		return 0, fmt.Errorf("invalid token usage: completion tokens must not be negative")
	}
	if u.TotalTokens < 0 {
		return 0, fmt.Errorf("invalid token usage: total tokens must not be negative")
	}
	if u.PromptTokens > math.MaxInt-u.CompletionTokens {
		return 0, fmt.Errorf("invalid token usage: prompt and completion token sum overflows int")
	}

	componentTotal := u.PromptTokens + u.CompletionTokens
	if componentTotal == 0 {
		return int64(u.TotalTokens), nil
	}
	if u.TotalTokens == 0 {
		return int64(componentTotal), nil
	}
	if u.TotalTokens != componentTotal {
		return 0, fmt.Errorf("invalid token usage: total tokens %d does not equal prompt and completion sum %d", u.TotalTokens, componentTotal)
	}
	return int64(componentTotal), nil
}

// CheckRequest 在真正发起一次 LLM 请求前检查上下文、单次/累计 Token
// 上限与请求频率。检查通过会消耗一次频率配额，但不预留 Token、不写入
// 实际用量；响应返回后应由 RecordUsageFunc 记录实际用量。
//
// Agent 场景的首选组合是：在 provider/调用方的每次外呼前对共享 Controller
// 调用 CheckRequest，并将 BudgetCostFunc 与 RecordUsageFunc 注入
// middleware.NewBudgetControl。NewBudgetControl 底层的 Budget 提供单 run 上限，
// CostControl 负责响应后的跨 run 累计记账与封顶。
func (c *Controller) CheckRequest(ctx context.Context, estimatedTokens int64) error {
	if ctx == nil {
		return fmt.Errorf("request context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("request context is done: %w", err)
	}
	if estimatedTokens < 0 {
		return fmt.Errorf("invalid estimated tokens: value must not be negative")
	}

	c.mu.Lock()
	callback, err := c.checkRequestLocked(ctx, estimatedTokens)
	c.mu.Unlock()
	if callback != nil {
		callback()
	}
	return err
}

// checkRequestLocked 在控制器锁内完成决策，并把用户回调延迟到解锁后执行。
func (c *Controller) checkRequestLocked(ctx context.Context, estimatedTokens int64) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("request context is done: %w", err)
	}
	// 检查单次请求 Token 限制
	if c.maxTokensPerRequest > 0 && estimatedTokens > c.maxTokensPerRequest {
		return nil, fmt.Errorf("request tokens %d exceeds limit %d", estimatedTokens, c.maxTokensPerRequest)
	}
	if c.usedTokens < 0 {
		return nil, fmt.Errorf("invalid token ledger: used tokens must not be negative")
	}
	if estimatedTokens > math.MaxInt64-c.usedTokens {
		return nil, fmt.Errorf("token total overflow: %d + %d exceeds int64", c.usedTokens, estimatedTokens)
	}
	projectedTokens := c.usedTokens + estimatedTokens

	// 检查总 Token 限制
	if c.maxTokensTotal > 0 && projectedTokens > c.maxTokensTotal {
		var callback func()
		if fn := c.onTokensExceeded; fn != nil {
			used, limit := c.usedTokens, c.maxTokensTotal
			callback = func() { fn(used, limit) }
		}
		return callback, fmt.Errorf("total tokens would exceed limit: %d + %d > %d",
			c.usedTokens, estimatedTokens, c.maxTokensTotal)
	}

	// 等待控制器锁期间上下文可能已取消，消费限流配额前必须再次确认。
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("request context is done: %w", err)
	}

	// 检查速率限制 (使用 toolkit SlidingWindow)
	allowed, count := c.rateLimiter.TryAllow()
	if !allowed {
		var callback func()
		if fn := c.onRateExceeded; fn != nil {
			limit := c.requestsPerMinute
			callback = func() { fn(count, limit) }
		}
		return callback, fmt.Errorf("rate limit exceeded: %d requests in last minute (limit: %d)",
			count, c.requestsPerMinute)
	}

	return nil, nil
}

// RecordUsage 记录使用量
//
// 注意：此方法采用"先检查后扣费"的原子操作模式，确保不会超额消费。
// 如果预算不足，会返回错误且不会记录使用量。
func (c *Controller) RecordUsage(model string, usage TokenUsage) error {
	totalTokens, err := usage.validate()
	if err != nil {
		return err
	}

	c.mu.Lock()
	callback, err := c.recordUsageLocked(model, usage, totalTokens)
	c.mu.Unlock()
	if callback != nil {
		callback()
	}
	return err
}

// recordUsageLocked 在同一临界区完成最终限额校验与写账，避免预检和记账之间的竞态。
func (c *Controller) recordUsageLocked(model string, usage TokenUsage, totalTokens int64) (func(), error) {
	pricing, ok := c.pricing[model]
	if !ok {
		pricing = c.pricing["default"]
	}

	cost, err := calculateCost(pricing, usage.PromptTokens, usage.CompletionTokens)
	if err != nil {
		return nil, fmt.Errorf("calculate cost for model %q: %w", model, err)
	}
	if c.usedTokens < 0 {
		return nil, fmt.Errorf("invalid token ledger: used tokens must not be negative")
	}
	if totalTokens > math.MaxInt64-c.usedTokens {
		return nil, fmt.Errorf("token ledger overflow: %d + %d exceeds int64", c.usedTokens, totalTokens)
	}
	newUsedTokens := c.usedTokens + totalTokens
	if c.maxTokensTotal > 0 && newUsedTokens > c.maxTokensTotal {
		var callback func()
		if fn := c.onTokensExceeded; fn != nil {
			used, limit := c.usedTokens, c.maxTokensTotal
			callback = func() { fn(used, limit) }
		}
		return callback, fmt.Errorf("total tokens would exceed limit: %d + %d > %d",
			c.usedTokens, totalTokens, c.maxTokensTotal)
	}
	if !isFiniteNonNegative(c.used) {
		return nil, fmt.Errorf("invalid cost ledger: used cost must be finite and non-negative")
	}
	newUsed := c.used + cost
	if !isFiniteNonNegative(newUsed) {
		return nil, fmt.Errorf("cost ledger overflow: accumulated cost is not finite")
	}

	// 先检查预算是否足够（原子性：检查和扣费在同一个锁内）
	if c.budget > 0 && newUsed > c.budget {
		var callback func()
		if fn := c.onBudgetExceeded; fn != nil {
			budget := c.budget
			callback = func() { fn(newUsed, budget) }
		}
		return callback, fmt.Errorf("budget exceeded: $%.4f + $%.4f would exceed $%.4f budget",
			c.used, cost, c.budget)
	}

	// 预算足够，执行扣费
	c.usedTokens = newUsedTokens
	c.used = newUsed
	c.remaining = c.budget - c.used

	return nil, nil
}

// calculateCost 使用公开的每千 Token 浮点定价直接计算，避免先放大到整数造成转换或乘法溢出。
func calculateCost(pricing ModelPricing, promptTokens, completionTokens int) (float64, error) {
	promptCost, ok := checkedTokenCost(promptTokens, pricing.PromptPrice)
	if !ok {
		return 0, fmt.Errorf("prompt cost is not finite")
	}
	completionCost, ok := checkedTokenCost(completionTokens, pricing.CompletionPrice)
	if !ok {
		return 0, fmt.Errorf("completion cost is not finite")
	}
	totalCost := promptCost + completionCost
	if !isFiniteNonNegative(totalCost) {
		return 0, fmt.Errorf("total cost is not finite")
	}
	return totalCost, nil
}

// checkedTokenCost 返回单类 Token 的有限非负成本。
func checkedTokenCost(tokens int, price float64) (float64, bool) {
	if tokens < 0 || !isFiniteNonNegative(price) {
		return 0, false
	}
	cost := (float64(tokens) / 1000) * price
	return cost, isFiniteNonNegative(cost)
}

func isFiniteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Stats 返回统计信息
func (c *Controller) Stats() ControllerStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return ControllerStats{
		Budget:          c.budget,
		Used:            c.used,
		Remaining:       c.remaining,
		UsedTokens:      c.usedTokens,
		MaxTokensTotal:  c.maxTokensTotal,
		RequestsLastMin: c.rateLimiter.Count(),
		RequestsPerMin:  c.requestsPerMinute,
	}
}

// ControllerStats 控制器统计
type ControllerStats struct {
	Budget          float64 `json:"budget"`
	Used            float64 `json:"used"`
	Remaining       float64 `json:"remaining"`
	UsedTokens      int64   `json:"used_tokens"`
	MaxTokensTotal  int64   `json:"max_tokens_total"`
	RequestsLastMin int     `json:"requests_last_min"`
	RequestsPerMin  int     `json:"requests_per_min"`
}

// Reset 重置统计
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.used = 0
	c.remaining = c.budget
	c.usedTokens = 0
	c.rateLimiter.Reset()
}

// EstimateCost 估算成本。输入无效或成本不可表示时返回正无穷，供无 error 签名的调用方安全拒绝。
func (c *Controller) EstimateCost(model string, promptTokens, completionTokens int) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	pricing, ok := c.pricing[model]
	if !ok {
		pricing = c.pricing["default"]
	}

	cost, err := calculateCost(pricing, promptTokens, completionTokens)
	if err != nil {
		return math.Inf(1)
	}
	return cost
}

// BudgetCostFunc 返回以 runtime.State 为输入的累计成本估算函数，首选作为
// runtime/middleware.NewBudgetControl 配置的 Cost 依赖注入；直接构造 Budget 时
// 也可作为其 Cost。
//
// 这是"meter→cost→budget 单向数据流"的规范桥接（路线图 §12 risk7 收尾）：
// State.Usage（计量数据）→ 本控制器 EstimateCost（成本估算所有权在此，不在别处重复）
// → Budget（唯一 fail-closed 强制点）。返回裸 func 签名（而非具名 CostFunc 类型），
// 使 runtime/middleware 无需反向依赖 security/cost——依赖方向保持单向（cost→runtime）。
//
// 首选用法（同一 Controller 还应在每次 LLM 外呼前执行 CheckRequest）：
//
//	budget := middleware.NewBudgetControl(middleware.BudgetControlConfig{
//	    Limits: ...,
//	    Cost:   costController.BudgetCostFunc(),
//	    Record: costController.RecordUsageFunc(),
//	})
func (c *Controller) BudgetCostFunc() func(*hruntime.State) float64 {
	return func(s *hruntime.State) float64 {
		if s == nil {
			return 0
		}
		model, _ := s.Attributes["model"].(string)
		return c.EstimateCost(model, s.Usage.PromptTokens, s.Usage.CompletionTokens)
	}
}

// RecordUsageFunc 返回把单次 LLM 调用用量记入本控制器**累计账**（used/usedTokens/remaining）
// 的桥接函数，首选作为 runtime/middleware.NewBudgetControl 配置的 Record
// 注入；直接构造 CostControl 时也可作为其 Record。
//
// 与 BudgetCostFunc 的区别决定了二者的语义分工：
//   - BudgetCostFunc 只**读** State.Usage（单 run）供底层 middleware.Budget 的 per-run 检查；
//   - 本函数**写**控制器的跨 run 累计账，且复用 RecordUsage 的"先检查后扣费"原子语义——
//     累计成本突破预算时返回错误且不记账。因控制器在 agent 的多次 run 间共享，故经
//     NewBudgetControl 底层的 middleware.CostControl 即可对多 run agent
//     （PlanExecute/Reflection）实现"全程累计预算"。
//
// CheckRequest 是外呼前的 Token/频率预检，不会写实际用量；它与本函数各司其职，
// 应在同一个跨 run 共享 Controller 上配合使用。
//
// 返回裸 func 签名（而非具名类型），使 runtime/middleware 无需反向依赖 security/cost，
// 依赖方向保持单向（cost→runtime）。
func (c *Controller) RecordUsageFunc() func(model string, usage llm.Usage) error {
	return func(model string, u llm.Usage) error {
		return c.RecordUsage(model, TokenUsage{
			PromptTokens:     u.PromptTokens,
			CompletionTokens: u.CompletionTokens,
			TotalTokens:      u.TotalTokens,
		})
	}
}

// RemainingBudget 返回剩余预算
func (c *Controller) RemainingBudget() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.remaining
}

// RemainingTokens 返回剩余 Token
// 如果未设置总 Token 限制（maxTokensTotal=0），返回 math.MaxInt64 表示无限制。
// 结果不会为负数。
func (c *Controller) RemainingTokens() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.maxTokensTotal <= 0 {
		return math.MaxInt64
	}
	remaining := c.maxTokensTotal - c.usedTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// CanAfford 检查是否能负担指定成本
func (c *Controller) CanAfford(estimatedCost float64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !isFiniteNonNegative(estimatedCost) {
		return false
	}
	if c.budget <= 0 {
		return true // 无预算限制
	}
	return c.remaining >= estimatedCost
}

// Context key
type controllerKey struct{}

// ContextWithController 将控制器添加到 context
func ContextWithController(ctx context.Context, c *Controller) context.Context {
	return context.WithValue(ctx, controllerKey{}, c)
}

// ControllerFromContext 从 context 获取控制器
func ControllerFromContext(ctx context.Context) *Controller {
	if c, ok := ctx.Value(controllerKey{}).(*Controller); ok {
		return c
	}
	return nil
}

// CheckAndRecord 检查并记录（便捷函数）
func CheckAndRecord(ctx context.Context, model string, usage TokenUsage) error {
	c := ControllerFromContext(ctx)
	if c == nil {
		return nil // 没有控制器，跳过检查
	}

	totalTokens, err := usage.validate()
	if err != nil {
		return err
	}
	if err := c.CheckRequest(ctx, totalTokens); err != nil {
		return err
	}

	return c.RecordUsage(model, usage)
}
