// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// barrier.go 实现延迟/屏障节点 (Barrier/Join Node)：
//   - BarrierNode: 等待所有指定的上游并行分支完成后再继续
//   - MapReduceNode: 将数据分片并行处理后聚合结果
//   - FanOutFanIn: 扇出扇入模式，自动并行执行后汇聚
//
// 对标 LangGraph 的 map-reduce 和 barrier 模式。
//
// 使用示例：
//
//	// 方式 1: Barrier 等待多个分支
//	graph := NewGraph[MyState]("pipeline").
//	    AddNode("step_a", handlerA).
//	    AddNode("step_b", handlerB).
//	    AddBarrier("join", mergeFunc, "step_a", "step_b").
//	    Build()
//
//	// 方式 2: MapReduce 模式
//	graph := NewGraph[MyState]("mr").
//	    AddMapReduce("process", splitFunc, mapFunc, reduceFunc).
//	    Build()
package graph

import (
	"context"
	"fmt"
	"sync"
)

// NodeTypeBarrier 屏障节点类型
const NodeTypeBarrier NodeType = 100

// BarrierMerger 屏障合并函数
// 接收原始状态和所有上游分支的输出状态，返回合并后的状态
// branchOutputs 仅含完整 State 前驱；映射节点的写集已通过字段 reducer 进入 original。
type BarrierMerger[S State] func(original S, branchOutputs map[string]S) S

// BarrierNode 创建屏障/延迟节点
// 等待所有指定的上游分支完成后，使用 merger 合并所有分支结果
//
// 参数:
//   - name: 节点名称
//   - merger: 状态合并函数
//   - waitFor: 需要等待的上游节点名称列表
func BarrierNode[S State](name string, merger BarrierMerger[S], waitFor ...string) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeBarrier,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 由执行器处理实际等待逻辑，此处直接返回
			// 如果 merger 为 nil，直接返回原始状态
			return state, nil
		},
		Metadata: map[string]any{
			"__barrier_wait_for": waitFor,
			"__barrier_merger":   merger,
		},
	}
}

// AddBarrier 在图构建器中添加屏障节点
// 等待所有指定上游节点完成后执行合并
func (b *GraphBuilder[S]) AddBarrier(name string, merger BarrierMerger[S], waitFor ...string) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	if len(waitFor) == 0 {
		b.err = fmt.Errorf("barrier %q 至少需要一个上游节点", name)
		return b
	}

	node := BarrierNode(name, merger, waitFor...)
	b.graph.Nodes[name] = node

	// 自动添加所有上游节点到此屏障的边
	for _, from := range waitFor {
		b.graph.Edges = append(b.graph.Edges, &Edge{
			From: from,
			To:   name,
			Type: EdgeTypeNormal,
		})
	}

	return b
}

// ============== MapReduce 模式 ==============

// SplitFunc 数据分片函数
// 将输入状态分割为多个子状态（每个子状态由一个 map worker 处理）
type SplitFunc[S State] func(state S) []S

// MapFunc 映射函数
// 对每个分片执行处理
type MapFunc[S State] func(ctx context.Context, state S) (S, error)

// ReduceFunc 归约函数
// 将所有 map 结果归约为最终状态
type ReduceFunc[S State] func(original S, results []S) S

// MapReduceNode 创建 MapReduce 节点
// 实现分片-并行处理-聚合的完整模式
//
// 参数:
//   - name: 节点名称
//   - split: 将输入状态分割为多个子状态
//   - mapFn: 对每个子状态执行处理
//   - reduce: 将所有处理结果归约为最终状态
//   - maxConcurrency: 最大并行度（0 表示无限制）
func MapReduceNode[S State](name string, split SplitFunc[S], mapFn MapFunc[S], reduce ReduceFunc[S], maxConcurrency int) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeParallel,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 1. 分片
			shards := split(state)
			if len(shards) == 0 {
				return state, nil
			}

			// 2. 并行 Map
			type mapResult struct {
				index int
				state S
				err   error
			}

			resultCh := make(chan mapResult, len(shards))

			// 信号量控制并发
			var sem chan struct{}
			if maxConcurrency > 0 {
				sem = make(chan struct{}, maxConcurrency)
			}

			var wg sync.WaitGroup
			for i, shard := range shards {
				wg.Add(1)
				go func(idx int, s S) {
					defer wg.Done()

					// 获取信号量
					if sem != nil {
						select {
						case sem <- struct{}{}:
							defer func() { <-sem }()
						case <-ctx.Done():
							resultCh <- mapResult{index: idx, state: s, err: ctx.Err()}
							return
						}
					}

					result, err := runIsolatedNode(ctx, fmt.Sprintf("%s[%d]", name, idx), NodeHandler[S](mapFn), s)
					resultCh <- mapResult{index: idx, state: result, err: err}
				}(i, shard)
			}

			// 等待所有 worker 完成
			go func() {
				wg.Wait()
				close(resultCh)
			}()

			// 收集结果
			results := make([]S, len(shards))
			outcomes := make([]ParallelResult[S], len(shards))
			for r := range resultCh {
				outcomes[r.index] = ParallelResult[S]{Name: fmt.Sprint(r.index), State: r.state, Error: r.err}
				results[r.index] = r.state
			}
			if err := parallelFailure(name, outcomes); err != nil {
				return state, err
			}

			// 3. Reduce
			return reduce(state, results), nil
		},
		Metadata: map[string]any{
			"__map_reduce":    true,
			"max_concurrency": maxConcurrency,
		},
	}
}

// AddMapReduce 在图构建器中添加 MapReduce 节点
func (b *GraphBuilder[S]) AddMapReduce(name string, split SplitFunc[S], mapFn MapFunc[S], reduce ReduceFunc[S], maxConcurrency int) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	node := MapReduceNode(name, split, mapFn, reduce, maxConcurrency)
	b.graph.Nodes[name] = node
	return b
}

// ============== FanOut/FanIn 模式 ==============

// FanOutFanInNode 创建扇出扇入节点
// 将状态同时发送到多个处理函数并行执行，然后合并所有结果
//
// 与 ParallelNodeWithMerger 的区别：
//   - FanOutFanIn 返回每个分支的命名结果，便于后续处理
//   - 每个分支有自己的名称，可在 merger 中按名称访问
func FanOutFanInNode[S State](name string, branches map[string]NodeHandler[S], merger BarrierMerger[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeParallel,
		Handler: func(ctx context.Context, state S) (S, error) {
			if len(branches) == 0 {
				return state, nil
			}

			type branchResult struct {
				name  string
				state S
				err   error
			}

			resultCh := make(chan branchResult, len(branches))

			for branchName, handler := range branches {
				go func(n string, h NodeHandler[S]) {
					result, err := runIsolatedNode(ctx, n, h, state)
					resultCh <- branchResult{name: n, state: result, err: err}
				}(branchName, handler)
			}

			// 收集结果
			outputs := make(map[string]S, len(branches))
			for range branches {
				r := <-resultCh
				if r.err != nil {
					return state, fmt.Errorf("扇出扇入节点 %q 的分支 %q 失败: %w", name, r.name, r.err)
				}
				outputs[r.name] = r.state
			}

			// 合并
			if merger == nil {
				if len(outputs) > 1 {
					return state, fmt.Errorf("fan-out node %q requires an explicit state merger", name)
				}
				for _, s := range outputs {
					return s, nil
				}
			}
			return merger(state, outputs), nil
		},
		Metadata: map[string]any{
			"__fan_out_fan_in": true,
			"branch_count":     len(branches),
		},
	}
}

// AddFanOutFanIn 在图构建器中添加扇出扇入节点
func (b *GraphBuilder[S]) AddFanOutFanIn(name string, branches map[string]NodeHandler[S], merger BarrierMerger[S]) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	node := FanOutFanInNode(name, branches, merger)
	b.graph.Nodes[name] = node
	return b
}
