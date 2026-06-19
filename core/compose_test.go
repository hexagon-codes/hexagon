package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

// ============== InvokeToStream 测试 ==============

func TestInvokeToStream_Stream(t *testing.T) {
	// 创建一个只有 Invoke 的 Runnable
	r := NewRunnable[string, int]("strlen", "returns string length",
		func(ctx context.Context, input string, opts ...Option) (int, error) {
			return len(input), nil
		},
	)

	adapted := InvokeToStream[string, int](r)

	// 验证 Invoke 仍然有效
	result, err := adapted.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 5 {
		t.Fatalf("expected 5, got %d", result)
	}

	// 验证 Stream 现在有效
	sr, err := adapted.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected recv error: %v", err)
	}
	if val != 5 {
		t.Fatalf("expected 5 from stream, got %d", val)
	}
}

func TestInvokeToStream_Name(t *testing.T) {
	r := NewRunnable[string, string]("original", "",
		func(ctx context.Context, input string, opts ...Option) (string, error) {
			return input, nil
		},
	)
	adapted := InvokeToStream[string, string](r)
	if !strings.Contains(adapted.Name(), "original") {
		t.Fatalf("expected name to contain 'original', got %q", adapted.Name())
	}
}

// ============== StreamToInvoke 测试 ==============

func TestStreamToInvoke_Invoke(t *testing.T) {
	// 创建一个只有 Stream 的 Runnable
	r := NewRunnable[int, int]("double", "doubles input",
		nil, // 无 Invoke
	).WithStream(func(ctx context.Context, input int, opts ...Option) (*stream.StreamReader[int], error) {
		return stream.FromValue(input * 2), nil
	})

	adapted := StreamToInvoke[int, int](r)

	// 验证 Invoke 现在有效（通过 Stream + Concat）
	result, err := adapted.Invoke(context.Background(), 21)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}

	// 验证 Stream 仍然有效
	sr, err := adapted.Stream(context.Background(), 5)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected recv error: %v", err)
	}
	if val != 10 {
		t.Fatalf("expected 10 from stream, got %d", val)
	}
}

// ============== Compose 测试 ==============

func TestCompose_Invoke(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("len=%d", n), nil
	})

	composed := Compose[string, int, string](r1, r2)

	result, err := composed.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "len=5" {
		t.Fatalf("expected 'len=5', got %q", result)
	}
}

func TestCompose_ErrorInFirst(t *testing.T) {
	r1 := RunnableFunc[string, int]("fail", func(ctx context.Context, s string) (int, error) {
		return 0, fmt.Errorf("first error")
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return "", nil
	})

	composed := Compose[string, int, string](r1, r2)

	_, err := composed.Invoke(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "first error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompose_ErrorInSecond(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("fail", func(ctx context.Context, n int) (string, error) {
		return "", fmt.Errorf("second error")
	})

	composed := Compose[string, int, string](r1, r2)

	_, err := composed.Invoke(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "second error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompose_Stream(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return strconv.Itoa(n), nil
	})

	composed := Compose[string, int, string](r1, r2)

	sr, err := composed.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected recv error: %v", err)
	}
	if val != "5" {
		t.Fatalf("expected '5', got %q", val)
	}
}

func TestCompose_Name(t *testing.T) {
	r1 := RunnableFunc[string, int]("parser", func(ctx context.Context, s string) (int, error) {
		return 0, nil
	})
	r2 := RunnableFunc[int, string]("formatter", func(ctx context.Context, n int) (string, error) {
		return "", nil
	})

	composed := Compose[string, int, string](r1, r2)
	name := composed.Name()
	if !strings.Contains(name, "parser") || !strings.Contains(name, "formatter") {
		t.Fatalf("expected name to contain both 'parser' and 'formatter', got %q", name)
	}
}

func TestCompose_Collect(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("%d", n), nil
	})

	composed := Compose[string, int, string](r1, r2)

	// Collect 会先 Concat 输入流，再走 Invoke 路径
	ctx := context.Background()
	result, err := composed.Collect(ctx, stream.FromSlice([]string{"ab", "cde"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Concat 对未注册类型返回最后一个元素 "cde"，len=3 → "3"
	if result != "3" {
		t.Fatalf("expected '3', got %q", result)
	}
}

func TestCompose_Transform(t *testing.T) {
	r1 := RunnableFunc[int, int]("double", func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("%d", n), nil
	})

	composed := Compose[int, int, string](r1, r2)

	sr := stream.FromSlice([]int{1, 2, 3})
	outSr, err := composed.Transform(context.Background(), sr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := outSr.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected collect error: %v", err)
	}
	expected := []string{"2", "4", "6"}
	for i, item := range items {
		if item != expected[i] {
			t.Fatalf("index %d: expected %q, got %q", i, expected[i], item)
		}
	}
}

// ============== ComposeStream 测试 ==============

func TestComposeStream_Invoke(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return fmt.Sprintf("len=%d", n), nil
	})

	composed := ComposeStream[string, int, string](r1, r2)

	// Invoke 路径与 Compose 相同
	result, err := composed.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "len=5" {
		t.Fatalf("expected 'len=5', got %q", result)
	}
}

func TestComposeStream_Stream(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return strconv.Itoa(n), nil
	})

	composed := ComposeStream[string, int, string](r1, r2)

	sr, err := composed.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected recv error: %v", err)
	}
	if val != "5" {
		t.Fatalf("expected '5', got %q", val)
	}
}

func TestComposeStream_Name(t *testing.T) {
	r1 := RunnableFunc[string, int]("parser", func(ctx context.Context, s string) (int, error) {
		return 0, nil
	})
	r2 := RunnableFunc[int, string]("formatter", func(ctx context.Context, n int) (string, error) {
		return "", nil
	})

	composed := ComposeStream[string, int, string](r1, r2)
	name := composed.Name()
	if !strings.Contains(name, "~>") {
		t.Fatalf("expected name to contain '~>' for stream compose, got %q", name)
	}
}

// ============== 多级组合测试 ==============

func TestCompose_ThreeStages(t *testing.T) {
	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		return len(s), nil
	})
	r2 := RunnableFunc[int, float64]("half", func(ctx context.Context, n int) (float64, error) {
		return float64(n) / 2.0, nil
	})
	r3 := RunnableFunc[float64, string]("format", func(ctx context.Context, f float64) (string, error) {
		return fmt.Sprintf("%.1f", f), nil
	})

	// 嵌套组合：(r1 → r2) → r3
	stage1 := Compose[string, int, float64](r1, r2)
	stage2 := Compose[string, float64, string](stage1, r3)

	result, err := stage2.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "hello" → 5 → 2.5 → "2.5"
	if result != "2.5" {
		t.Fatalf("expected '2.5', got %q", result)
	}
}

func TestCompose_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	r1 := RunnableFunc[string, int]("strlen", func(ctx context.Context, s string) (int, error) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return len(s), nil
	})
	r2 := RunnableFunc[int, string]("format", func(ctx context.Context, n int) (string, error) {
		return "", nil
	})

	composed := Compose[string, int, string](r1, r2)
	_, err := composed.Invoke(ctx, "hello")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}
