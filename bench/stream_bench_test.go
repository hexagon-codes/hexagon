package bench

import (
	"context"
	"strings"
	"testing"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

// BenchmarkStreamPipe 测试管道流创建和收集性能
func BenchmarkStreamPipe(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reader, writer := stream.Pipe[string](10)
		go func() {
			for j := 0; j < 10; j++ {
				writer.Send("item")
			}
			writer.Close()
		}()
		reader.Collect(ctx)
	}
}

// BenchmarkStreamFromSlice 测试切片流创建和收集性能
func BenchmarkStreamFromSlice(b *testing.B) {
	items := make([]string, 100)
	for i := range items {
		items[i] = "item"
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sr := stream.FromSlice(items)
		sr.Collect(ctx)
	}
}

// BenchmarkStreamMap 测试 Map 操作性能
func BenchmarkStreamMap(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sr := stream.FromSlice([]string{"hello", "world", "go", "hexagon"})
		mapped := stream.Map(sr, strings.ToUpper)
		mapped.Collect(ctx)
	}
}

// BenchmarkStreamFilter 测试 Filter 操作性能
func BenchmarkStreamFilter(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sr := stream.FromSlice([]string{"a", "hello", "b", "world", "c", "hexagon"})
		filtered := stream.Filter(sr, func(s string) bool {
			return len(s) > 3
		})
		filtered.Collect(ctx)
	}
}

// BenchmarkStreamMapFilter 测试 Map + Filter 组合性能
func BenchmarkStreamMapFilter(b *testing.B) {
	items := make([]string, 100)
	for i := range items {
		items[i] = "item"
		if i%2 == 0 {
			items[i] = "long-item-name"
		}
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sr := stream.FromSlice(items)
		mapped := stream.Map(sr, strings.ToUpper)
		filtered := stream.Filter(mapped, func(s string) bool {
			return len(s) > 5
		})
		filtered.Collect(ctx)
	}
}

// BenchmarkStreamMerge 测试合并流性能
func BenchmarkStreamMerge(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s1 := stream.FromSlice([]string{"a", "b", "c"})
		s2 := stream.FromSlice([]string{"d", "e", "f"})
		s3 := stream.FromSlice([]string{"g", "h", "i"})
		merged := stream.Merge(s1, s2, s3)
		merged.Collect(ctx)
	}
}

// BenchmarkStreamPipeConcurrent 测试并发管道流性能
func BenchmarkStreamPipeConcurrent(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			reader, writer := stream.Pipe[int](5)
			go func() {
				for j := 0; j < 5; j++ {
					writer.Send(j)
				}
				writer.Close()
			}()
			reader.Collect(ctx)
		}
	})
}
