// Package chain 提供 Hexagon AI Agent 框架的链式编排
//
// 本文件提供链的构建期 I/O 类型校验：把相邻节点的输出/输入反射类型记录下来，
// 在 Compile 时逐对校验，类型不匹配立即报错——而不是等到运行时数据流到中途
// 才因 %T 断言失败而中断。
//
// 三条兼容规则（上游输出 out 能否流入下游输入 in）：
//   - 相等：out == in（含 out 可直接赋值给 in）
//   - 下游接口被实现：in 是接口且 out 实现它
//   - 上游接口留运行期：out 是接口（具体值运行期才确定）→ 不在构建期否决，留运行期断言
package chain

import (
	"context"
	"fmt"
	"reflect"
)

// TypeCompat 描述相邻节点输出→输入的类型兼容判定结果。
type TypeCompat int

const (
	// TypeIncompatible 不兼容：Compile 期报错
	TypeIncompatible TypeCompat = iota
	// TypeEqual 相等或可直接赋值
	TypeEqual
	// TypeImplements 下游输入是接口且上游输出实现它
	TypeImplements
	// TypeRuntimeAssert 上游输出是接口（或类型未知）：编译期无法确定，留运行期断言
	TypeRuntimeAssert
)

// checkTypeCompat 按三规则判定上游输出 out 能否流入下游输入 in。
func checkTypeCompat(out, in reflect.Type) TypeCompat {
	if out == nil || in == nil {
		return TypeRuntimeAssert // 类型未知，留运行期
	}
	if out == in || out.AssignableTo(in) {
		return TypeEqual
	}
	if in.Kind() == reflect.Interface && out.Implements(in) {
		return TypeImplements
	}
	if out.Kind() == reflect.Interface {
		// 上游产出接口：具体值运行期才知，可能满足 in → 不在编译期否决，留运行期断言
		return TypeRuntimeAssert
	}
	return TypeIncompatible
}

// TypedNode 是带输入/输出反射类型的链节点，供 Compile 做相邻节点 I/O 校验。
type TypedNode struct {
	Name    string
	InType  reflect.Type
	OutType reflect.Type
	Handler func(ctx context.Context, input any) (any, error)
}

// NewTypedNode 从类型安全的处理函数创建带反射类型的节点。
//
// 节点的输入/输出类型由泛型参数 I/O 捕获，使 Compile 能在构建期校验相邻节点衔接。
func NewTypedNode[I, O any](name string, fn func(ctx context.Context, input I) (O, error)) TypedNode {
	return TypedNode{
		Name:    name,
		InType:  reflect.TypeFor[I](),
		OutType: reflect.TypeFor[O](),
		Handler: func(ctx context.Context, input any) (any, error) {
			typed, ok := input.(I)
			if !ok {
				var zero I
				return nil, fmt.Errorf("input type mismatch: expected %T, got %T", zero, input)
			}
			return fn(ctx, typed)
		},
	}
}

// CompileError 描述 Compile 期发现的相邻节点 I/O 类型不匹配。
type CompileError struct {
	FromNode string
	ToNode   string
	OutType  reflect.Type
	InType   reflect.Type
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("chain compile: 节点 %q 输出类型 %v 无法流入节点 %q 输入类型 %v",
		e.FromNode, e.OutType, e.ToNode, e.InType)
}

// CompiledChain 是通过 I/O 类型校验后的可执行链。
type CompiledChain struct {
	nodes []TypedNode
}

// Compile 校验相邻节点的 I/O 类型（三规则），任一对不兼容即在构建期返回 *CompileError，
// 不必等运行时数据流到中途才暴露类型错误。校验通过返回可执行的 CompiledChain。
func Compile(nodes ...TypedNode) (*CompiledChain, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("chain compile: 至少需要一个节点")
	}
	for i := 0; i+1 < len(nodes); i++ {
		out, in := nodes[i].OutType, nodes[i+1].InType
		if checkTypeCompat(out, in) == TypeIncompatible {
			return nil, &CompileError{
				FromNode: nodes[i].Name,
				ToNode:   nodes[i+1].Name,
				OutType:  out,
				InType:   in,
			}
		}
	}
	return &CompiledChain{nodes: nodes}, nil
}

// Run 顺序执行编译后的链：每个节点的输出作为下一个节点的输入。
func (c *CompiledChain) Run(ctx context.Context, input any) (any, error) {
	current := input
	for _, n := range c.nodes {
		out, err := n.Handler(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", n.Name, err)
		}
		current = out
	}
	return current, nil
}

// Len 返回链中节点数量。
func (c *CompiledChain) Len() int { return len(c.nodes) }
