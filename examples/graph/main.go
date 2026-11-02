// Package main demonstrates the Graph orchestration engine.
//
// This example shows how to use graph-based orchestration to build
// a multi-step workflow with conditional branching.
//
// Usage:
//
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hexagon-codes/hexagon"
	"github.com/hexagon-codes/hexagon/orchestration/graph"
)

// WorkflowState 定义工作流状态
type WorkflowState struct {
	Input     string            // 原始输入
	Category  string            // 分类结果
	Processed string            // 处理结果
	Final     string            // 最终输出
	Metadata  map[string]string // 元数据
}

// Clone 实现 State 接口
func (s WorkflowState) Clone() (graph.State, error) {
	clone := WorkflowState{
		Input:     s.Input,
		Category:  s.Category,
		Processed: s.Processed,
		Final:     s.Final,
		Metadata:  make(map[string]string),
	}
	for k, v := range s.Metadata {
		clone.Metadata[k] = v
	}
	return clone, nil
}

func main() {
	ctx := context.Background()

	// ========================================
	// 示例 1: 简单线性图
	// ========================================
	fmt.Println("=== 示例 1: 简单线性图 ===")
	runLinearGraph(ctx)

	// ========================================
	// 示例 2: 条件分支图
	// ========================================
	fmt.Println("\n=== 示例 2: 条件分支图 ===")
	runConditionalGraph(ctx)

	// ========================================
	// 示例 3: 流式执行
	// ========================================
	fmt.Println("\n=== 示例 3: 流式执行 ===")
	runStreamGraph(ctx)
}

// runLinearGraph 演示简单的线性图
func runLinearGraph(ctx context.Context) {
	// 构建图: START -> step1 -> step2 -> step3 -> END
	g, err := hexagon.NewGraph[WorkflowState]("linear-graph").
		AddNode("step1", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 step1: 分析输入")
			s.Category = "text"
			s.Metadata["step1"] = "completed"
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 step2: 处理数据")
			s.Processed = strings.ToUpper(s.Input)
			s.Metadata["step2"] = "completed"
			return s, nil
		}).
		AddNode("step3", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 step3: 生成结果")
			s.Final = fmt.Sprintf("Category: %s, Result: %s", s.Category, s.Processed)
			s.Metadata["step3"] = "completed"
			return s, nil
		}).
		AddEdge(hexagon.START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", "step3").
		AddEdge("step3", hexagon.END).
		Build()

	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	// 执行图
	initialState := WorkflowState{
		Input:    "hello world",
		Metadata: make(map[string]string),
	}

	result, err := g.Run(ctx, initialState)
	if err != nil {
		log.Fatalf("Failed to run graph: %v", err)
	}

	fmt.Printf("  最终结果: %s\n", result.Final)
	fmt.Printf("  元数据: %v\n", result.Metadata)
}

// runConditionalGraph 演示条件分支图
func runConditionalGraph(ctx context.Context) {
	// 构建图:
	//                    ┌─> process_text ─┐
	// START -> classify ─┤                 ├─> summarize -> END
	//                    └─> process_code ─┘

	g, err := hexagon.NewGraph[WorkflowState]("conditional-graph").
		AddNode("classify", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 classify: 分类输入")
			// 根据输入内容判断类型
			if strings.Contains(s.Input, "func ") || strings.Contains(s.Input, "package ") {
				s.Category = "code"
			} else {
				s.Category = "text"
			}
			fmt.Printf("    分类结果: %s\n", s.Category)
			return s, nil
		}).
		AddNode("process_text", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 process_text: 处理文本")
			s.Processed = "Text processed: " + s.Input
			return s, nil
		}).
		AddNode("process_code", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 process_code: 处理代码")
			s.Processed = "Code analyzed: " + s.Input
			return s, nil
		}).
		AddNode("summarize", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			fmt.Println("  执行 summarize: 生成摘要")
			s.Final = fmt.Sprintf("[%s] %s", s.Category, s.Processed)
			return s, nil
		}).
		AddEdge(hexagon.START, "classify").
		AddConditionalEdge("classify", func(s WorkflowState) string {
			// 路由函数：根据分类决定下一个节点
			if s.Category == "code" {
				return "code"
			}
			return "text"
		}, map[string]string{
			"text": "process_text",
			"code": "process_code",
		}).
		AddEdge("process_text", "summarize").
		AddEdge("process_code", "summarize").
		AddEdge("summarize", hexagon.END).
		Build()

	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	// 测试文本输入
	fmt.Println("\n  --- 测试文本输入 ---")
	textState := WorkflowState{
		Input:    "Hello, this is a text message",
		Metadata: make(map[string]string),
	}
	result1, _ := g.Run(ctx, textState)
	fmt.Printf("  结果: %s\n", result1.Final)

	// 测试代码输入
	fmt.Println("\n  --- 测试代码输入 ---")
	codeState := WorkflowState{
		Input:    "func main() { fmt.Println(\"Hello\") }",
		Metadata: make(map[string]string),
	}
	result2, _ := g.Run(ctx, codeState)
	fmt.Printf("  结果: %s\n", result2.Final)
}

// runStreamGraph 演示流式执行
func runStreamGraph(ctx context.Context) {
	g, _ := hexagon.NewGraph[WorkflowState]("stream-graph").
		AddNode("step1", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Processed = "step1 done"
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Processed += " -> step2 done"
			return s, nil
		}).
		AddNode("step3", func(ctx context.Context, s WorkflowState) (WorkflowState, error) {
			s.Final = s.Processed + " -> step3 done"
			return s, nil
		}).
		AddEdge(hexagon.START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", "step3").
		AddEdge("step3", hexagon.END).
		Build()

	initialState := WorkflowState{
		Input:    "test",
		Metadata: make(map[string]string),
	}

	// 流式执行，接收每个节点的事件
	events, _ := g.Stream(ctx, initialState)
	for event := range events {
		switch event.Type {
		case graph.EventTypeNodeStart:
			fmt.Printf("  [开始] 节点: %s\n", event.NodeName)
		case graph.EventTypeNodeEnd:
			fmt.Printf("  [完成] 节点: %s, 状态: %s\n", event.NodeName, event.State.Processed)
		case graph.EventTypeEnd:
			fmt.Printf("  [结束] 最终结果: %s\n", event.State.Final)
		case graph.EventTypeError:
			fmt.Printf("  [错误] %v\n", event.Error)
		}
	}
}
