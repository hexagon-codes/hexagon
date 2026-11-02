// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// command.go 实现 Command API（状态更新+节点跳转合一）：
//   - Command: 统一的命令对象，同时携带状态更新和路由指令
//   - CommandHandler: 返回 Command 的节点处理函数
//   - CommandNode: 使用 Command API 的节点
//
// 对标 LangGraph 的 Command API，让常见的"更新状态并跳转到下一节点"操作更简洁。
//
// 使用示例：
//
//	// 传统方式需要分别设置状态和条件路由
//	// Command API 方式：一步到位
//	graph := NewGraph[MyState]("flow").
//	    AddCommandNode("classify", func(ctx context.Context, state MyState) (*Command[MyState], error) {
//	        if state.IsUrgent {
//	            return Goto[MyState]("urgent_handler").
//	                WithUpdate(func(s MyState) MyState { s.Priority = "high"; return s }),
//	                nil
//	        }
//	        return Goto[MyState]("normal_handler").
//	            WithUpdate(func(s MyState) MyState { s.Priority = "normal"; return s }),
//	            nil
//	    }).
//	    Build()
package graph

import (
	"context"
	"fmt"
)

// Command 命令对象
// 同时携带状态更新操作和路由决策
type Command[S State] struct {
	// goto_ 目标节点名称
	goto_ string

	// updates 状态更新函数列表（按顺序执行）
	updates []func(S) S

	// send 发送到指定节点的数据
	sends []Send

	// metadata 附加元数据
	metadata map[string]any
}

// Goto 创建跳转到指定节点的命令
func Goto[S State](target string) *Command[S] {
	return &Command[S]{
		goto_:    target,
		metadata: make(map[string]any),
	}
}

// GotoEnd 创建跳转到结束节点的命令
func GotoEnd[S State]() *Command[S] {
	return Goto[S](END)
}

// WithUpdate 添加状态更新函数
// 可链式调用多次，按顺序执行
func (c *Command[S]) WithUpdate(update func(S) S) *Command[S] {
	c.updates = append(c.updates, update)
	return c
}

// WithState 直接设置新状态（替换而非更新）
func (c *Command[S]) WithState(state S) *Command[S] {
	c.updates = append(c.updates, func(_ S) S { return state })
	return c
}

// WithSend 附带发送数据到其他节点
func (c *Command[S]) WithSend(sends ...Send) *Command[S] {
	c.sends = append(c.sends, sends...)
	return c
}

// WithMetadata 附加元数据
func (c *Command[S]) WithMetadata(key string, value any) *Command[S] {
	c.metadata[key] = value
	return c
}

// Target 返回目标节点
func (c *Command[S]) Target() string {
	return c.goto_
}

// ApplyUpdates 应用所有状态更新
func (c *Command[S]) ApplyUpdates(state S) S {
	for _, update := range c.updates {
		state = update(state)
	}
	return state
}

// Sends 返回所有发送项
func (c *Command[S]) Sends() []Send {
	return c.sends
}

// CommandHandler 命令处理函数类型
// 返回 Command 而不是直接返回状态和路由
type CommandHandler[S State] func(ctx context.Context, state S) (*Command[S], error)

// AddCommandNode 在图构建器中添加使用 Command API 的节点
// Command 节点的处理函数返回 Command，自动处理状态更新和路由
func (b *GraphBuilder[S]) AddCommandNode(name string, handler CommandHandler[S]) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	if name == START || name == END {
		b.err = fmt.Errorf("cannot use reserved node name: %s", name)
		return b
	}

	if _, exists := b.graph.Nodes[name]; exists {
		b.err = fmt.Errorf("node %s already exists", name)
		return b
	}

	// 包装 CommandHandler 为标准 NodeHandler
	b.graph.Nodes[name] = &Node[S]{
		Name:           name,
		Type:           NodeTypeConditional, // 因为 Command 自带路由逻辑
		commandHandler: handler,
		Handler: func(ctx context.Context, state S) (S, error) {
			output, _, err := invokeCommand(ctx, handler, state)
			return output, err
		},
		Metadata: map[string]any{
			"__command_handler": handler,
			"__is_command_node": true,
		},
	}

	return b
}

func invokeCommand[S State](ctx context.Context, handler CommandHandler[S], state S) (S, *string, error) {
	command, err := handler(ctx, state)
	if err != nil {
		return state, nil, err
	}
	target := END
	if command == nil {
		return state, &target, nil
	}
	target = command.Target()
	return command.ApplyUpdates(state), &target, nil
}

// executeCommandNode 将状态与路由作为同一次 attempt 的结果返回，不写入共享定义。
func executeCommandNode[S State](ctx context.Context, node *Node[S], input S, defaults *RetryPolicy) (S, *string, error) {
	if node.commandHandler == nil {
		output, err := executeNodeHandler(ctx, node, input, defaults)
		return output, nil, err
	}
	local := *node
	var target *string
	local.Handler = func(callCtx context.Context, state S) (S, error) {
		output, next, err := invokeCommand(callCtx, node.commandHandler, state)
		if err == nil {
			target = next
		}
		return output, err
	}
	output, err := executeNodeHandler(ctx, &local, input, defaults)
	if err != nil {
		return output, nil, err
	}
	return output, target, nil
}

// ============== 便捷函数 ==============

// UpdateAndGoto 创建一个更新状态并跳转的命令（最常见场景的快捷方式）
func UpdateAndGoto[S State](target string, update func(S) S) *Command[S] {
	return Goto[S](target).WithUpdate(update)
}

// UpdateAndEnd 创建一个更新状态并结束的命令
func UpdateAndEnd[S State](update func(S) S) *Command[S] {
	return GotoEnd[S]().WithUpdate(update)
}

// GotoIf 条件跳转
func GotoIf[S State](condition bool, ifTrue, ifFalse string) *Command[S] {
	if condition {
		return Goto[S](ifTrue)
	}
	return Goto[S](ifFalse)
}

// GotoSwitch 多路选择跳转
func GotoSwitch[S State](label string, routes map[string]string) *Command[S] {
	if target, ok := routes[label]; ok {
		return Goto[S](target)
	}
	return GotoEnd[S]()
}
