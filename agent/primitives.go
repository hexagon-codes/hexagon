// Package agent 提供 AI Agent 核心接口和实现
//
// primitives.go 实现高级 Agent 编排原语：
//   - SequentialAgent: 按顺序依次执行多个子 Agent
//   - ParallelAgent: 并行执行多个子 Agent，合并结果
//   - LoopAgent: 循环执行子 Agent 直到满足条件
//
// 这些原语提供比 Graph 更简单直观的编排方式，适合常见场景。
//
// 使用示例：
//
//	// 顺序执行：研究 → 撰写 → 审核
//	pipeline := NewSequentialAgent("pipeline",
//	    researchAgent,
//	    writerAgent,
//	    reviewerAgent,
//	)
//	output, err := pipeline.Run(ctx, Input{Query: "写一篇关于 AI 的文章"})
//
//	// 并行执行：同时搜索多个来源
//	searcher := NewParallelAgent("searcher",
//	    webSearchAgent,
//	    dbSearchAgent,
//	    docSearchAgent,
//	)
//	output, err := searcher.Run(ctx, Input{Query: "Go 并发模式"})
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
	"github.com/hexagon-codes/hexagon/internal/util"
	"github.com/hexagon-codes/toolkit/lang/syncx"
)

// ============== primitiveBase 公共基础 ==============

// primitiveBase 提供原语 Agent 的公共 Schema 和 Runnable 方法
// 三个原语（Sequential/Parallel/Loop）内嵌此结构体以避免重复代码
type primitiveBase struct{}

// InputSchema 返回输入 Schema
func (b *primitiveBase) InputSchema() *core.Schema {
	return core.SchemaOf[Input]()
}

// OutputSchema 返回输出 Schema
func (b *primitiveBase) OutputSchema() *core.Schema {
	return core.SchemaOf[Output]()
}

// collectAgentTools 聚合多个子 Agent 的工具列表（按名称去重）
func collectAgentTools(agents []Agent) []tool.Tool {
	seen := make(map[string]struct{})
	var result []tool.Tool
	for _, ag := range agents {
		for _, t := range ag.Tools() {
			if _, exists := seen[t.Name()]; !exists {
				seen[t.Name()] = struct{}{}
				result = append(result, t)
			}
		}
	}
	return result
}

// ============== SequentialAgent ==============

// SequentialAgent 顺序执行 Agent
// 按顺序依次执行多个子 Agent，前一个 Agent 的输出作为后一个的上下文
type SequentialAgent struct {
	primitiveBase
	config Config
	agents []Agent
}

// NewSequentialAgent 创建顺序执行 Agent
//
// 子 Agent 将按添加顺序依次执行，每个 Agent 的输出
// 会作为下一个 Agent 输入的 Context 传递。
func NewSequentialAgent(name string, agents []Agent, opts ...Option) *SequentialAgent {
	cfg := Config{
		ID:   util.AgentID(),
		Name: name,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	return &SequentialAgent{
		config: cfg,
		agents: agents,
	}
}

// Run 按顺序执行所有子 Agent
func (a *SequentialAgent) Run(ctx context.Context, input Input) (Output, error) {
	if len(a.agents) == 0 {
		return Output{}, fmt.Errorf("SequentialAgent %q 没有子 Agent", a.config.Name)
	}

	currentInput := input
	var lastOutput Output

	for i, agent := range a.agents {
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}

		output, err := agent.Run(ctx, currentInput)
		if err != nil {
			return Output{}, fmt.Errorf("SequentialAgent %q 第 %d 步 (%s) 失败: %w",
				a.config.Name, i+1, agent.ID(), err)
		}

		lastOutput = output

		// 将输出作为下一个 Agent 的输入上下文
		currentInput = Input{
			Query: output.Content,
			Context: map[string]any{
				"previous_agent":  agent.ID(),
				"previous_output": output.Content,
				"step":            i + 1,
			},
		}
	}

	// 合并所有工具调用记录
	lastOutput.Metadata = map[string]any{
		"agent_type": "sequential",
		"steps":      len(a.agents),
	}

	return lastOutput, nil
}

// ID 返回 Agent ID
func (a *SequentialAgent) ID() string { return a.config.ID }

// Name 返回 Agent 名称
func (a *SequentialAgent) Name() string { return a.config.Name }

// Description 返回描述
func (a *SequentialAgent) Description() string { return a.config.Description }

// Role 返回角色
func (a *SequentialAgent) Role() Role { return a.config.Role }

// Tools 返回工具列表（聚合所有子 Agent 的工具，按名称去重）
func (a *SequentialAgent) Tools() []tool.Tool { return collectAgentTools(a.agents) }

// Memory 返回记忆系统
func (a *SequentialAgent) Memory() memory.Memory { return a.config.Memory }

// LLM 返回 LLM Provider
func (a *SequentialAgent) LLM() llm.Provider { return a.config.LLM }

// Invoke 执行 Agent
func (a *SequentialAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *SequentialAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *SequentialAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *SequentialAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *SequentialAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (a *SequentialAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// ============== ParallelAgent ==============

// ParallelAgent 并行执行 Agent
// 同时执行多个子 Agent，合并所有结果
type ParallelAgent struct {
	primitiveBase
	config      Config
	agents      []Agent
	mergeFunc   func([]Output) Output
	maxParallel int // 最大并发数（0 表示不限制）
}

// ParallelOption ParallelAgent 专用选项
type ParallelOption func(*ParallelAgent)

// WithMergeFunc 设置结果合并函数
func WithMergeFunc(fn func([]Output) Output) ParallelOption {
	return func(a *ParallelAgent) {
		a.mergeFunc = fn
	}
}

// WithMaxParallel 设置最大并发数
func WithMaxParallel(n int) ParallelOption {
	return func(a *ParallelAgent) {
		a.maxParallel = n
	}
}

// NewParallelAgent 创建并行执行 Agent
//
// 所有子 Agent 将同时执行，结果通过 mergeFunc 合并。
// 默认合并策略：拼接所有输出内容。
func NewParallelAgent(name string, agents []Agent, popts ...ParallelOption) *ParallelAgent {
	a := &ParallelAgent{
		config: Config{
			ID:   util.AgentID(),
			Name: name,
		},
		agents: agents,
	}

	for _, opt := range popts {
		opt(a)
	}

	if a.mergeFunc == nil {
		a.mergeFunc = defaultMerge
	}

	return a
}

// Run 并行执行所有子 Agent
func (a *ParallelAgent) Run(ctx context.Context, input Input) (Output, error) {
	if len(a.agents) == 0 {
		return Output{}, fmt.Errorf("ParallelAgent %q 没有子 Agent", a.config.Name)
	}

	type result struct {
		index  int
		output Output
		err    error
	}

	results := make(chan result, len(a.agents))

	// 使用 toolkit/lang/syncx.Semaphore 控制并发度
	// maxParallel <= 0 时不限制并发，保持原有语义
	var sem *syncx.Semaphore
	if a.maxParallel > 0 {
		sem = syncx.NewSemaphore(a.maxParallel)
	}

	var wg sync.WaitGroup
	for i, agent := range a.agents {
		wg.Add(1)
		go func(idx int, ag Agent) {
			defer wg.Done()

			if sem != nil {
				sem.Acquire()
				defer sem.Release()
			}

			output, err := ag.Run(ctx, input)
			results <- result{index: idx, output: output, err: err}
		}(i, agent)
	}

	// 等待所有完成后关闭通道
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果
	outputs := make([]Output, len(a.agents))
	var errors []string
	for res := range results {
		if res.err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", a.agents[res.index].ID(), res.err))
			continue
		}
		outputs[res.index] = res.output
	}

	// 如果全部失败则返回错误
	if len(errors) == len(a.agents) {
		return Output{}, fmt.Errorf("ParallelAgent %q 所有子 Agent 失败: %s",
			a.config.Name, strings.Join(errors, "; "))
	}

	merged := a.mergeFunc(outputs)
	merged.Metadata = map[string]any{
		"agent_type": "parallel",
		"total":      len(a.agents),
		"failed":     len(errors),
		"errors":     errors,
	}

	return merged, nil
}

// ID 返回 Agent ID
func (a *ParallelAgent) ID() string { return a.config.ID }

// Name 返回 Agent 名称
func (a *ParallelAgent) Name() string { return a.config.Name }

// Description 返回描述
func (a *ParallelAgent) Description() string { return a.config.Description }

// Role 返回角色
func (a *ParallelAgent) Role() Role { return a.config.Role }

// Tools 返回工具列表（聚合所有子 Agent 的工具，按名称去重）
func (a *ParallelAgent) Tools() []tool.Tool { return collectAgentTools(a.agents) }

// Memory 返回记忆系统
func (a *ParallelAgent) Memory() memory.Memory { return a.config.Memory }

// LLM 返回 LLM Provider
func (a *ParallelAgent) LLM() llm.Provider { return a.config.LLM }

// Invoke 执行 Agent
func (a *ParallelAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *ParallelAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *ParallelAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *ParallelAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *ParallelAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (a *ParallelAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// defaultMerge 默认合并策略：拼接所有非空输出
func defaultMerge(outputs []Output) Output {
	var parts []string
	var allToolCalls []ToolCallRecord

	for _, o := range outputs {
		if o.Content != "" {
			parts = append(parts, o.Content)
		}
		allToolCalls = append(allToolCalls, o.ToolCalls...)
	}

	return Output{
		Content:   strings.Join(parts, "\n\n"),
		ToolCalls: allToolCalls,
	}
}

// ============== LoopAgent ==============

// LoopAgent 循环执行 Agent
// 反复执行子 Agent 直到满足终止条件
type LoopAgent struct {
	primitiveBase
	config    Config
	agent     Agent
	condition func(Output, int) bool // 终止条件：返回 true 则停止循环
	maxLoops  int                    // 最大循环次数
}

// LoopOption LoopAgent 专用选项
type LoopOption func(*LoopAgent)

// WithLoopCondition 设置循环终止条件
// condition 函数接收当前输出和循环次数，返回 true 时停止循环
func WithLoopCondition(condition func(Output, int) bool) LoopOption {
	return func(a *LoopAgent) {
		a.condition = condition
	}
}

// WithMaxLoops 设置最大循环次数
func WithMaxLoops(n int) LoopOption {
	return func(a *LoopAgent) {
		a.maxLoops = n
	}
}

// NewLoopAgent 创建循环执行 Agent
//
// 子 Agent 将反复执行，直到 condition 返回 true 或达到 maxLoops。
// 默认最大循环 10 次。
func NewLoopAgent(name string, agent Agent, lopts ...LoopOption) *LoopAgent {
	a := &LoopAgent{
		config: Config{
			ID:   util.AgentID(),
			Name: name,
		},
		agent:    agent,
		maxLoops: 10,
	}

	for _, opt := range lopts {
		opt(a)
	}

	if a.condition == nil {
		// 默认条件：达到最大循环次数停止
		a.condition = func(_ Output, iteration int) bool {
			return iteration >= a.maxLoops
		}
	}

	return a
}

// Run 循环执行子 Agent
func (a *LoopAgent) Run(ctx context.Context, input Input) (Output, error) {
	currentInput := input
	var lastOutput Output

	for i := 0; i < a.maxLoops; i++ {
		if ctx.Err() != nil {
			return Output{}, ctx.Err()
		}

		output, err := a.agent.Run(ctx, currentInput)
		if err != nil {
			return Output{}, fmt.Errorf("LoopAgent %q 第 %d 次循环失败: %w",
				a.config.Name, i+1, err)
		}

		lastOutput = output

		// 检查终止条件
		if a.condition(output, i+1) {
			break
		}

		// 将输出作为下一轮的输入
		currentInput = Input{
			Query: output.Content,
			Context: map[string]any{
				"loop_iteration":  i + 1,
				"previous_output": output.Content,
			},
		}
	}

	lastOutput.Metadata = map[string]any{
		"agent_type": "loop",
	}

	return lastOutput, nil
}

// ID 返回 Agent ID
func (a *LoopAgent) ID() string { return a.config.ID }

// Name 返回 Agent 名称
func (a *LoopAgent) Name() string { return a.config.Name }

// Description 返回描述
func (a *LoopAgent) Description() string { return a.config.Description }

// Role 返回角色
func (a *LoopAgent) Role() Role { return a.config.Role }

// Tools 返回工具列表（返回子 Agent 的工具）
func (a *LoopAgent) Tools() []tool.Tool { return a.agent.Tools() }

// Memory 返回记忆系统
func (a *LoopAgent) Memory() memory.Memory { return a.config.Memory }

// LLM 返回 LLM Provider
func (a *LoopAgent) LLM() llm.Provider { return a.config.LLM }

// Invoke 执行 Agent
func (a *LoopAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return a.Run(ctx, input)
}

// Stream 流式执行 Agent
func (a *LoopAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := a.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

// Batch 批量执行 Agent
func (a *LoopAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := a.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

// Collect 收集流式输入并执行
func (a *LoopAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return a.Run(ctx, collected)
}

// Transform 转换流
func (a *LoopAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := a.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

// BatchStream 批量流式执行
func (a *LoopAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := a.Batch(ctx, inputs, opts...)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}
