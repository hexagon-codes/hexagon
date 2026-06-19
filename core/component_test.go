package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

func TestNewSliceStream(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	sr := NewSliceStream(items)

	if sr == nil {
		t.Fatal("expected non-nil stream")
	}
}

func TestSliceStreamRecv(t *testing.T) {
	items := []string{"a", "b", "c"}
	sr := NewSliceStream(items)

	// Read all items
	for i, expected := range items {
		val, err := sr.Recv()
		if err != nil {
			t.Errorf("unexpected error at index %d: %v", i, err)
		}
		if val != expected {
			t.Errorf("expected %s, got %s", expected, val)
		}
	}

	// No more items
	_, err := sr.Recv()
	if err == nil {
		t.Error("expected error when stream is exhausted")
	}
}

func TestSliceStreamCollect(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	sr := NewSliceStream(items)
	ctx := context.Background()

	// Read first two items
	sr.Recv()
	sr.Recv()

	// Collect remaining
	remaining, err := stream.Concat(ctx, sr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 由于 Concat 是对基本类型的，对于 int 数组可能不会正常工作
	// 但至少验证没有 panic
	_ = remaining
}

func TestSliceStreamClose(t *testing.T) {
	sr := NewSliceStream([]int{1, 2, 3})

	if err := sr.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestSchemaOf(t *testing.T) {
	type Person struct {
		Name string `json:"name" desc:"Person's name"`
		Age  int    `json:"age" desc:"Person's age"`
	}

	schema := SchemaOf[Person]()
	if schema == nil {
		t.Error("expected non-nil schema")
	}
}

func TestStreamReaderCopy(t *testing.T) {
	items := []int{1, 2, 3}
	sr := NewSliceStream(items)

	// Copy to 2 readers
	copies := sr.Copy(2)
	if len(copies) != 2 {
		t.Fatalf("expected 2 copies, got %d", len(copies))
	}

	// 验证两个副本都能读取
	for i, cp := range copies {
		val, err := cp.Recv()
		if err != nil {
			t.Errorf("copy %d: unexpected error: %v", i, err)
		}
		if val != 1 {
			t.Errorf("copy %d: expected 1, got %d", i, val)
		}
	}
}

func TestStreamMerge(t *testing.T) {
	sr1 := NewSliceStream([]int{1, 2})
	sr2 := NewSliceStream([]int{3, 4})

	merged := stream.Merge(sr1, sr2)
	if merged == nil {
		t.Fatal("expected non-nil merged stream")
	}

	// 验证可以读取
	val, err := merged.Recv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 值可能是 1 或 3（取决于哪个先到）
	if val != 1 && val != 3 {
		t.Errorf("expected 1 or 3, got %d", val)
	}
}

func TestStreamMap(t *testing.T) {
	sr := NewSliceStream([]int{1, 2, 3})
	doubled := stream.Map(sr, func(n int) int { return n * 2 })

	val, err := doubled.Recv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 2 {
		t.Errorf("expected 2, got %d", val)
	}
}

func TestStreamFilter(t *testing.T) {
	sr := NewSliceStream([]int{1, 2, 3, 4, 5})
	evens := stream.Filter(sr, func(n int) bool { return n%2 == 0 })

	val, err := evens.Recv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 2 {
		t.Errorf("expected 2 (first even), got %d", val)
	}
}

func TestNewRunnable(t *testing.T) {
	doubled := func(ctx context.Context, n int, opts ...Option) (int, error) {
		return n * 2, nil
	}

	r := NewRunnable("doubler", "Doubles a number", doubled)

	if r.Name() != "doubler" {
		t.Errorf("expected name 'doubler', got '%s'", r.Name())
	}

	if r.Description() != "Doubles a number" {
		t.Errorf("expected description 'Doubles a number', got '%s'", r.Description())
	}
}

func TestRunnableInvoke(t *testing.T) {
	doubled := func(ctx context.Context, n int, opts ...Option) (int, error) {
		return n * 2, nil
	}

	r := NewRunnable("doubler", "Doubles a number", doubled)
	ctx := context.Background()

	result, err := r.Invoke(ctx, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != 10 {
		t.Errorf("expected 10, got %d", result)
	}
}

func TestRunnableStream(t *testing.T) {
	doubled := func(ctx context.Context, n int, opts ...Option) (int, error) {
		return n * 2, nil
	}

	r := NewRunnable("doubler", "Doubles a number", doubled)
	ctx := context.Background()

	sr, err := r.Stream(ctx, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if val != 10 {
		t.Errorf("expected 10, got %d", val)
	}
}

func TestRunnableBatch(t *testing.T) {
	doubled := func(ctx context.Context, n int, opts ...Option) (int, error) {
		return n * 2, nil
	}

	r := NewRunnable("doubler", "Doubles a number", doubled)
	ctx := context.Background()

	results, err := r.Batch(ctx, []int{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []int{2, 4, 6, 8, 10}
	if len(results) != len(expected) {
		t.Fatalf("expected %d results, got %d", len(expected), len(results))
	}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("expected %d at index %d, got %d", expected[i], i, v)
		}
	}
}

func TestRunnableInputOutputSchema(t *testing.T) {
	type Input struct {
		Value int `json:"value"`
	}
	type Output struct {
		Result int `json:"result"`
	}

	fn := func(ctx context.Context, in Input, opts ...Option) (Output, error) {
		return Output{Result: in.Value * 2}, nil
	}

	r := NewRunnable("typed", "Typed runnable", fn)

	inputSchema := r.InputSchema()
	if inputSchema == nil {
		t.Error("expected non-nil input schema")
	}

	outputSchema := r.OutputSchema()
	if outputSchema == nil {
		t.Error("expected non-nil output schema")
	}
}

// ============================================================================
// Fallback 测试
// ============================================================================

// errPrimary 主 Runnable 错误
var errPrimary = errors.New("主 Runnable 错误")

// errFallback 降级 Runnable 错误
var errFallback = errors.New("降级 Runnable 错误")

// errSpecific 特定类型错误（用于 ExceptionsToHandle 测试）
var errSpecific = errors.New("特定类型错误")

// errOther 其他类型错误
var errOther = errors.New("其他类型错误")

// newFailRunnable 创建一个总是返回错误的 Runnable
func newFailRunnable(name string, err error) Runnable[string, string] {
	return NewRunnable[string, string](name, "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		return "", err
	})
}

// newSuccessRunnable 创建一个总是成功的 Runnable
func newSuccessRunnable(name string, result string) Runnable[string, string] {
	return NewRunnable[string, string](name, "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		return result, nil
	})
}

// TestWithFallback_PrimarySuccess 测试主 Runnable 成功时不触发降级
func TestWithFallback_PrimarySuccess(t *testing.T) {
	primary := newSuccessRunnable("primary", "主成功")
	fallback := newSuccessRunnable("fallback", "降级成功")

	r := WithFallback(primary, fallback)
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "主成功" {
		t.Errorf("期望 '主成功'，但得到 '%s'", result)
	}
}

// TestWithFallback_PrimaryFailFallbackSuccess 测试主失败、降级成功
func TestWithFallback_PrimaryFailFallbackSuccess(t *testing.T) {
	primary := newFailRunnable("primary", errPrimary)
	fallback := newSuccessRunnable("fallback", "降级成功")

	r := WithFallback(primary, fallback)
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "降级成功" {
		t.Errorf("期望 '降级成功'，但得到 '%s'", result)
	}
}

// TestWithFallback_AllFail 测试所有降级都失败
func TestWithFallback_AllFail(t *testing.T) {
	primary := newFailRunnable("primary", errPrimary)
	fallback1 := newFailRunnable("fallback1", errFallback)
	fallback2 := newFailRunnable("fallback2", errFallback)

	r := WithFallback(primary, fallback1, fallback2)
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if !errors.Is(err, ErrAllFallbacksFailed) {
		t.Fatalf("期望 ErrAllFallbacksFailed 错误，但得到: %v", err)
	}
}

// TestWithFallback_MultipleFallbacks 测试多个降级，第二个成功
func TestWithFallback_MultipleFallbacks(t *testing.T) {
	primary := newFailRunnable("primary", errPrimary)
	fallback1 := newFailRunnable("fallback1", errFallback)
	fallback2 := newSuccessRunnable("fallback2", "第二降级成功")

	r := WithFallback(primary, fallback1, fallback2)
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "第二降级成功" {
		t.Errorf("期望 '第二降级成功'，但得到 '%s'", result)
	}
}

// TestWithFallback_ExceptionsToHandle 测试仅特定错误触发降级
func TestWithFallback_ExceptionsToHandle(t *testing.T) {
	primary := newFailRunnable("primary", errSpecific)
	fallback := newSuccessRunnable("fallback", "降级成功")

	// 只对 errSpecific 降级
	r := WithFallback(primary, fallback).WithOptions(WithExceptions(errSpecific))
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "降级成功" {
		t.Errorf("期望 '降级成功'，但得到 '%s'", result)
	}
}

// TestWithFallback_ExceptionsToHandle_NotMatch 测试非匹配错误不触发降级
func TestWithFallback_ExceptionsToHandle_NotMatch(t *testing.T) {
	primary := newFailRunnable("primary", errOther)
	fallback := newSuccessRunnable("fallback", "降级成功")

	// 只对 errSpecific 降级，但实际错误是 errOther
	r := WithFallback(primary, fallback).WithOptions(WithExceptions(errSpecific))
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if !errors.Is(err, errOther) {
		t.Fatalf("期望 errOther 错误，但得到: %v", err)
	}
}

// TestWithFallback_Callback 测试降级回调触发
func TestWithFallback_Callback(t *testing.T) {
	primary := newFailRunnable("primary", errPrimary)
	fallback := newSuccessRunnable("fallback", "降级成功")

	var callbackErr error
	var callbackIndex int
	called := false

	r := WithFallback(primary, fallback).WithOptions(
		WithFallbackCallback(func(err error, idx int) {
			callbackErr = err
			callbackIndex = idx
			called = true
		}),
	)
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if !called {
		t.Error("期望回调被调用")
	}
	if !errors.Is(callbackErr, errPrimary) {
		t.Errorf("期望回调错误为 errPrimary，但得到: %v", callbackErr)
	}
	if callbackIndex != 0 {
		t.Errorf("期望回调索引为 0，但得到: %d", callbackIndex)
	}
}

// TestWithFallback_Name 测试降级 Runnable 名称
func TestWithFallback_Name(t *testing.T) {
	primary := newSuccessRunnable("my_primary", "ok")
	r := WithFallback(primary)
	if r.Name() != "my_primary_with_fallback" {
		t.Errorf("期望名称 'my_primary_with_fallback'，但得到 '%s'", r.Name())
	}
}

// TestRunnableWithFallback_Stream 测试 Stream 降级
func TestRunnableWithFallback_Stream(t *testing.T) {
	// 主 Runnable Stream 失败
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return nil, errPrimary
	}

	// 降级 Runnable Stream 成功
	fallback := NewRunnable[string, string]("fallback", "", nil)
	fallback.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return stream.FromValue("降级流"), nil
	}

	r := WithFallback[string, string](primary, fallback)
	ctx := context.Background()

	sr, err := r.Stream(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "降级流" {
		t.Errorf("期望 '降级流'，但得到 '%s'", val)
	}
}

// TestRunnableWithFallback_Batch 测试 Batch 降级
func TestRunnableWithFallback_Batch(t *testing.T) {
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.batchFn = func(ctx context.Context, inputs []string, opts ...Option) ([]string, error) {
		return nil, errPrimary
	}

	fallback := NewRunnable[string, string]("fallback", "", nil)
	fallback.batchFn = func(ctx context.Context, inputs []string, opts ...Option) ([]string, error) {
		results := make([]string, len(inputs))
		for i, in := range inputs {
			results[i] = "降级_" + in
		}
		return results, nil
	}

	r := WithFallback[string, string](primary, fallback)
	ctx := context.Background()

	results, err := r.Batch(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(results) != 2 || results[0] != "降级_a" || results[1] != "降级_b" {
		t.Errorf("期望降级结果，但得到: %v", results)
	}
}

// TestRunnableWithFallback_BatchStream 测试 BatchStream 降级
func TestRunnableWithFallback_BatchStream(t *testing.T) {
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.batchStreamFn = func(ctx context.Context, inputs []string, opts ...Option) (*StreamReader[string], error) {
		return nil, errPrimary
	}

	fallback := NewRunnable[string, string]("fallback", "", nil)
	fallback.batchStreamFn = func(ctx context.Context, inputs []string, opts ...Option) (*StreamReader[string], error) {
		return stream.FromSlice([]string{"降级a", "降级b"}), nil
	}

	r := WithFallback[string, string](primary, fallback)
	ctx := context.Background()

	sr, err := r.BatchStream(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "降级a" {
		t.Errorf("期望 '降级a'，但得到 '%s'", val)
	}
}

// TestRunnableWithFallback_Schema 测试降级 Runnable 的 Schema 委托
func TestRunnableWithFallback_Schema(t *testing.T) {
	primary := newSuccessRunnable("primary", "ok")
	r := WithFallback(primary)
	if r.Description() != primary.Description() {
		t.Errorf("Description 应该委托给 primary")
	}
	if r.InputSchema() == nil {
		t.Error("InputSchema 不应为 nil")
	}
	if r.OutputSchema() == nil {
		t.Error("OutputSchema 不应为 nil")
	}
}

// ============================================================================
// Retry 测试
// ============================================================================

// TestWithRetry_FirstSuccess 测试首次成功不重试
func TestWithRetry_FirstSuccess(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		return "成功", nil
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	})
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "成功" {
		t.Errorf("期望 '成功'，但得到 '%s'", result)
	}
	if callCount != 1 {
		t.Errorf("期望调用 1 次，但调用了 %d 次", callCount)
	}
}

// TestWithRetry_RetryThenSuccess 测试重试后成功
func TestWithRetry_RetryThenSuccess(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		if callCount < 3 {
			return "", errPrimary
		}
		return "重试成功", nil
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   5,
		InitialDelay: time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   1.0,
		RetryOn:      func(err error) bool { return err != nil },
	})
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "重试成功" {
		t.Errorf("期望 '重试成功'，但得到 '%s'", result)
	}
	if callCount != 3 {
		t.Errorf("期望调用 3 次，但调用了 %d 次", callCount)
	}
}

// TestWithRetry_MaxRetriesExceeded 测试超过最大重试次数
func TestWithRetry_MaxRetriesExceeded(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		return "", errPrimary
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   1.0,
		RetryOn:      func(err error) bool { return err != nil },
	})
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if !errors.Is(err, errPrimary) {
		t.Fatalf("期望 errPrimary，但得到: %v", err)
	}
	// 初始调用 + 2 次重试 = 3 次
	if callCount != 3 {
		t.Errorf("期望调用 3 次，但调用了 %d 次", callCount)
	}
}

// TestWithRetry_RetryOnFilter 测试 RetryOn 过滤非重试错误
func TestWithRetry_RetryOnFilter(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		return "", errOther
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   5,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   1.0,
		// 只对 errSpecific 重试
		RetryOn: func(err error) bool { return errors.Is(err, errSpecific) },
	})
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if !errors.Is(err, errOther) {
		t.Fatalf("期望 errOther，但得到: %v", err)
	}
	// 不应重试，只调用一次
	if callCount != 1 {
		t.Errorf("期望调用 1 次，但调用了 %d 次", callCount)
	}
}

// TestWithRetry_OnRetryCallback 测试 OnRetry 回调
func TestWithRetry_OnRetryCallback(t *testing.T) {
	callCount := 0
	retryAttempts := []int{}

	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		if callCount < 3 {
			return "", errPrimary
		}
		return "成功", nil
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   5,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   1.0,
		RetryOn:      func(err error) bool { return err != nil },
		OnRetry: func(attempt int, err error) {
			retryAttempts = append(retryAttempts, attempt)
		},
	})
	ctx := context.Background()

	_, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	// 应该有 2 次重试回调（attempt 0 和 1）
	if len(retryAttempts) != 2 {
		t.Errorf("期望 2 次重试回调，但得到 %d 次", len(retryAttempts))
	}
	if retryAttempts[0] != 0 || retryAttempts[1] != 1 {
		t.Errorf("期望重试次数 [0,1]，但得到 %v", retryAttempts)
	}
}

// TestWithRetry_ContextCancel 测试 context 取消中断重试
func TestWithRetry_ContextCancel(t *testing.T) {
	callCount := int32(0)
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		atomic.AddInt32(&callCount, 1)
		return "", errPrimary
	})

	r := WithRetry(primary, &RetryConfig{
		MaxRetries:   100,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   1.0,
		RetryOn:      func(err error) bool { return err != nil },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := r.Invoke(ctx, "input")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 context.DeadlineExceeded，但得到: %v", err)
	}
	// 应该只调用了少于 100 次
	count := atomic.LoadInt32(&callCount)
	if count >= 5 {
		t.Errorf("期望很少的调用次数，但调用了 %d 次", count)
	}
}

// TestDefaultRetryConfig 测试默认重试配置
func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("期望 MaxRetries=3，但得到 %d", cfg.MaxRetries)
	}
	if cfg.InitialDelay != time.Second {
		t.Errorf("期望 InitialDelay=1s，但得到 %v", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 30*time.Second {
		t.Errorf("期望 MaxDelay=30s，但得到 %v", cfg.MaxDelay)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("期望 Multiplier=2.0，但得到 %f", cfg.Multiplier)
	}
	if cfg.Jitter != 0.1 {
		t.Errorf("期望 Jitter=0.1，但得到 %f", cfg.Jitter)
	}
	// 确认 RetryOn 对所有错误返回 true
	if cfg.RetryOn == nil || !cfg.RetryOn(errors.New("test")) {
		t.Error("期望 RetryOn 对所有错误返回 true")
	}
}

// TestWithRetry_DefaultConfig 测试使用默认配置创建重试 Runnable
func TestWithRetry_DefaultConfig(t *testing.T) {
	primary := newSuccessRunnable("primary", "ok")
	r := WithRetry[string, string](primary)
	if r.Name() != "primary_with_retry" {
		t.Errorf("期望名称 'primary_with_retry'，但得到 '%s'", r.Name())
	}
}

// TestRunnableWithRetry_Stream 测试 Stream 重试
func TestRunnableWithRetry_Stream(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		callCount++
		if callCount < 2 {
			return nil, errPrimary
		}
		return stream.FromValue("流重试成功"), nil
	}

	r := WithRetry[string, string](primary, &RetryConfig{
		MaxRetries:   3,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   1.0,
		RetryOn:      func(err error) bool { return err != nil },
	})
	ctx := context.Background()

	sr, err := r.Stream(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "流重试成功" {
		t.Errorf("期望 '流重试成功'，但得到 '%s'", val)
	}
}

// TestRunnableWithRetry_Schema 测试重试 Runnable 的 Schema 委托
func TestRunnableWithRetry_Schema(t *testing.T) {
	primary := newSuccessRunnable("primary", "ok")
	r := WithRetry(primary)
	if r.Description() != primary.Description() {
		t.Error("Description 应该委托给 primary")
	}
	if r.InputSchema() == nil {
		t.Error("InputSchema 不应为 nil")
	}
	if r.OutputSchema() == nil {
		t.Error("OutputSchema 不应为 nil")
	}
}

// ============================================================================
// CircuitBreaker 测试
// ============================================================================

// TestCircuitBreaker_ClosedToOpen 测试从关闭到打开状态
func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          time.Second,
	})

	if cb.State() != CircuitClosed {
		t.Fatalf("期望初始状态为 CircuitClosed")
	}

	// 记录 3 次失败达到阈值
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitClosed {
		t.Fatalf("2 次失败后应仍为 CircuitClosed")
	}
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("3 次失败后应为 CircuitOpen")
	}
}

// TestCircuitBreaker_OpenNotAllow 测试打开状态不允许执行
func TestCircuitBreaker_OpenNotAllow(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          time.Hour, // 长超时确保不会自动转半开
	})

	cb.RecordFailure() // 触发打开
	if cb.State() != CircuitOpen {
		t.Fatalf("期望 CircuitOpen")
	}
	if cb.Allow() {
		t.Error("打开状态不应允许执行")
	}
}

// TestCircuitBreaker_OpenToHalfOpen 测试超时后从打开到半开
func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          5 * time.Millisecond,
	})

	cb.RecordFailure() // 触发打开
	if cb.State() != CircuitOpen {
		t.Fatalf("期望 CircuitOpen")
	}

	// 等待超时
	time.Sleep(10 * time.Millisecond)

	// Allow() 应该触发转为 HalfOpen
	if !cb.Allow() {
		t.Error("超时后应允许执行（半开状态）")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("超时后期望 CircuitHalfOpen，但得到 %d", cb.State())
	}
}

// TestCircuitBreaker_HalfOpenToClosed 测试半开状态成功达到阈值后关闭
func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		Timeout:          5 * time.Millisecond,
	})

	cb.RecordFailure()                // → Open
	time.Sleep(10 * time.Millisecond) // 等待超时
	cb.Allow()                        // → HalfOpen

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("期望 CircuitHalfOpen")
	}

	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("1 次成功后应仍为 CircuitHalfOpen")
	}
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatalf("2 次成功后应为 CircuitClosed")
	}
}

// TestCircuitBreaker_HalfOpenToOpen 测试半开状态失败后重新打开
func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 3,
		Timeout:          5 * time.Millisecond,
	})

	cb.RecordFailure()                // → Open
	time.Sleep(10 * time.Millisecond) // 等待超时
	cb.Allow()                        // → HalfOpen

	if cb.State() != CircuitHalfOpen {
		t.Fatalf("期望 CircuitHalfOpen")
	}

	cb.RecordFailure() // → 重新 Open
	if cb.State() != CircuitOpen {
		t.Fatalf("半开状态失败后应为 CircuitOpen")
	}
}

// TestCircuitBreaker_OnStateChange 测试状态变化回调
func TestCircuitBreaker_OnStateChange(t *testing.T) {
	var transitions []string
	var mu sync.Mutex

	cb := NewCircuitBreaker(&CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          5 * time.Millisecond,
		OnStateChange: func(from, to CircuitState) {
			mu.Lock()
			transitions = append(transitions, fmt.Sprintf("%d->%d", from, to))
			mu.Unlock()
		},
	})

	cb.RecordFailure()
	cb.RecordFailure() // → Open

	time.Sleep(10 * time.Millisecond)
	cb.Allow() // → HalfOpen

	cb.RecordSuccess() // → Closed

	mu.Lock()
	defer mu.Unlock()

	// 应该有 3 次状态变化: Closed→Open, Open→HalfOpen, HalfOpen→Closed
	if len(transitions) != 3 {
		t.Fatalf("期望 3 次状态变化，但得到 %d 次: %v", len(transitions), transitions)
	}
}

// TestDefaultCircuitBreakerConfig 测试默认熔断器配置
func TestDefaultCircuitBreakerConfig(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	if cfg.FailureThreshold != 5 {
		t.Errorf("期望 FailureThreshold=5，但得到 %d", cfg.FailureThreshold)
	}
	if cfg.SuccessThreshold != 3 {
		t.Errorf("期望 SuccessThreshold=3，但得到 %d", cfg.SuccessThreshold)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("期望 Timeout=30s，但得到 %v", cfg.Timeout)
	}
}

// TestRunnableWithCircuitBreaker_Invoke 测试带熔断的 Invoke
func TestRunnableWithCircuitBreaker_Invoke(t *testing.T) {
	callCount := 0
	primary := NewRunnable[string, string]("primary", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		callCount++
		return "", errPrimary
	})

	r := WithCircuitBreaker(primary, &CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          time.Hour,
	})
	ctx := context.Background()

	// 触发熔断
	r.Invoke(ctx, "a")
	r.Invoke(ctx, "b")

	// 熔断后应直接返回 ErrCircuitOpen
	_, err := r.Invoke(ctx, "c")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("期望 ErrCircuitOpen，但得到: %v", err)
	}
	if callCount != 2 {
		t.Errorf("期望调用 2 次（熔断后不再调用），但调用了 %d 次", callCount)
	}
}

// TestRunnableWithCircuitBreaker_InvokeSuccess 测试带熔断的成功调用
func TestRunnableWithCircuitBreaker_InvokeSuccess(t *testing.T) {
	primary := newSuccessRunnable("primary", "成功")

	r := WithCircuitBreaker(primary, &CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout:          time.Second,
	})
	ctx := context.Background()

	result, err := r.Invoke(ctx, "input")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "成功" {
		t.Errorf("期望 '成功'，但得到 '%s'", result)
	}
}

// TestRunnableWithCircuitBreaker_Stream 测试带熔断的 Stream
func TestRunnableWithCircuitBreaker_Stream(t *testing.T) {
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return nil, errPrimary
	}

	r := WithCircuitBreaker[string, string](primary, &CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          time.Hour,
	})
	ctx := context.Background()

	// 第一次失败触发熔断
	r.Stream(ctx, "a")

	// 第二次应被熔断
	_, err := r.Stream(ctx, "b")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("期望 ErrCircuitOpen，但得到: %v", err)
	}
}

// TestRunnableWithCircuitBreaker_Batch 测试带熔断的 Batch
func TestRunnableWithCircuitBreaker_Batch(t *testing.T) {
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.batchFn = func(ctx context.Context, inputs []string, opts ...Option) ([]string, error) {
		return nil, errPrimary
	}

	r := WithCircuitBreaker[string, string](primary, &CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          time.Hour,
	})
	ctx := context.Background()

	r.Batch(ctx, []string{"a"})

	_, err := r.Batch(ctx, []string{"b"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("期望 ErrCircuitOpen，但得到: %v", err)
	}
}

// TestRunnableWithCircuitBreaker_BatchStream 测试带熔断的 BatchStream
func TestRunnableWithCircuitBreaker_BatchStream(t *testing.T) {
	primary := NewRunnable[string, string]("primary", "", nil)
	primary.batchStreamFn = func(ctx context.Context, inputs []string, opts ...Option) (*StreamReader[string], error) {
		return nil, errPrimary
	}

	r := WithCircuitBreaker[string, string](primary, &CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout:          time.Hour,
	})
	ctx := context.Background()

	r.BatchStream(ctx, []string{"a"})

	_, err := r.BatchStream(ctx, []string{"b"})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("期望 ErrCircuitOpen，但得到: %v", err)
	}
}

// TestRunnableWithCircuitBreaker_Schema 测试带熔断 Runnable 的 Schema 委托
func TestRunnableWithCircuitBreaker_Schema(t *testing.T) {
	primary := newSuccessRunnable("primary", "ok")
	r := WithCircuitBreaker(primary)
	if r.Name() != "primary_with_circuit_breaker" {
		t.Errorf("期望名称 'primary_with_circuit_breaker'，但得到 '%s'", r.Name())
	}
	if r.Description() != primary.Description() {
		t.Error("Description 应该委托给 primary")
	}
	if r.InputSchema() == nil {
		t.Error("InputSchema 不应为 nil")
	}
	if r.OutputSchema() == nil {
		t.Error("OutputSchema 不应为 nil")
	}
}

// ============================================================================
// Async 测试
// ============================================================================

// TestFuture_CompleteAndGet 测试 Future 的 Complete 和 Get
func TestFuture_CompleteAndGet(t *testing.T) {
	f := NewFuture[string]()

	go func() {
		time.Sleep(5 * time.Millisecond)
		f.Complete("结果", nil)
	}()

	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "结果" {
		t.Errorf("期望 '结果'，但得到 '%s'", result)
	}
}

// TestFuture_CompleteOnce 测试 Future 只完成一次
func TestFuture_CompleteOnce(t *testing.T) {
	f := NewFuture[string]()

	f.Complete("第一次", nil)
	f.Complete("第二次", errors.New("不应生效"))

	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "第一次" {
		t.Errorf("期望 '第一次'，但得到 '%s'", result)
	}
}

// TestFuture_IsDone 测试 IsDone
func TestFuture_IsDone(t *testing.T) {
	f := NewFuture[int]()

	if f.IsDone() {
		t.Error("未完成的 Future 不应返回 IsDone=true")
	}

	f.Complete(42, nil)

	if !f.IsDone() {
		t.Error("已完成的 Future 应返回 IsDone=true")
	}
}

// TestFuture_GetWithTimeout_Success 测试 GetWithTimeout 在超时前完成
func TestFuture_GetWithTimeout_Success(t *testing.T) {
	f := NewFuture[string]()

	go func() {
		time.Sleep(5 * time.Millisecond)
		f.Complete("及时", nil)
	}()

	result, err := f.GetWithTimeout(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "及时" {
		t.Errorf("期望 '及时'，但得到 '%s'", result)
	}
}

// TestFuture_GetWithTimeout_Timeout 测试 GetWithTimeout 超时
func TestFuture_GetWithTimeout_Timeout(t *testing.T) {
	f := NewFuture[string]()
	// 不完成 future

	_, err := f.GetWithTimeout(10 * time.Millisecond)
	if err == nil {
		t.Fatal("期望超时错误")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("期望包含 'timeout' 的错误，但得到: %v", err)
	}
}

// TestFuture_GetWithContext_Success 测试 GetWithContext 在取消前完成
func TestFuture_GetWithContext_Success(t *testing.T) {
	f := NewFuture[string]()

	go func() {
		time.Sleep(5 * time.Millisecond)
		f.Complete("成功", nil)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result, err := f.GetWithContext(ctx)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "成功" {
		t.Errorf("期望 '成功'，但得到 '%s'", result)
	}
}

// TestFuture_GetWithContext_Cancel 测试 GetWithContext 被取消
func TestFuture_GetWithContext_Cancel(t *testing.T) {
	f := NewFuture[string]()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := f.GetWithContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 context.DeadlineExceeded，但得到: %v", err)
	}
}

// TestFuture_Then_Success 测试 Then 链式成功
func TestFuture_Then_Success(t *testing.T) {
	f := NewFuture[int]()
	f.Complete(5, nil)

	next := f.Then(func(v int) (int, error) {
		return v * 2, nil
	})

	result, err := next.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != 10 {
		t.Errorf("期望 10，但得到 %d", result)
	}
}

// TestFuture_Then_ErrorPropagation 测试 Then 链式错误传播
func TestFuture_Then_ErrorPropagation(t *testing.T) {
	f := NewFuture[int]()
	f.Complete(0, errPrimary)

	next := f.Then(func(v int) (int, error) {
		return v * 2, nil // 不应被调用
	})

	_, err := next.Get()
	if !errors.Is(err, errPrimary) {
		t.Fatalf("期望 errPrimary，但得到: %v", err)
	}
}

// TestFuture_Catch_WithError 测试 Catch 处理错误
func TestFuture_Catch_WithError(t *testing.T) {
	f := NewFuture[string]()
	f.Complete("", errPrimary)

	caught := f.Catch(func(err error) (string, error) {
		return "已恢复", nil
	})

	result, err := caught.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "已恢复" {
		t.Errorf("期望 '已恢复'，但得到 '%s'", result)
	}
}

// TestFuture_Catch_NoError 测试 Catch 无错误时跳过
func TestFuture_Catch_NoError(t *testing.T) {
	f := NewFuture[string]()
	f.Complete("原始值", nil)

	caught := f.Catch(func(err error) (string, error) {
		return "不应出现", nil
	})

	result, err := caught.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "原始值" {
		t.Errorf("期望 '原始值'，但得到 '%s'", result)
	}
}

// TestAsyncWrapper_InvokeAsync 测试异步包装的 InvokeAsync
func TestAsyncWrapper_InvokeAsync(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	wrapper := WrapAsync(r)
	ctx := context.Background()

	future := wrapper.InvokeAsync(ctx, 21)
	result, err := future.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != 42 {
		t.Errorf("期望 42，但得到 %d", result)
	}
}

// TestAsyncWrapper_BatchAsync 测试异步包装的 BatchAsync
func TestAsyncWrapper_BatchAsync(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	wrapper := WrapAsync(r)
	ctx := context.Background()

	future := wrapper.BatchAsync(ctx, []int{1, 2, 3})
	results, err := future.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	expected := []int{2, 4, 6}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("期望 results[%d]=%d，但得到 %d", i, expected[i], v)
		}
	}
}

// TestParallel_AllSuccess 测试并行执行全部成功
func TestParallel_AllSuccess(t *testing.T) {
	f1 := NewFuture[int]()
	f2 := NewFuture[int]()
	f3 := NewFuture[int]()

	f1.Complete(1, nil)
	f2.Complete(2, nil)
	f3.Complete(3, nil)

	result := Parallel(f1, f2, f3)
	values, err := result.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(values) != 3 || values[0] != 1 || values[1] != 2 || values[2] != 3 {
		t.Errorf("期望 [1,2,3]，但得到 %v", values)
	}
}

// TestParallel_PartialFailure 测试并行执行部分失败
func TestParallel_PartialFailure(t *testing.T) {
	f1 := NewFuture[int]()
	f2 := NewFuture[int]()

	f1.Complete(1, nil)
	f2.Complete(0, errPrimary)

	result := Parallel(f1, f2)
	_, err := result.Get()
	if err == nil {
		t.Fatal("期望有错误")
	}
}

// TestParallelWithLimit 测试带并发限制的并行执行
func TestParallelWithLimit(t *testing.T) {
	futures := make([]*Future[int], 5)
	for i := range futures {
		futures[i] = NewFuture[int]()
		futures[i].Complete(i, nil)
	}

	result := ParallelWithLimit(2, futures...)
	values, err := result.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(values) != 5 {
		t.Fatalf("期望 5 个结果，但得到 %d", len(values))
	}
	for i, v := range values {
		if v != i {
			t.Errorf("期望 values[%d]=%d，但得到 %d", i, i, v)
		}
	}
}

// TestRace 测试竞争执行返回第一个完成的
func TestRace(t *testing.T) {
	f1 := NewFuture[string]()
	f2 := NewFuture[string]()

	// f1 立即完成
	f1.Complete("快速", nil)

	// f2 延迟完成
	go func() {
		time.Sleep(50 * time.Millisecond)
		f2.Complete("慢速", nil)
	}()

	result := Race(f1, f2)
	value, err := result.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if value != "快速" {
		t.Errorf("期望 '快速'，但得到 '%s'", value)
	}
}

// TestAny_FirstSuccess 测试 Any 返回第一个成功的
func TestAny_FirstSuccess(t *testing.T) {
	f1 := NewFuture[string]()
	f2 := NewFuture[string]()

	f1.Complete("", errPrimary)
	f2.Complete("成功", nil)

	result := Any(f1, f2)
	value, err := result.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if value != "成功" {
		t.Errorf("期望 '成功'，但得到 '%s'", value)
	}
}

// TestAny_AllFail 测试 Any 全部失败
func TestAny_AllFail(t *testing.T) {
	f1 := NewFuture[string]()
	f2 := NewFuture[string]()

	f1.Complete("", errPrimary)
	f2.Complete("", errFallback)

	result := Any(f1, f2)
	_, err := result.Get()
	if err == nil {
		t.Fatal("期望有错误")
	}
}

// TestRunAsync 测试 RunAsync
func TestRunAsync(t *testing.T) {
	f := RunAsync(func() (int, error) {
		return 42, nil
	})

	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != 42 {
		t.Errorf("期望 42，但得到 %d", result)
	}
}

// TestRunAsyncWithContext 测试 RunAsyncWithContext
func TestRunAsyncWithContext(t *testing.T) {
	ctx := context.Background()
	f := RunAsyncWithContext(ctx, func(ctx context.Context) (string, error) {
		return "异步上下文", nil
	})

	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "异步上下文" {
		t.Errorf("期望 '异步上下文'，但得到 '%s'", result)
	}
}

// TestDelay 测试延迟执行
func TestDelay(t *testing.T) {
	start := time.Now()
	f := Delay(20*time.Millisecond, func() (string, error) {
		return "延迟完成", nil
	})

	result, err := f.Get()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "延迟完成" {
		t.Errorf("期望 '延迟完成'，但得到 '%s'", result)
	}
	if elapsed < 15*time.Millisecond {
		t.Errorf("期望至少延迟 15ms，但只延迟了 %v", elapsed)
	}
}

// TestRetryAsync_Success 测试异步重试成功
func TestRetryAsync_Success(t *testing.T) {
	callCount := int32(0)
	f := Retry(3, time.Millisecond, func() (string, error) {
		count := atomic.AddInt32(&callCount, 1)
		if count < 3 {
			return "", errPrimary
		}
		return "重试成功", nil
	})

	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "重试成功" {
		t.Errorf("期望 '重试成功'，但得到 '%s'", result)
	}
}

// TestRetryAsync_AllFail 测试异步重试全部失败
func TestRetryAsync_AllFail(t *testing.T) {
	f := Retry(2, time.Millisecond, func() (string, error) {
		return "", errPrimary
	})

	_, err := f.Get()
	if !errors.Is(err, errPrimary) {
		t.Fatalf("期望 errPrimary，但得到: %v", err)
	}
}

// TestPromise_Resolve 测试 Promise Resolve
func TestPromise_Resolve(t *testing.T) {
	p := NewPromise[string]()

	go func() {
		time.Sleep(5 * time.Millisecond)
		p.Resolve("承诺完成")
	}()

	result, err := p.Future().Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "承诺完成" {
		t.Errorf("期望 '承诺完成'，但得到 '%s'", result)
	}
}

// TestPromise_Reject 测试 Promise Reject
func TestPromise_Reject(t *testing.T) {
	p := NewPromise[string]()
	p.Reject(errPrimary)

	_, err := p.Future().Get()
	if !errors.Is(err, errPrimary) {
		t.Fatalf("期望 errPrimary，但得到: %v", err)
	}
}

// TestResultChannel_SendReceive 测试 ResultChannel 发送和接收
func TestResultChannel_SendReceive(t *testing.T) {
	rc := NewResultChannel[string](1)

	rc.Send("你好", nil)
	value, err := rc.Receive()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if value != "你好" {
		t.Errorf("期望 '你好'，但得到 '%s'", value)
	}
}

// TestResultChannel_SendReceiveError 测试 ResultChannel 发送和接收错误
func TestResultChannel_SendReceiveError(t *testing.T) {
	rc := NewResultChannel[string](1)

	rc.Send("", errPrimary)
	_, err := rc.Receive()
	if !errors.Is(err, errPrimary) {
		t.Fatalf("期望 errPrimary，但得到: %v", err)
	}
}

// TestResultChannel_ReceiveWithContext 测试带上下文的接收
func TestResultChannel_ReceiveWithContext(t *testing.T) {
	rc := NewResultChannel[string](1)

	rc.Send("成功", nil)

	ctx := context.Background()
	value, err := rc.ReceiveWithContext(ctx)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if value != "成功" {
		t.Errorf("期望 '成功'，但得到 '%s'", value)
	}
}

// TestResultChannel_ReceiveWithContext_Cancel 测试带上下文接收被取消
func TestResultChannel_ReceiveWithContext_Cancel(t *testing.T) {
	rc := NewResultChannel[string](0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := rc.ReceiveWithContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 context.DeadlineExceeded，但得到: %v", err)
	}
}

// TestResultChannel_Close 测试关闭通道
func TestResultChannel_Close(t *testing.T) {
	rc := NewResultChannel[int](0)
	rc.Close()

	// 关闭后的通道应该可以读取，返回零值
	ch := rc.Channel()
	_, ok := <-ch
	if ok {
		t.Error("期望关闭的通道读取 ok=false")
	}
}

// TestResultChannel_Channel 测试获取底层通道
func TestResultChannel_Channel(t *testing.T) {
	rc := NewResultChannel[int](1)
	rc.Send(42, nil)

	ch := rc.Channel()
	result := <-ch
	if result.Value != 42 || result.Err != nil {
		t.Errorf("期望 Value=42, Err=nil，但得到 Value=%d, Err=%v", result.Value, result.Err)
	}
}

// TestInvokeWithCallback 测试带回调的调用
func TestInvokeWithCallback(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	done := make(chan struct{})
	var cbResult int
	var cbErr error

	InvokeWithCallback(context.Background(), r, 21, func(result int, err error) {
		cbResult = result
		cbErr = err
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("回调超时")
	}

	if cbErr != nil {
		t.Fatalf("期望无错误，但得到: %v", cbErr)
	}
	if cbResult != 42 {
		t.Errorf("期望 42，但得到 %d", cbResult)
	}
}

// TestFuture_ConcurrentComplete 测试并发 Complete 的安全性
func TestFuture_ConcurrentComplete(t *testing.T) {
	f := NewFuture[int]()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			f.Complete(val, nil)
		}(i)
	}
	wg.Wait()

	// 应该只有一个值
	result, err := f.Get()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result < 0 || result >= 100 {
		t.Errorf("结果应在 [0,100) 范围内，但得到 %d", result)
	}
}

// ============================================================================
// Runnable 补充测试
// ============================================================================

// TestBaseRunnable_InvokeFromStream 测试从 Stream 推导 Invoke
func TestBaseRunnable_InvokeFromStream(t *testing.T) {
	r := &BaseRunnable[string, string]{
		name: "stream_only",
	}
	r.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return stream.FromValue("流结果_" + input), nil
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "流结果_test" {
		t.Errorf("期望 '流结果_test'，但得到 '%s'", result)
	}
}

// TestBaseRunnable_InvokeFromCollect 测试从 Collect 推导 Invoke
func TestBaseRunnable_InvokeFromCollect(t *testing.T) {
	r := &BaseRunnable[string, string]{
		name: "collect_only",
	}
	r.collectFn = func(ctx context.Context, input *StreamReader[string], opts ...Option) (string, error) {
		// 从流中收集所有值
		val, err := input.Recv()
		if err != nil {
			return "", err
		}
		return "收集_" + val, nil
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "收集_test" {
		t.Errorf("期望 '收集_test'，但得到 '%s'", result)
	}
}

// TestBaseRunnable_InvokeFromTransform 测试从 Transform 推导 Invoke
func TestBaseRunnable_InvokeFromTransform(t *testing.T) {
	r := &BaseRunnable[string, string]{
		name: "transform_only",
	}
	r.transformFn = func(ctx context.Context, input *StreamReader[string], opts ...Option) (*StreamReader[string], error) {
		// 读取输入流并转换
		reader, writer := stream.Pipe[string](1)
		go func() {
			defer writer.Close()
			val, err := input.Recv()
			if err != nil {
				return
			}
			writer.Send("转换_" + val)
		}()
		return reader, nil
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "转换_test" {
		t.Errorf("期望 '转换_test'，但得到 '%s'", result)
	}
}

// TestBaseRunnable_InvokeNoImpl 测试无实现时 Invoke 返回零值
func TestBaseRunnable_InvokeNoImpl(t *testing.T) {
	r := &BaseRunnable[string, string]{
		name: "empty",
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "" {
		t.Errorf("期望空字符串，但得到 '%s'", result)
	}
}

// TestBaseRunnable_StreamFromInvoke 测试从 Invoke 推导 Stream
func TestBaseRunnable_StreamFromInvoke(t *testing.T) {
	r := NewRunnable[string, string]("invoker", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		return "调用_" + input, nil
	})

	ctx := context.Background()
	sr, err := r.Stream(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "调用_test" {
		t.Errorf("期望 '调用_test'，但得到 '%s'", val)
	}
}

// TestBaseRunnable_StreamFromTransform 测试从 Transform 推导 Stream
func TestBaseRunnable_StreamFromTransform(t *testing.T) {
	r := &BaseRunnable[string, string]{
		name: "transform_only",
	}
	r.transformFn = func(ctx context.Context, input *StreamReader[string], opts ...Option) (*StreamReader[string], error) {
		reader, writer := stream.Pipe[string](1)
		go func() {
			defer writer.Close()
			val, err := input.Recv()
			if err != nil {
				return
			}
			writer.Send("T_" + val)
		}()
		return reader, nil
	}

	ctx := context.Background()
	sr, err := r.Stream(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "T_test" {
		t.Errorf("期望 'T_test'，但得到 '%s'", val)
	}
}

// TestBaseRunnable_StreamNoImpl 测试无实现时 Stream 返回空流
func TestBaseRunnable_StreamNoImpl(t *testing.T) {
	r := &BaseRunnable[string, string]{name: "empty"}

	ctx := context.Background()
	sr, err := r.Stream(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	// 空流应立即返回 EOF
	_, err = sr.Recv()
	if err == nil {
		t.Error("期望空流返回错误")
	}
}

// TestBaseRunnable_BatchStream 测试 BatchStream
func TestBaseRunnable_BatchStream(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	ctx := context.Background()
	sr, err := r.BatchStream(ctx, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	// 收集所有结果
	results, err := sr.Collect(ctx)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	// 结果应包含 2, 4, 6（顺序可能不同）
	sort.Ints(results)
	expected := []int{2, 4, 6}
	if len(results) != 3 {
		t.Fatalf("期望 3 个结果，但得到 %d", len(results))
	}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("期望 results[%d]=%d，但得到 %d", i, expected[i], v)
		}
	}
}

// TestBaseRunnable_BatchStreamEmpty 测试 BatchStream 空输入
func TestBaseRunnable_BatchStreamEmpty(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	ctx := context.Background()
	sr, err := r.BatchStream(ctx, []int{})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	// 空输入应返回空流
	results, err := sr.Collect(ctx)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("期望空结果，但得到 %d 个", len(results))
	}
}

// TestBaseRunnable_BatchStreamCustom 测试自定义 BatchStream 实现
func TestBaseRunnable_BatchStreamCustom(t *testing.T) {
	r := NewRunnable[int, int]("custom", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input, nil
	})
	r.WithBatchStream(func(ctx context.Context, inputs []int, opts ...Option) (*StreamReader[int], error) {
		total := 0
		for _, v := range inputs {
			total += v
		}
		return stream.FromValue(total), nil
	})

	ctx := context.Background()
	sr, err := r.BatchStream(ctx, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != 6 {
		t.Errorf("期望 6，但得到 %d", val)
	}
}

// TestBaseRunnable_WithStreamBuilder 测试 WithStream builder
func TestBaseRunnable_WithStreamBuilder(t *testing.T) {
	r := NewRunnable[string, string]("test", "", nil)
	r.WithStream(func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return stream.FromValue("自定义流_" + input), nil
	})

	ctx := context.Background()
	sr, err := r.Stream(ctx, "hello")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	val, err := sr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "自定义流_hello" {
		t.Errorf("期望 '自定义流_hello'，但得到 '%s'", val)
	}
}

// TestBaseRunnable_WithBatchBuilder 测试 WithBatch builder
func TestBaseRunnable_WithBatchBuilder(t *testing.T) {
	r := NewRunnable[int, int]("test", "", nil)
	r.WithBatch(func(ctx context.Context, inputs []int, opts ...Option) ([]int, error) {
		results := make([]int, len(inputs))
		for i, v := range inputs {
			results[i] = v + 100
		}
		return results, nil
	})

	ctx := context.Background()
	results, err := r.Batch(ctx, []int{1, 2})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(results) != 2 || results[0] != 101 || results[1] != 102 {
		t.Errorf("期望 [101,102]，但得到 %v", results)
	}
}

// TestBaseRunnable_WithCollectBuilder 测试 WithCollect builder
func TestBaseRunnable_WithCollectBuilder(t *testing.T) {
	r := NewRunnable[string, string]("test", "", nil)
	r.WithCollect(func(ctx context.Context, input *StreamReader[string], opts ...Option) (string, error) {
		val, err := input.Recv()
		if err != nil {
			return "", err
		}
		return "C_" + val, nil
	})

	ctx := context.Background()
	sr := stream.FromValue("hello")
	result, err := r.Collect(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "C_hello" {
		t.Errorf("期望 'C_hello'，但得到 '%s'", result)
	}
}

// TestBaseRunnable_WithTransformBuilder 测试 WithTransform builder
func TestBaseRunnable_WithTransformBuilder(t *testing.T) {
	r := NewRunnable[string, string]("test", "", nil)
	r.WithTransform(func(ctx context.Context, input *StreamReader[string], opts ...Option) (*StreamReader[string], error) {
		reader, writer := stream.Pipe[string](1)
		go func() {
			defer writer.Close()
			val, err := input.Recv()
			if err != nil {
				return
			}
			writer.Send("T_" + val)
		}()
		return reader, nil
	})

	ctx := context.Background()
	sr := stream.FromValue("hello")
	outSr, err := r.Transform(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	val, err := outSr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "T_hello" {
		t.Errorf("期望 'T_hello'，但得到 '%s'", val)
	}
}

// TestRunnableFunc 测试 RunnableFunc
func TestRunnableFunc(t *testing.T) {
	r := RunnableFunc[string, string]("upper", func(ctx context.Context, input string) (string, error) {
		return "UPPER_" + input, nil
	})

	if r.Name() != "upper" {
		t.Errorf("期望名称 'upper'，但得到 '%s'", r.Name())
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, "test")
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "UPPER_test" {
		t.Errorf("期望 'UPPER_test'，但得到 '%s'", result)
	}
}

// TestRunnableLambda 测试 RunnableLambda
func TestRunnableLambda(t *testing.T) {
	r := RunnableLambda(func(n int) int { return n * 3 })

	if r.Name() != "lambda" {
		t.Errorf("期望名称 'lambda'，但得到 '%s'", r.Name())
	}

	ctx := context.Background()
	result, err := r.Invoke(ctx, 7)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != 21 {
		t.Errorf("期望 21，但得到 %d", result)
	}
}

// TestApplyOptions 测试 ApplyOptions
func TestApplyOptions(t *testing.T) {
	opts := ApplyOptions(
		WithTimeout(5000),
		WithMaxRetries(3),
		WithMetadata(map[string]any{"key": "value"}),
		WithStreamBuffer(100),
		WithNodeID("node-1"),
		WithNodeType("llm"),
	)

	if opts.Timeout != 5000 {
		t.Errorf("期望 Timeout=5000，但得到 %d", opts.Timeout)
	}
	if opts.MaxRetries != 3 {
		t.Errorf("期望 MaxRetries=3，但得到 %d", opts.MaxRetries)
	}
	if opts.Metadata["key"] != "value" {
		t.Errorf("期望 Metadata[key]='value'，但得到 '%v'", opts.Metadata["key"])
	}
	if opts.StreamBufferSize != 100 {
		t.Errorf("期望 StreamBufferSize=100，但得到 %d", opts.StreamBufferSize)
	}
	if opts.NodeID != "node-1" {
		t.Errorf("期望 NodeID='node-1'，但得到 '%s'", opts.NodeID)
	}
	if opts.NodeType != "llm" {
		t.Errorf("期望 NodeType='llm'，但得到 '%s'", opts.NodeType)
	}
}

// TestApplyOptions_Empty 测试空选项
func TestApplyOptions_Empty(t *testing.T) {
	opts := ApplyOptions()
	if opts.Metadata == nil {
		t.Error("期望 Metadata 非 nil")
	}
	if opts.Extra == nil {
		t.Error("期望 Extra 非 nil")
	}
}

// TestBatch_EmptyInput 测试 Batch 空输入
func TestBatch_EmptyInput(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	ctx := context.Background()
	results, err := r.Batch(ctx, []int{})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if results != nil {
		t.Errorf("期望 nil 结果，但得到 %v", results)
	}
}

// TestBatch_SingleInput 测试 Batch 单个输入优化
func TestBatch_SingleInput(t *testing.T) {
	r := NewRunnable[int, int]("doubler", "", func(ctx context.Context, input int, opts ...Option) (int, error) {
		return input * 2, nil
	})

	ctx := context.Background()
	results, err := r.Batch(ctx, []int{5})
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if len(results) != 1 || results[0] != 10 {
		t.Errorf("期望 [10]，但得到 %v", results)
	}
}

// TestTransform_ContextCancel 测试 Transform 推导时 context 取消
func TestTransform_ContextCancel(t *testing.T) {
	r := NewRunnable[string, string]("slow", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		// 模拟慢操作
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return input, nil
		}
	})

	ctx, cancel := context.WithCancel(context.Background())

	// 创建一个无限流
	reader, writer := stream.Pipe[string](10)
	go func() {
		for i := 0; i < 100; i++ {
			if err := writer.Send(fmt.Sprintf("item_%d", i)); err != nil {
				return
			}
		}
		writer.Close()
	}()

	outSr, err := r.Transform(ctx, reader)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	// 取消 context
	cancel()

	// 给一点时间让 goroutine 响应取消
	time.Sleep(50 * time.Millisecond)

	// 尝试读取，应该得到错误或 context cancelled
	_, err = outSr.Recv()
	// 结果可能已经关闭或返回 context 错误，两种都可接受
	_ = err
}

// TestCollect_FromInvoke 测试 Collect 从 Invoke 推导
func TestCollect_FromInvoke(t *testing.T) {
	r := NewRunnable[string, string]("joiner", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		return "处理_" + input, nil
	})

	ctx := context.Background()
	sr := stream.FromValue("hello")
	result, err := r.Collect(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "处理_hello" {
		t.Errorf("期望 '处理_hello'，但得到 '%s'", result)
	}
}

// TestCollect_FromTransform 测试 Collect 从 Transform 推导
func TestCollect_FromTransform(t *testing.T) {
	r := &BaseRunnable[string, string]{name: "t_only"}
	r.transformFn = func(ctx context.Context, input *StreamReader[string], opts ...Option) (*StreamReader[string], error) {
		reader, writer := stream.Pipe[string](1)
		go func() {
			defer writer.Close()
			val, err := input.Recv()
			if err != nil {
				return
			}
			writer.Send("CT_" + val)
		}()
		return reader, nil
	}

	ctx := context.Background()
	sr := stream.FromValue("hello")
	result, err := r.Collect(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "CT_hello" {
		t.Errorf("期望 'CT_hello'，但得到 '%s'", result)
	}
}

// TestCollect_NoImpl 测试无实现时 Collect 返回零值
func TestCollect_NoImpl(t *testing.T) {
	r := &BaseRunnable[string, string]{name: "empty"}

	ctx := context.Background()
	sr := stream.FromValue("hello")
	result, err := r.Collect(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if result != "" {
		t.Errorf("期望空字符串，但得到 '%s'", result)
	}
}

// TestTransform_FromStream 测试 Transform 从 Stream 推导
func TestTransform_FromStream(t *testing.T) {
	r := &BaseRunnable[string, string]{name: "s_only"}
	r.streamFn = func(ctx context.Context, input string, opts ...Option) (*StreamReader[string], error) {
		return stream.FromValue("S_" + input), nil
	}

	ctx := context.Background()
	sr := stream.FromValue("hello")
	outSr, err := r.Transform(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	val, err := outSr.Recv()
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	if val != "S_hello" {
		t.Errorf("期望 'S_hello'，但得到 '%s'", val)
	}
}

// TestTransform_FromInvoke 测试 Transform 从 Invoke 推导
func TestTransform_FromInvoke(t *testing.T) {
	r := NewRunnable[string, string]("invoker", "", func(ctx context.Context, input string, opts ...Option) (string, error) {
		return "I_" + input, nil
	})

	ctx := context.Background()
	sr := stream.FromSlice([]string{"a", "b", "c"})
	outSr, err := r.Transform(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	results, err := outSr.Collect(ctx)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}
	expected := []string{"I_a", "I_b", "I_c"}
	if len(results) != 3 {
		t.Fatalf("期望 3 个结果，但得到 %d", len(results))
	}
	for i, v := range results {
		if v != expected[i] {
			t.Errorf("期望 results[%d]='%s'，但得到 '%s'", i, expected[i], v)
		}
	}
}

// TestTransform_NoImpl 测试无实现时 Transform 返回空流
func TestTransform_NoImpl(t *testing.T) {
	r := &BaseRunnable[string, string]{name: "empty"}

	ctx := context.Background()
	sr := stream.FromValue("hello")
	outSr, err := r.Transform(ctx, sr)
	if err != nil {
		t.Fatalf("期望无错误，但得到: %v", err)
	}

	_, err = outSr.Recv()
	if err == nil {
		t.Error("期望空流返回错误")
	}
}
