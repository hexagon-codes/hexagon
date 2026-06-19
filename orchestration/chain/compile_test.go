package chain

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
)

// TestCompile_CompatibleConcreteChain 验证类型衔接正确的链通过校验并能执行。
func TestCompile_CompatibleConcreteChain(t *testing.T) {
	double := NewTypedNode("double", func(_ context.Context, i int) (int, error) { return i * 2, nil })
	toStr := NewTypedNode("toStr", func(_ context.Context, i int) (string, error) { return strconv.Itoa(i), nil })

	c, err := Compile(double, toStr)
	if err != nil {
		t.Fatalf("相容链应通过 Compile: %v", err)
	}
	got, err := c.Run(context.Background(), 21)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "42" {
		t.Errorf("Run = %v, want 42", got)
	}
}

// TestCompile_IncompatibleConcreteFails 验证相邻具体类型不匹配时在构建期报错。
func TestCompile_IncompatibleConcreteFails(t *testing.T) {
	outStr := NewTypedNode("outStr", func(_ context.Context, i int) (string, error) { return "x", nil })
	inInt := NewTypedNode("inInt", func(_ context.Context, i int) (int, error) { return i, nil })

	_, err := Compile(outStr, inInt) // string -> int 不兼容
	if err == nil {
		t.Fatal("不兼容的相邻类型应在 Compile 期报错")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回 *CompileError, got %T", err)
	}
	if ce.FromNode != "outStr" || ce.ToNode != "inInt" {
		t.Errorf("错误应指明 outStr->inInt, got %s->%s", ce.FromNode, ce.ToNode)
	}
}

// TestCompile_DownstreamInterfaceImplemented 验证下游输入是接口且上游输出实现它时通过（规则二）。
func TestCompile_DownstreamInterfaceImplemented(t *testing.T) {
	// 上游输出具体 *bytes.Buffer，下游输入 io.Reader 接口
	produce := NewTypedNode("produce", func(_ context.Context, s string) (*bytes.Buffer, error) {
		return bytes.NewBufferString(s), nil
	})
	consume := NewTypedNode("consume", func(_ context.Context, r io.Reader) (string, error) {
		b, _ := io.ReadAll(r)
		return string(b), nil
	})

	c, err := Compile(produce, consume)
	if err != nil {
		t.Fatalf("*bytes.Buffer 实现 io.Reader 应通过: %v", err)
	}
	got, err := c.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "hello" {
		t.Errorf("Run = %v, want hello", got)
	}
}

// TestCompile_UpstreamInterfaceDefersToRuntime 验证上游输出是接口时不在编译期否决（规则三）。
func TestCompile_UpstreamInterfaceDefersToRuntime(t *testing.T) {
	// 上游输出 io.Reader 接口，下游输入具体 *bytes.Buffer —— 编译期无法确定，留运行期断言
	produce := NewTypedNode("produce", func(_ context.Context, s string) (io.Reader, error) {
		return bytes.NewBufferString(s), nil
	})
	consume := NewTypedNode("consume", func(_ context.Context, b *bytes.Buffer) (int, error) {
		return b.Len(), nil
	})

	if _, err := Compile(produce, consume); err != nil {
		t.Fatalf("上游接口应留运行期断言、不在编译期否决: %v", err)
	}
}

// TestCompile_Empty 验证空节点列表报错。
func TestCompile_Empty(t *testing.T) {
	if _, err := Compile(); err == nil {
		t.Error("空链应报错")
	}
}

// TestCompile_RuntimeAssertFailsAtRun 验证规则三留到运行期的断言在类型确实不符时于运行期报错。
func TestCompile_RuntimeAssertFailsAtRun(t *testing.T) {
	// 上游接口实际产出 *bytes.Buffer，但下游要求 *strings.Reader —— 编译期放行、运行期断言失败
	produce := NewTypedNode("produce", func(_ context.Context, s string) (io.Reader, error) {
		return bytes.NewBufferString(s), nil
	})
	consume := NewTypedNode("consume", func(_ context.Context, r *bytes.Reader) (int, error) {
		return int(r.Size()), nil
	})

	c, err := Compile(produce, consume)
	if err != nil {
		t.Fatalf("编译期应放行接口上游: %v", err)
	}
	if _, err := c.Run(context.Background(), "data"); err == nil {
		t.Error("运行期类型断言应失败")
	}
}
