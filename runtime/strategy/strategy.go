// Package strategy 提供统一 agent loop 的可选执行策略。
//
// 三种策略（ReAct / PlanExecute / Reflection）都实现 runtime.Strategy 接口，
// 以「提示词引导 + 共享回合循环」的方式表达各自模式——运行在同一个统一 runtime
// 回合循环上，而非各自独立的 loop 实现。经 agent.WithStrategy 选用。
//
// 这是「统一 agent loop」的轻量落点：同一个 ReActAgent 通过切换 Strategy 即可
// 表现为 ReAct / 先规划后执行 / 自检反思。功能更丰富的多调用编排（独立的
// PlanExecuteAgent / ReflectionAgent）与本路径并存，互不影响。
package strategy

import hruntime "github.com/hexagon-codes/hexagon/runtime"

// 编译期断言：三种策略均满足 runtime.Strategy 接口。
var (
	_ hruntime.Strategy = ReAct{}
	_ hruntime.Strategy = PlanExecute{}
	_ hruntime.Strategy = Reflection{}
)

// All 返回全部内置策略（稳定顺序：react, plan-execute, reflection）。
func All() []hruntime.Strategy {
	return []hruntime.Strategy{ReAct{}, PlanExecute{}, Reflection{}}
}

// ByName 按名称返回内置策略；未知名称返回 (nil, false)。
//
// 名称与各策略 Name() 一致：react / plan-execute / reflection。
func ByName(name string) (hruntime.Strategy, bool) {
	for _, s := range All() {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}
