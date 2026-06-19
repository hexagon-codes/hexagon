package agent

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/testing/mock"
)

// TestAgentTool_Execute 子 Agent 经工具接口被调用，其最终回复作为工具结果返回。
func TestAgentTool_Execute(t *testing.T) {
	sub := NewReAct(WithName("researcher"), WithLLM(mock.FixedProvider("sub-answer")))
	at := NewAgentTool(sub)

	if at.Name() != "researcher" {
		t.Errorf("默认工具名应为子 agent 名, got %q", at.Name())
	}

	res, err := at.Execute(context.Background(), map[string]any{"query": "do research"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Output != "sub-answer" {
		t.Errorf("工具结果 = %+v, want Success+sub-answer", res)
	}
}

// TestAgentTool_Validate 校验缺失/非法 query。
func TestAgentTool_Validate(t *testing.T) {
	at := NewAgentTool(NewReAct(WithName("x"), WithLLM(mock.FixedProvider("x"))))
	if at.Validate(map[string]any{}) == nil {
		t.Error("缺 query 应报错")
	}
	if at.Validate(map[string]any{"query": ""}) == nil {
		t.Error("空 query 应报错")
	}
	if err := at.Validate(map[string]any{"query": "ok"}); err != nil {
		t.Errorf("合法 query 不应报错: %v", err)
	}
}

// TestAgentTool_Options 验证名称/描述/字段名覆盖。
func TestAgentTool_Options(t *testing.T) {
	at := NewAgentTool(
		NewReAct(WithName("base"), WithLLM(mock.FixedProvider("x"))),
		WithAgentToolName("delegate"),
		WithAgentToolDescription("自定义说明"),
		WithAgentToolQueryKey("task"),
	)
	if at.Name() != "delegate" || at.Description() != "自定义说明" {
		t.Errorf("名称/描述覆盖失败: %q / %q", at.Name(), at.Description())
	}
	// queryKey 改为 task 后，Schema 必填字段应为 task
	if len(at.Schema().Required) != 1 || at.Schema().Required[0] != "task" {
		t.Errorf("Schema 必填字段应为 task, got %v", at.Schema().Required)
	}
	if at.Validate(map[string]any{"task": "go"}) != nil {
		t.Error("task 字段应被接受")
	}
}

// TestAgentTool_RecursiveSubChain 递归子链：子 agent 作为工具挂到父 agent，
// 验证可组合（编译 + 父 agent 正常构造运行）。
func TestAgentTool_RecursiveSubChain(t *testing.T) {
	sub := NewReAct(WithName("sub"), WithLLM(mock.FixedProvider("from-sub")))
	parent := NewReAct(
		WithName("parent"),
		WithLLM(mock.FixedProvider("parent-final")),
		WithTools(NewAgentTool(sub)), // 子 agent 作为父 agent 的工具
	)

	// 父 agent 持有子 agent 工具
	tools := parent.Tools()
	found := false
	for _, tl := range tools {
		if tl.Name() == "sub" {
			found = true
		}
	}
	if !found {
		t.Error("父 agent 的工具集应包含子 agent 工具 'sub'")
	}

	// 父 agent 仍能正常运行（mock 不触发工具调用，直接产出）
	out, err := parent.Run(context.Background(), Input{Query: "go"})
	if err != nil || out.Content == "" {
		t.Fatalf("父 agent 运行失败: (%q,%v)", out.Content, err)
	}
}
