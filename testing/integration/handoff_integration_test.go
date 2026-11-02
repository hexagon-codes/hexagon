// Package integration 提供 Hexagon 框架的集成测试
//
// 本文件测试 Agent 交接（Handoff）机制：
//   - TransferTo: 创建转交工具
//   - HandoffHandler: 交接处理器
//   - SwarmRunner: Swarm 风格的 Agent 交接运行器
//   - ContextVariables: 上下文变量传递
//   - AgentAsTool: Agent 作为工具的包装器
package integration

import (
	"context"
	"testing"

	"github.com/hexagon-codes/ai-core/tool"
	"github.com/hexagon-codes/hexagon/agent"
	"github.com/hexagon-codes/hexagon/testing/mock"
)

// TestTransferToCreation 测试创建转交工具
func TestTransferToCreation(t *testing.T) {
	target := agent.NewBaseAgent(
		agent.WithName("support"),
		agent.WithDescription("客户支持助手"),
	)

	transferTool := agent.TransferTo(target)

	// 验证工具名称
	expectedName := "transfer_to_support"
	if transferTool.Name() != expectedName {
		t.Errorf("期望工具名为 '%s'，实际为 '%s'", expectedName, transferTool.Name())
	}

	// 验证工具描述包含目标 Agent 信息
	desc := transferTool.Description()
	if desc == "" {
		t.Error("期望工具描述不为空")
	}

	// 验证 Schema 不为 nil
	if transferTool.Schema() == nil {
		t.Error("期望 Schema 不为 nil")
	}
}

// TestTransferToValidation 测试转交工具参数验证
func TestTransferToValidation(t *testing.T) {
	target := agent.NewBaseAgent(agent.WithName("target"))
	transferTool := agent.TransferTo(target)

	// 缺少 message 参数应该返回错误
	err := transferTool.Validate(map[string]any{})
	if err == nil {
		t.Fatal("期望缺少 message 参数时返回错误")
	}

	// 提供 message 参数应该通过
	err = transferTool.Validate(map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("期望提供 message 参数时通过验证: %v", err)
	}
}

// TestTransferToExecution 测试转交工具执行
func TestTransferToExecution(t *testing.T) {
	target := agent.NewBaseAgent(agent.WithName("specialist"))
	transferTool := agent.TransferTo(target)

	ctx := context.Background()
	result, err := transferTool.Execute(ctx, map[string]any{
		"message": "请帮我处理这个问题",
		"reason":  "需要专家协助",
		"context": map[string]any{
			"topic": "技术问题",
		},
	})

	if err != nil {
		t.Fatalf("执行转交工具失败: %v", err)
	}

	if !result.Success {
		t.Error("期望转交成功")
	}

	// 验证输出是 Handoff 类型
	handoff, ok := result.Output.(agent.Handoff)
	if !ok {
		t.Fatalf("期望输出为 Handoff 类型，实际为 %T", result.Output)
	}

	if handoff.Message != "请帮我处理这个问题" {
		t.Errorf("期望消息为 '请帮我处理这个问题'，实际为 '%s'", handoff.Message)
	}

	if handoff.Reason != "需要专家协助" {
		t.Errorf("期望原因为 '需要专家协助'，实际为 '%s'", handoff.Reason)
	}

	if handoff.TargetAgent == nil {
		t.Fatal("期望目标 Agent 不为 nil")
	}

	if handoff.TargetAgent.Name() != "specialist" {
		t.Errorf("期望目标 Agent 名称为 'specialist'")
	}
}

// TestHandoffHandler 测试交接处理器
func TestHandoffHandler(t *testing.T) {
	target := agent.NewBaseAgent(agent.WithName("target"))

	handoffReceived := false
	handler := &agent.HandoffHandler{
		OnHandoff: func(ctx context.Context, handoff agent.Handoff) error {
			handoffReceived = true
			if handoff.TargetAgent.Name() != "target" {
				t.Errorf("期望目标为 'target'")
			}
			return nil
		},
	}

	ctx := context.Background()

	// 测试非交接结果
	normalResult := tool.Result{
		Success: true,
		Output:  "普通结果",
	}
	handoff, err := handler.ProcessToolResult(ctx, normalResult)
	if err != nil {
		t.Fatalf("处理普通结果失败: %v", err)
	}
	if handoff != nil {
		t.Error("期望普通结果不产生交接")
	}

	// 测试交接结果
	transferResult := tool.Result{
		Success: true,
		Output: agent.Handoff{
			TargetAgent: target,
			Message:     "交接",
			Reason:      "测试",
		},
	}
	handoff, err = handler.ProcessToolResult(ctx, transferResult)
	if err != nil {
		t.Fatalf("处理交接结果失败: %v", err)
	}
	if handoff == nil {
		t.Fatal("期望交接结果产生 Handoff")
	}
	if !handoffReceived {
		t.Error("期望 OnHandoff 回调被调用")
	}
}

// TestHandoffHandlerFailedResult 测试处理失败的工具结果
func TestHandoffHandlerFailedResult(t *testing.T) {
	handler := &agent.HandoffHandler{}

	ctx := context.Background()
	failedResult := tool.Result{
		Success: false,
		Output:  "失败",
	}

	handoff, err := handler.ProcessToolResult(ctx, failedResult)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if handoff != nil {
		t.Error("期望失败的结果不产生交接")
	}
}

// TestContextVariablesFlow 测试上下文变量在 Agent 间传递
func TestContextVariablesFlow(t *testing.T) {
	ctx := context.Background()

	// Agent A 设置初始变量
	agentAVars := agent.ContextVariables{
		"user_id":   "u123",
		"user_name": "Alice",
		"role":      "admin",
	}
	ctx = agent.ContextWithVariables(ctx, agentAVars)

	// 模拟 Agent A 完成任务后更新变量
	ctx = agent.UpdateContextVariables(ctx, agent.ContextVariables{
		"task_a_done":   true,
		"task_a_result": "已完成分析",
	})

	// Agent B 读取变量
	vars := agent.VariablesFromContext(ctx)

	// 验证所有变量可见
	checks := map[string]any{
		"user_id":       "u123",
		"user_name":     "Alice",
		"role":          "admin",
		"task_a_done":   true,
		"task_a_result": "已完成分析",
	}

	for key, expected := range checks {
		val, ok := vars.Get(key)
		if !ok {
			t.Errorf("期望变量 '%s' 存在", key)
			continue
		}
		if val != expected {
			t.Errorf("期望 %s=%v，实际为 %v", key, expected, val)
		}
	}
}

// TestSafeContextVariablesIntegration 测试线程安全的上下文变量
func TestSafeContextVariablesIntegration(t *testing.T) {
	// 创建共享变量
	shared := agent.NewSafeContextVariables()
	shared.Set("counter", 0)

	ctx := context.Background()
	ctx = agent.ContextWithSafeVariables(ctx, shared)

	// 从 context 中取回
	retrieved := agent.SafeVariablesFromContext(ctx)
	if retrieved == nil {
		t.Fatal("期望能从 context 中取回 SafeContextVariables")
	}

	// 验证克隆隔离
	cloned := retrieved.CloneSafe()
	cloned.Set("extra", "only_in_clone")

	_, ok := retrieved.Get("extra")
	if ok {
		t.Error("期望克隆后的修改不影响原始对象")
	}
}

// TestContextVariablesMerge 测试上下文变量合并
func TestContextVariablesMerge(t *testing.T) {
	vars1 := agent.ContextVariables{
		"key1": "value1",
		"key2": "value2",
	}

	vars2 := agent.ContextVariables{
		"key2": "overwritten",
		"key3": "value3",
	}

	vars1.Merge(vars2)

	// key2 应该被覆盖
	if val, _ := vars1.Get("key2"); val != "overwritten" {
		t.Errorf("期望 key2 被覆盖为 'overwritten'，实际为 '%v'", val)
	}

	// key3 应该被添加
	if val, _ := vars1.Get("key3"); val != "value3" {
		t.Errorf("期望 key3 为 'value3'")
	}

	// key1 应该保留
	if val, _ := vars1.Get("key1"); val != "value1" {
		t.Errorf("期望 key1 仍为 'value1'")
	}
}

// TestAgentAsToolCreation 测试 AgentAsTool 创建
func TestAgentAsToolCreation(t *testing.T) {
	provider := mock.FixedProvider("工具 Agent 的回复")
	workerAgent := agent.NewBaseAgent(
		agent.WithName("worker"),
		agent.WithDescription("工作 Agent"),
		agent.WithLLM(provider),
	)

	agentTool := agent.AgentAsTool(workerAgent)

	// 验证工具名称
	if agentTool.Name() != "agent_worker" {
		t.Errorf("期望工具名为 'agent_worker'，实际为 '%s'", agentTool.Name())
	}

	// 验证描述包含 Agent 信息
	desc := agentTool.Description()
	if desc == "" {
		t.Error("期望描述不为空")
	}

	// 验证参数校验
	err := agentTool.Validate(map[string]any{})
	if err == nil {
		t.Error("期望缺少 message 时返回错误")
	}

	err = agentTool.Validate(map[string]any{"message": "test"})
	if err != nil {
		t.Errorf("期望提供 message 时验证通过: %v", err)
	}
}

// TestAgentAsToolExecution 测试 AgentAsTool 执行
func TestAgentAsToolExecution(t *testing.T) {
	provider := mock.FixedProvider("工具 Agent 回复")
	workerAgent := agent.NewBaseAgent(
		agent.WithName("worker"),
		agent.WithLLM(provider),
	)

	agentTool := agent.AgentAsTool(workerAgent)

	ctx := context.Background()
	result, err := agentTool.Execute(ctx, map[string]any{
		"message": "请帮我做些事情",
	})

	if err != nil {
		t.Fatalf("执行 AgentAsTool 失败: %v", err)
	}

	if !result.Success {
		t.Errorf("期望执行成功，但失败: %v", result.Output)
	}

	// 完整子结果保留身份与用量，模型文本从结构化信封读取。
	output, ok := result.Output.(agent.ChildRunResult)
	if !ok {
		t.Fatalf("expected structured child result, got %T", result.Output)
	}
	if output.Child.Output.Content != "工具 Agent 回复" || output.RunID == "" || output.Version != 1 {
		t.Errorf("unexpected child result: %+v", output)
	}
}

// TestAgentAsToolExecutionError 测试 AgentAsTool 执行失败
func TestAgentAsToolExecutionError(t *testing.T) {
	// 不设置 LLM Provider，执行应该失败
	workerAgent := agent.NewBaseAgent(
		agent.WithName("broken"),
	)

	agentTool := agent.AgentAsTool(workerAgent)

	ctx := context.Background()
	result, err := agentTool.Execute(ctx, map[string]any{
		"message": "test",
	})

	// AgentAsTool 内部捕获错误，不返回 error
	if err != nil {
		t.Fatalf("不期望返回 error: %v", err)
	}

	// 但 result.Success 应该为 false
	if result.Success {
		t.Error("期望执行失败时 Success 为 false")
	}
}

// TestMultipleTransferToTools 测试创建多个转交工具
func TestMultipleTransferToTools(t *testing.T) {
	support := agent.NewBaseAgent(agent.WithName("support"))
	billing := agent.NewBaseAgent(agent.WithName("billing"))
	technical := agent.NewBaseAgent(agent.WithName("technical"))

	tools := []tool.Tool{
		agent.TransferTo(support),
		agent.TransferTo(billing),
		agent.TransferTo(technical),
	}

	// 验证每个工具名称唯一
	names := make(map[string]bool)
	for _, t := range tools {
		if names[t.Name()] {
			// 不使用 t.Errorf 因为 t 被工具变量覆盖
			panic("期望工具名称唯一")
		}
		names[t.Name()] = true
	}

	if len(names) != 3 {
		// 同理
		panic("期望 3 个唯一名称")
	}
}

// TestSwarmRunnerCreation 测试创建 Swarm 运行器
func TestSwarmRunnerCreation(t *testing.T) {
	initialAgent := agent.NewBaseAgent(agent.WithName("initial"))

	runner := agent.NewSwarmRunner(initialAgent)
	if runner == nil {
		t.Fatal("期望运行器不为 nil")
	}

	if runner.InitialAgent != initialAgent {
		t.Error("期望初始 Agent 正确")
	}

	if runner.MaxHandoffs != 10 {
		t.Errorf("期望默认 MaxHandoffs 为 10，实际为 %d", runner.MaxHandoffs)
	}

	if runner.GlobalState == nil {
		t.Error("期望 GlobalState 不为 nil")
	}
}

// TestContextVariablesFromEmptyContext 测试从空 context 获取变量
func TestContextVariablesFromEmptyContext(t *testing.T) {
	ctx := context.Background()

	vars := agent.VariablesFromContext(ctx)
	if vars != nil {
		t.Error("期望空 context 返回 nil")
	}

	safeVars := agent.SafeVariablesFromContext(ctx)
	if safeVars != nil {
		t.Error("期望空 context 返回 nil SafeContextVariables")
	}
}
