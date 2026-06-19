// Package main 演示 Hexagon 流式处理
//
// 流处理系统支持：
//   - Pipe: 管道流（生产者/消费者模式）
//   - FromSlice: 从切片创建流
//   - Map/Filter: 流操作符
//   - Merge: 合并多个流
//   - Collect: 收集流中所有元素
//
// 运行方式:
//
//	go run ./examples/streaming/
package main

import (
	"context"
	"fmt"
	"strings"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

func main() {
	ctx := context.Background()

	fmt.Println("=== 示例 1: 管道流 (Pipe) ===")
	runPipeStream(ctx)

	fmt.Println("\n=== 示例 2: 切片流 + 操作符 ===")
	runSliceStream(ctx)

	fmt.Println("\n=== 示例 3: 合并多个流 ===")
	runMergeStreams(ctx)
}

// runPipeStream 演示生产者/消费者管道流
func runPipeStream(ctx context.Context) {
	reader, writer := stream.Pipe[string](5)

	// 生产者
	go func() {
		words := []string{"Hexagon", "是", "一个", "AI", "Agent", "框架"}
		for _, w := range words {
			writer.Send(w)
		}
		writer.Close()
	}()

	// 消费者: 收集所有元素
	items, err := reader.Collect(ctx)
	if err != nil {
		fmt.Printf("  错误: %v\n", err)
		return
	}
	fmt.Printf("  收到 %d 个元素: %v\n", len(items), items)
}

// runSliceStream 演示从切片创建流并使用操作符
func runSliceStream(ctx context.Context) {
	// 从切片创建流
	numbers := stream.FromSlice([]string{
		"hello", "world", "go", "hexagon", "ai", "agent", "framework",
	})

	// Map: 转大写
	upper := stream.Map(numbers, strings.ToUpper)

	// Filter: 只保留长度 > 3 的
	filtered := stream.Filter(upper, func(s string) bool {
		return len(s) > 3
	})

	// 收集结果
	result, err := filtered.Collect(ctx)
	if err != nil {
		fmt.Printf("  错误: %v\n", err)
		return
	}
	fmt.Printf("  原始: 7 个词\n")
	fmt.Printf("  转大写 + 过滤(len>3): %v\n", result)
}

// runMergeStreams 演示合并多个流
func runMergeStreams(ctx context.Context) {
	// 创建三个独立的流
	stream1 := stream.FromSlice([]string{"A1", "A2", "A3"})
	stream2 := stream.FromSlice([]string{"B1", "B2"})
	stream3 := stream.FromSlice([]string{"C1", "C2", "C3", "C4"})

	// 合并
	merged := stream.Merge(stream1, stream2, stream3)

	// 收集所有元素
	items, err := merged.Collect(ctx)
	if err != nil {
		fmt.Printf("  错误: %v\n", err)
		return
	}
	fmt.Printf("  合并 3 个流: 共 %d 个元素\n", len(items))
	fmt.Printf("  元素: %v\n", items)
}
