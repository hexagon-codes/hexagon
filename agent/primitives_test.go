package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hexagon-codes/ai-core/llm"
	"github.com/hexagon-codes/ai-core/memory"
	stream "github.com/hexagon-codes/ai-core/streamx"
	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/core"
)

// ============== Mock Agent ==============

// mockAgent 是用于测试的 mock agent
type mockAgent struct {
	id          string
	name        string
	description string
	role        Role
	runFunc     func(context.Context, Input) (Output, error)
}

func newMockAgent(name string, runFunc func(context.Context, Input) (Output, error)) *mockAgent {
	return &mockAgent{
		id:          name + "-id",
		name:        name,
		description: "Mock agent: " + name,
		runFunc:     runFunc,
	}
}

func (m *mockAgent) ID() string                 { return m.id }
func (m *mockAgent) Name() string               { return m.name }
func (m *mockAgent) Description() string        { return m.description }
func (m *mockAgent) Role() Role                 { return m.role }
func (m *mockAgent) Tools() []tool.Tool         { return nil }
func (m *mockAgent) Memory() memory.Memory      { return nil }
func (m *mockAgent) LLM() llm.Provider          { return nil }
func (m *mockAgent) InputSchema() *core.Schema  { return core.SchemaOf[Input]() }
func (m *mockAgent) OutputSchema() *core.Schema { return core.SchemaOf[Output]() }

func (m *mockAgent) Run(ctx context.Context, input Input) (Output, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, input)
	}
	return Output{Content: "mock response from " + m.name}, nil
}

func (m *mockAgent) Invoke(ctx context.Context, input Input, opts ...core.Option) (Output, error) {
	return m.Run(ctx, input)
}

func (m *mockAgent) Stream(ctx context.Context, input Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	output, err := m.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	return stream.FromValue(output), nil
}

func (m *mockAgent) Batch(ctx context.Context, inputs []Input, opts ...core.Option) ([]Output, error) {
	results := make([]Output, len(inputs))
	for i, input := range inputs {
		output, err := m.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		results[i] = output
	}
	return results, nil
}

func (m *mockAgent) Collect(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (Output, error) {
	collected, err := stream.Concat(ctx, input)
	if err != nil {
		return Output{}, err
	}
	return m.Run(ctx, collected)
}

func (m *mockAgent) Transform(ctx context.Context, input *stream.StreamReader[Input], opts ...core.Option) (*stream.StreamReader[Output], error) {
	reader, writer := stream.Pipe[Output](10)
	go func() {
		defer writer.Close()
		for {
			in, err := input.Recv()
			if err != nil {
				return
			}
			result, err := m.Run(ctx, in)
			if err != nil {
				writer.CloseWithError(err)
				return
			}
			writer.Send(result)
		}
	}()
	return reader, nil
}

func (m *mockAgent) BatchStream(ctx context.Context, inputs []Input, opts ...core.Option) (*stream.StreamReader[Output], error) {
	results, err := m.Batch(ctx, inputs)
	if err != nil {
		return nil, err
	}
	return stream.FromSlice(results), nil
}

// ============== SequentialAgent Tests ==============

// TestSequentialAgent_Basic 测试创建和执行顺序 Agent
func TestSequentialAgent_Basic(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "step1: " + input.Query}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "step2: " + input.Query}, nil
	})

	agent3 := newMockAgent("agent3", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "step3: " + input.Query}, nil
	})

	seq := NewSequentialAgent("pipeline", []Agent{agent1, agent2, agent3})

	ctx := context.Background()
	output, err := seq.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证最终输出是第三步的结果
	if !strings.Contains(output.Content, "step3") {
		t.Errorf("输出内容不正确: got %q", output.Content)
	}

	// 验证元数据
	if output.Metadata == nil {
		t.Fatal("Metadata 为空")
	}
	if output.Metadata["agent_type"] != "sequential" {
		t.Errorf("agent_type 不正确: got %v", output.Metadata["agent_type"])
	}
	if output.Metadata["steps"] != 3 {
		t.Errorf("steps 数量不正确: got %v, want 3", output.Metadata["steps"])
	}
}

// TestSequentialAgent_Empty 测试无子 Agent 时返回错误
func TestSequentialAgent_Empty(t *testing.T) {
	seq := NewSequentialAgent("empty", []Agent{})

	ctx := context.Background()
	_, err := seq.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("期望返回错误，但成功了")
	}

	expectedMsg := "没有子 Agent"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("错误消息不匹配: got %q, want contain %q", err.Error(), expectedMsg)
	}
}

// TestSequentialAgent_Error 测试子 Agent 错误传播
func TestSequentialAgent_Error(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "step1"}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, errors.New("agent2 失败")
	})

	agent3 := newMockAgent("agent3", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "step3"}, nil
	})

	seq := NewSequentialAgent("pipeline", []Agent{agent1, agent2, agent3})

	ctx := context.Background()
	_, err := seq.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("期望返回错误，但成功了")
	}

	// 验证错误包含失败的 agent 信息
	if !strings.Contains(err.Error(), "agent2") {
		t.Errorf("错误消息应包含 agent2: got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "第 2 步") {
		t.Errorf("错误消息应包含步骤号: got %q", err.Error())
	}
}

// TestSequentialAgent_ID 测试 ID/Name/Description 方法
func TestSequentialAgent_ID(t *testing.T) {
	seq := NewSequentialAgent("test-seq", []Agent{
		newMockAgent("a1", nil),
	}, WithDescription("Test sequential agent"))

	if seq.Name() != "test-seq" {
		t.Errorf("Name 不正确: got %q, want %q", seq.Name(), "test-seq")
	}

	if seq.Description() != "Test sequential agent" {
		t.Errorf("Description 不正确: got %q", seq.Description())
	}

	if seq.ID() == "" {
		t.Error("ID 不应为空")
	}
}

// TestSequentialAgent_ContextPropagation 测试上下文传递
func TestSequentialAgent_ContextPropagation(t *testing.T) {
	var receivedContexts []map[string]any

	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		receivedContexts = append(receivedContexts, input.Context)
		return Output{Content: "result1"}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		receivedContexts = append(receivedContexts, input.Context)
		return Output{Content: "result2"}, nil
	})

	seq := NewSequentialAgent("pipeline", []Agent{agent1, agent2})

	ctx := context.Background()
	_, err := seq.Run(ctx, Input{Query: "test", Context: map[string]any{"initial": "value"}})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证第一个 agent 收到初始上下文
	if receivedContexts[0] == nil || receivedContexts[0]["initial"] != "value" {
		t.Errorf("agent1 未收到初始上下文: %v", receivedContexts[0])
	}

	// 验证第二个 agent 收到前一个 agent 的输出
	if receivedContexts[1] == nil || receivedContexts[1]["previous_output"] == nil {
		t.Errorf("agent2 未收到前一个 agent 的输出: %v", receivedContexts[1])
	}
}

// ============== ParallelAgent Tests ==============

// TestParallelAgent_Basic 测试创建和执行并行 Agent
func TestParallelAgent_Basic(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "result1"}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "result2"}, nil
	})

	agent3 := newMockAgent("agent3", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "result3"}, nil
	})

	parallel := NewParallelAgent("parallel", []Agent{agent1, agent2, agent3})

	ctx := context.Background()
	output, err := parallel.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证输出包含所有 agent 的结果
	if !strings.Contains(output.Content, "result1") {
		t.Errorf("输出应包含 result1: got %q", output.Content)
	}
	if !strings.Contains(output.Content, "result2") {
		t.Errorf("输出应包含 result2: got %q", output.Content)
	}
	if !strings.Contains(output.Content, "result3") {
		t.Errorf("输出应包含 result3: got %q", output.Content)
	}

	// 验证元数据
	if output.Metadata == nil {
		t.Fatal("Metadata 为空")
	}
	if output.Metadata["agent_type"] != "parallel" {
		t.Errorf("agent_type 不正确: got %v", output.Metadata["agent_type"])
	}
	if output.Metadata["total"] != 3 {
		t.Errorf("total 数量不正确: got %v, want 3", output.Metadata["total"])
	}
}

// TestParallelAgent_Empty 测试无子 Agent 时返回错误
func TestParallelAgent_Empty(t *testing.T) {
	parallel := NewParallelAgent("empty", []Agent{})

	ctx := context.Background()
	_, err := parallel.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("期望返回错误，但成功了")
	}

	expectedMsg := "没有子 Agent"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("错误消息不匹配: got %q, want contain %q", err.Error(), expectedMsg)
	}
}

// TestParallelAgent_MergeFunc 测试自定义合并函数
func TestParallelAgent_MergeFunc(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "10"}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "20"}, nil
	})

	// 自定义合并：计算总和
	mergeFunc := func(outputs []Output) Output {
		sum := 0
		for _, o := range outputs {
			if o.Content == "10" {
				sum += 10
			} else if o.Content == "20" {
				sum += 20
			}
		}
		return Output{Content: fmt.Sprintf("sum=%d", sum)}
	}

	parallel := NewParallelAgent("parallel", []Agent{agent1, agent2}, WithMergeFunc(mergeFunc))

	ctx := context.Background()
	output, err := parallel.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证合并结果
	if output.Content != "sum=30" {
		t.Errorf("合并结果不正确: got %q, want %q", output.Content, "sum=30")
	}
}

// TestParallelAgent_MaxParallel 测试并发限制
func TestParallelAgent_MaxParallel(t *testing.T) {
	var maxConcurrent atomic.Int64
	var currentConcurrent atomic.Int64

	makeAgent := func(id int) Agent {
		return newMockAgent(fmt.Sprintf("agent%d", id), func(ctx context.Context, input Input) (Output, error) {
			// 原子操作记录并发数
			cur := currentConcurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}

			// 模拟工作
			time.Sleep(10 * time.Millisecond)

			currentConcurrent.Add(-1)
			return Output{Content: fmt.Sprintf("result%d", id)}, nil
		})
	}

	agents := []Agent{
		makeAgent(1), makeAgent(2), makeAgent(3), makeAgent(4), makeAgent(5),
	}

	parallel := NewParallelAgent("parallel", agents, WithMaxParallel(2))

	ctx := context.Background()
	_, err := parallel.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证最大并发数不超过限制
	if maxConcurrent.Load() > 2 {
		t.Errorf("最大并发数超出预期: got %d, want <= 2", maxConcurrent.Load())
	}
}

// TestParallelAgent_PartialFailure 测试部分失败的情况
func TestParallelAgent_PartialFailure(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "success1"}, nil
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, errors.New("agent2 失败")
	})

	agent3 := newMockAgent("agent3", func(ctx context.Context, input Input) (Output, error) {
		return Output{Content: "success3"}, nil
	})

	parallel := NewParallelAgent("parallel", []Agent{agent1, agent2, agent3})

	ctx := context.Background()
	output, err := parallel.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("部分失败不应导致整体失败: %v", err)
	}

	// 验证成功的结果被包含
	if !strings.Contains(output.Content, "success1") || !strings.Contains(output.Content, "success3") {
		t.Errorf("输出应包含成功的结果: got %q", output.Content)
	}

	// 验证失败信息在元数据中
	if output.Metadata["failed"] != 1 {
		t.Errorf("failed 计数不正确: got %v, want 1", output.Metadata["failed"])
	}
}

// TestParallelAgent_AllFailed 测试全部失败时返回错误
func TestParallelAgent_AllFailed(t *testing.T) {
	agent1 := newMockAgent("agent1", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, errors.New("agent1 失败")
	})

	agent2 := newMockAgent("agent2", func(ctx context.Context, input Input) (Output, error) {
		return Output{}, errors.New("agent2 失败")
	})

	parallel := NewParallelAgent("parallel", []Agent{agent1, agent2})

	ctx := context.Background()
	_, err := parallel.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("全部失败应返回错误")
	}

	// 验证错误消息包含所有失败信息
	if !strings.Contains(err.Error(), "所有子 Agent 失败") {
		t.Errorf("错误消息不正确: got %q", err.Error())
	}
}

// ============== LoopAgent Tests ==============

// TestLoopAgent_Basic 测试创建和执行循环 Agent
func TestLoopAgent_Basic(t *testing.T) {
	loopCount := 0

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		loopCount++
		return Output{Content: fmt.Sprintf("iteration %d", loopCount)}, nil
	})

	loop := NewLoopAgent("loop", loopAgent, WithMaxLoops(3))

	ctx := context.Background()
	output, err := loop.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证循环次数
	if loopCount != 3 {
		t.Errorf("循环次数不正确: got %d, want 3", loopCount)
	}

	// 验证最终输出
	if !strings.Contains(output.Content, "iteration 3") {
		t.Errorf("输出不正确: got %q", output.Content)
	}

	// 验证元数据
	if output.Metadata == nil {
		t.Fatal("Metadata 为空")
	}
	if output.Metadata["agent_type"] != "loop" {
		t.Errorf("agent_type 不正确: got %v", output.Metadata["agent_type"])
	}
}

// TestLoopAgent_Condition 测试自定义终止条件
func TestLoopAgent_Condition(t *testing.T) {
	loopCount := 0

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		loopCount++
		return Output{Content: fmt.Sprintf("count=%d", loopCount)}, nil
	})

	// 条件：当输出包含 "count=5" 时停止
	condition := func(output Output, iteration int) bool {
		return strings.Contains(output.Content, "count=5")
	}

	loop := NewLoopAgent("loop", loopAgent,
		WithLoopCondition(condition),
		WithMaxLoops(10), // 设置更大的最大值
	)

	ctx := context.Background()
	output, err := loop.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证在条件满足时停止
	if loopCount != 5 {
		t.Errorf("循环次数不正确: got %d, want 5", loopCount)
	}

	if !strings.Contains(output.Content, "count=5") {
		t.Errorf("输出不正确: got %q", output.Content)
	}
}

// TestLoopAgent_MaxLoops 测试最大循环次数
func TestLoopAgent_MaxLoops(t *testing.T) {
	loopCount := 0

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		loopCount++
		return Output{Content: "loop"}, nil
	})

	// 不设置自定义条件，依赖默认的最大次数限制
	loop := NewLoopAgent("loop", loopAgent, WithMaxLoops(5))

	ctx := context.Background()
	_, err := loop.Run(ctx, Input{Query: "test"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证达到最大次数后停止
	if loopCount != 5 {
		t.Errorf("循环次数不正确: got %d, want 5", loopCount)
	}
}

// TestLoopAgent_Error 测试循环中的错误处理
func TestLoopAgent_Error(t *testing.T) {
	loopCount := 0

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		loopCount++
		if loopCount == 3 {
			return Output{}, errors.New("第 3 次循环失败")
		}
		return Output{Content: "ok"}, nil
	})

	loop := NewLoopAgent("loop", loopAgent, WithMaxLoops(5))

	ctx := context.Background()
	_, err := loop.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("期望返回错误，但成功了")
	}

	// 验证错误包含循环信息
	if !strings.Contains(err.Error(), "第 3 次循环失败") {
		t.Errorf("错误消息不正确: got %q", err.Error())
	}

	// 验证在第 3 次失败
	if loopCount != 3 {
		t.Errorf("循环次数不正确: got %d, want 3", loopCount)
	}
}

// TestLoopAgent_ContextCancellation 测试上下文取消
func TestLoopAgent_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		// 第一次被调用时立即取消上下文，cancel() 是同步的，
		// 调用后 ctx.Done() 立即关闭，无需跨 goroutine 同步。
		cancel()
		select {
		case <-ctx.Done():
			return Output{}, ctx.Err()
		default:
			return Output{Content: "ok"}, nil
		}
	})

	loop := NewLoopAgent("loop", loopAgent, WithMaxLoops(100))

	_, err := loop.Run(ctx, Input{Query: "test"})
	if err == nil {
		t.Fatal("期望返回取消错误，但成功了")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("错误类型不正确: got %v, want context.Canceled", err)
	}
}

// TestLoopAgent_InputPropagation 测试输入传播
func TestLoopAgent_InputPropagation(t *testing.T) {
	var receivedQueries []string

	loopAgent := newMockAgent("loop", func(ctx context.Context, input Input) (Output, error) {
		receivedQueries = append(receivedQueries, input.Query)
		return Output{Content: "iteration " + input.Query}, nil
	})

	loop := NewLoopAgent("loop", loopAgent, WithMaxLoops(3))

	ctx := context.Background()
	_, err := loop.Run(ctx, Input{Query: "initial"})
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}

	// 验证第一次收到初始查询
	if receivedQueries[0] != "initial" {
		t.Errorf("第一次查询不正确: got %q, want %q", receivedQueries[0], "initial")
	}

	// 验证后续循环收到前一次的输出
	for i := 1; i < len(receivedQueries); i++ {
		if !strings.Contains(receivedQueries[i], "iteration") {
			t.Errorf("第 %d 次查询应包含前一次输出: got %q", i+1, receivedQueries[i])
		}
	}
}
