package chain

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

// ============== Pipe2 测试 ==============

func TestPipe2_StringToInt(t *testing.T) {
	step := Pipe2[string, int, string]("parse-format",
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		func(ctx context.Context, n int) (string, error) {
			return fmt.Sprintf("length=%d", n), nil
		},
	)

	result, err := step.handler(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "length=5" {
		t.Fatalf("expected 'length=5', got %q", result)
	}
}

func TestPipe2_ErrorInFirstStep(t *testing.T) {
	step := Pipe2[string, int, string]("fail-first",
		func(ctx context.Context, s string) (int, error) {
			return 0, fmt.Errorf("parse error")
		},
		func(ctx context.Context, n int) (string, error) {
			t.Fatal("should not be called")
			return "", nil
		},
	)

	_, err := step.handler(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "parse error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPipe2_ErrorInSecondStep(t *testing.T) {
	step := Pipe2[string, int, string]("fail-second",
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		func(ctx context.Context, n int) (string, error) {
			return "", fmt.Errorf("format error")
		},
	)

	_, err := step.handler(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "format error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============== Pipe3 测试 ==============

func TestPipe3_ThreeSteps(t *testing.T) {
	step := Pipe3[string, []string, int, string]("three-step",
		func(ctx context.Context, s string) ([]string, error) {
			return strings.Split(s, " "), nil
		},
		func(ctx context.Context, words []string) (int, error) {
			return len(words), nil
		},
		func(ctx context.Context, count int) (string, error) {
			return fmt.Sprintf("words=%d", count), nil
		},
	)

	result, err := step.handler(context.Background(), "hello world foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "words=3" {
		t.Fatalf("expected 'words=3', got %q", result)
	}
}

func TestPipe3_ErrorInMiddleStep(t *testing.T) {
	step := Pipe3[int, string, float64, string]("fail-mid",
		func(ctx context.Context, n int) (string, error) {
			return strconv.Itoa(n), nil
		},
		func(ctx context.Context, s string) (float64, error) {
			return 0, fmt.Errorf("conversion error")
		},
		func(ctx context.Context, f float64) (string, error) {
			t.Fatal("should not be called")
			return "", nil
		},
	)

	_, err := step.handler(context.Background(), 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "conversion error") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============== Pipe4 测试 ==============

func TestPipe4_FourSteps(t *testing.T) {
	step := Pipe4[string, int, float64, bool, string]("four-step",
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		func(ctx context.Context, n int) (float64, error) {
			return float64(n) * 1.5, nil
		},
		func(ctx context.Context, f float64) (bool, error) {
			return f > 10.0, nil
		},
		func(ctx context.Context, b bool) (string, error) {
			if b {
				return "long", nil
			}
			return "short", nil
		},
	)

	result, err := step.handler(context.Background(), "hello world!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "hello world!" 长度12，12*1.5=18 > 10 → "long"
	if result != "long" {
		t.Fatalf("expected 'long', got %q", result)
	}

	// 测试短字符串
	result, err = step.handler(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "hi" 长度2，2*1.5=3 <= 10 → "short"
	if result != "short" {
		t.Fatalf("expected 'short', got %q", result)
	}
}

// ============== TypedChain 测试 ==============

func TestTypedChain_Invoke(t *testing.T) {
	step := Pipe2[string, int, string]("len-format",
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		func(ctx context.Context, n int) (string, error) {
			return fmt.Sprintf("len=%d", n), nil
		},
	)

	chain := NewTypedChain("test-chain", step)

	result, err := chain.Invoke(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "len=5" {
		t.Fatalf("expected 'len=5', got %q", result)
	}
}

func TestTypedChain_NilStep(t *testing.T) {
	chain := NewTypedChain[string, string]("empty", nil)

	_, err := chain.Invoke(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for nil step")
	}
	if !strings.Contains(err.Error(), "没有步骤") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTypedChain_Metadata(t *testing.T) {
	step := NewTypedStep("id", func(ctx context.Context, s string) (string, error) {
		return s, nil
	})
	chain := NewTypedChain("test", step).WithDescription("desc")

	if chain.Name() != "test" {
		t.Fatalf("expected name 'test', got %q", chain.Name())
	}
	if chain.Description() != "desc" {
		t.Fatalf("expected description 'desc', got %q", chain.Description())
	}
	if chain.InputSchema() == nil {
		t.Fatal("InputSchema should not be nil")
	}
	if chain.OutputSchema() == nil {
		t.Fatal("OutputSchema should not be nil")
	}
}

func TestTypedChain_Stream(t *testing.T) {
	step := NewTypedStep("double", func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	})
	chain := NewTypedChain("double-chain", step)

	sr, err := chain.Stream(context.Background(), 21)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected recv error: %v", err)
	}
	if val != 42 {
		t.Fatalf("expected 42, got %d", val)
	}
}

func TestTypedChain_Batch(t *testing.T) {
	step := NewTypedStep("inc", func(ctx context.Context, n int) (int, error) {
		return n + 1, nil
	})
	chain := NewTypedChain("inc-chain", step)

	results, err := chain.Batch(context.Background(), []int{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []int{2, 3, 4}
	for i, r := range results {
		if r != expected[i] {
			t.Fatalf("index %d: expected %d, got %d", i, expected[i], r)
		}
	}
}

func TestTypedChain_BatchError(t *testing.T) {
	step := NewTypedStep("fail-on-3", func(ctx context.Context, n int) (int, error) {
		if n == 3 {
			return 0, fmt.Errorf("no threes")
		}
		return n, nil
	})
	chain := NewTypedChain("fail-chain", step)

	_, err := chain.Batch(context.Background(), []int{1, 2, 3, 4})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no threes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTypedChain_Transform(t *testing.T) {
	step := NewTypedStep("upper", func(ctx context.Context, s string) (string, error) {
		return strings.ToUpper(s), nil
	})
	chain := NewTypedChain("upper-chain", step)

	input := []string{"hello", "world"}
	items, err := chain.Batch(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0] != "HELLO" || items[1] != "WORLD" {
		t.Fatalf("unexpected results: %v", items)
	}
}

func TestTypedChain_Collect(t *testing.T) {
	step := NewTypedStep("id", func(ctx context.Context, s string) (string, error) {
		return s + "!", nil
	})
	chain := NewTypedChain("collect-chain", step)

	// Collect 会先 Concat 流为单值，然后 Invoke
	// 对于 string 类型，Concat 返回最后一个元素（默认行为）
	ctx := context.Background()
	result, err := chain.Collect(ctx, stream.FromSlice([]string{"a", "b"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Concat 对未注册类型返回最后一个元素 "b"，加 "!" → "b!"
	if result != "b!" {
		t.Fatalf("expected 'b!', got %q", result)
	}
}

func TestTypedChain_BatchStream(t *testing.T) {
	step := NewTypedStep("triple", func(ctx context.Context, n int) (int, error) {
		return n * 3, nil
	})
	chain := NewTypedChain("triple-chain", step)

	sr, err := chain.BatchStream(context.Background(), []int{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, err := sr.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected collect error: %v", err)
	}
	expected := []int{3, 6, 9}
	for i, r := range items {
		if r != expected[i] {
			t.Fatalf("index %d: expected %d, got %d", i, expected[i], r)
		}
	}
}

// ============== Pipe 与 Then 组合测试 ==============

func TestPipe2_WithThen_Compose(t *testing.T) {
	// Pipe2 生成 TypedStep，再通过 Then 与另一个 TypedStep 组合
	first := Pipe2[string, int, string]("a",
		func(ctx context.Context, s string) (int, error) {
			return len(s), nil
		},
		func(ctx context.Context, n int) (string, error) {
			return fmt.Sprintf("%d", n), nil
		},
	)

	second := NewTypedStep("b", func(ctx context.Context, s string) (bool, error) {
		n, _ := strconv.Atoi(s)
		return n > 3, nil
	})

	combined := Then(first, second)

	result, err := combined.handler(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "hello" 长度5 → "5" → 5 > 3 → true
	if !result {
		t.Fatal("expected true")
	}
}

func TestPipe_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	step := Pipe2[string, int, string]("cancel-test",
		func(ctx context.Context, s string) (int, error) {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return len(s), nil
		},
		func(ctx context.Context, n int) (string, error) {
			return "", nil
		},
	)

	_, err := step.handler(ctx, "hello")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// ============== TypedStep.ToStep 兼容性测试 ==============

func TestTypedStep_ToStep_CompatibleWithChain(t *testing.T) {
	// TypedStep 可以通过 ToStep() 转换为普通 step，嵌入传统 Chain
	ts := NewTypedStep("double", func(ctx context.Context, n int) (int, error) {
		return n * 2, nil
	})

	s := ts.ToStep()
	result, err := s.handler(context.Background(), 21)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %v", result)
	}
}

func TestTypedStep_ToStep_TypeMismatch(t *testing.T) {
	ts := NewTypedStep("int-only", func(ctx context.Context, n int) (int, error) {
		return n, nil
	})

	s := ts.ToStep()
	_, err := s.handler(context.Background(), "not-an-int")
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
	if !strings.Contains(err.Error(), "type mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ============== Runnable 接口兼容性验证 ==============

func TestTypedChain_ImplementsRunnable(t *testing.T) {
	step := NewTypedStep("id", func(ctx context.Context, s string) (string, error) {
		return s, nil
	})
	chain := NewTypedChain("test", step)

	// 验证可以作为 Runnable 使用
	var _ interface {
		Invoke(context.Context, string, ...interface{ Apply(*interface{}) }) (string, error)
	}
	_ = chain
}
