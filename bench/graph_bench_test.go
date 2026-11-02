package bench

import (
	"context"
	"testing"

	"github.com/hexagon-codes/hexagon/orchestration/graph"
)

// TestState 测试用的状态
type TestState struct {
	Counter int
	Data    string
}

func (s TestState) Clone() (graph.State, error) {
	return TestState{
		Counter: s.Counter,
		Data:    s.Data,
	}, nil
}

// BenchmarkGraphCreation 测试图创建性能
func BenchmarkGraphCreation(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = graph.NewGraph[TestState]("bench-graph").
			AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
				s.Counter++
				return s, nil
			}).
			AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
				s.Counter++
				return s, nil
			}).
			AddEdge(graph.START, "step1").
			AddEdge("step1", "step2").
			AddEdge("step2", graph.END).
			Build()
	}
}

// BenchmarkGraphRun 测试图执行性能
func BenchmarkGraphRun(b *testing.B) {
	g, _ := graph.NewGraph[TestState]("bench-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step3", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(graph.START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", "step3").
		AddEdge("step3", graph.END).
		Build()

	ctx := context.Background()
	initialState := TestState{Counter: 0, Data: "test"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = g.Run(ctx, initialState)
	}
}

// BenchmarkGraphRunConditional 测试条件图执行性能
func BenchmarkGraphRunConditional(b *testing.B) {
	g, _ := graph.NewGraph[TestState]("conditional-graph").
		AddNode("check", func(ctx context.Context, s TestState) (TestState, error) {
			return s, nil
		}).
		AddNode("path_a", func(ctx context.Context, s TestState) (TestState, error) {
			s.Data = "path_a"
			return s, nil
		}).
		AddNode("path_b", func(ctx context.Context, s TestState) (TestState, error) {
			s.Data = "path_b"
			return s, nil
		}).
		AddEdge(graph.START, "check").
		AddConditionalEdge("check", func(s TestState) string {
			if s.Counter%2 == 0 {
				return "a"
			}
			return "b"
		}, map[string]string{
			"a": "path_a",
			"b": "path_b",
		}).
		AddEdge("path_a", graph.END).
		AddEdge("path_b", graph.END).
		Build()

	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = g.Run(ctx, TestState{Counter: i, Data: ""})
	}
}

// BenchmarkGraphStream 测试图流式执行性能
func BenchmarkGraphStream(b *testing.B) {
	g, _ := graph.NewGraph[TestState]("stream-graph").
		AddNode("step1", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddNode("step2", func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		}).
		AddEdge(graph.START, "step1").
		AddEdge("step1", "step2").
		AddEdge("step2", graph.END).
		Build()

	ctx := context.Background()
	initialState := TestState{Counter: 0, Data: "test"}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		events, _ := g.Stream(ctx, initialState)
		for range events {
			// 消费所有事件
		}
	}
}

// BenchmarkMapState 测试 MapState 操作性能
func BenchmarkMapState(b *testing.B) {
	b.Run("Set", func(b *testing.B) {
		state := graph.MapState{}
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			state.Set("key", i)
		}
	})

	b.Run("Get", func(b *testing.B) {
		state := graph.MapState{"key": "value"}
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, _ = state.Get("key")
		}
	})

	b.Run("Clone", func(b *testing.B) {
		state := graph.MapState{
			"key1": "value1",
			"key2": "value2",
			"key3": "value3",
		}
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			_, _ = state.Clone()
		}
	})

	b.Run("Merge", func(b *testing.B) {
		state1 := graph.MapState{"key1": "value1"}
		state2 := graph.MapState{"key2": "value2", "key3": "value3"}
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			state1.Merge(state2)
		}
	})
}

// BenchmarkLargeGraph 测试大规模图执行性能
func BenchmarkLargeGraph(b *testing.B) {
	// 创建一个 10 节点的线性图
	builder := graph.NewGraph[TestState]("large-graph")

	nodeCount := 10
	for i := 0; i < nodeCount; i++ {
		nodeName := "step" + string(rune('a'+i))
		builder.AddNode(nodeName, func(ctx context.Context, s TestState) (TestState, error) {
			s.Counter++
			return s, nil
		})
	}

	// 添加边
	builder.AddEdge(graph.START, "stepa")
	for i := 0; i < nodeCount-1; i++ {
		from := "step" + string(rune('a'+i))
		to := "step" + string(rune('a'+i+1))
		builder.AddEdge(from, to)
	}
	builder.AddEdge("step"+string(rune('a'+nodeCount-1)), graph.END)

	g, _ := builder.Build()
	ctx := context.Background()
	initialState := TestState{Counter: 0}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = g.Run(ctx, initialState)
	}
}
