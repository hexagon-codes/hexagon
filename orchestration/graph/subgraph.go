// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// 本文件实现子图功能，支持：
//   - 子图嵌入：将一个图作为另一个图的节点
//   - 图组合：动态组合多个图
//   - 图分解：将大图分解为子图
//   - 动态图：运行时动态添加/删除节点和边

package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hexagon-codes/hexagon/interrupt"
)

type graphContinuation struct {
	Name     string          `json:"name"`
	State    json.RawMessage `json:"state"`
	Frontier json.RawMessage `json:"frontier"`
}

// ============== 子图节点 ==============

// SubgraphNode 创建子图节点
//
// 将一个完整的图作为当前图的一个节点使用。
// 子图的输入是父节点的状态，输出合并回父状态。
//
// 参数：
//   - name: 节点名称
//   - subgraph: 子图
//   - stateMapper: 可选的状态映射函数
func SubgraphNode[S State](name string, subgraph *Graph[S], stateMapper ...*SubgraphStateMapper[S]) *Node[S] {
	var mapper *SubgraphStateMapper[S]
	if len(stateMapper) > 0 {
		mapper = stateMapper[0]
	}

	return &Node[S]{
		Name: name,
		Type: NodeTypeSubgraph,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 应用输入映射
			inputState := state
			if mapper != nil && mapper.Input != nil {
				inputState = mapper.Input(state)
			}

			x := newPlanExecutor(subgraph, inputState)
			wasInterrupted, hasState, progress := interrupt.GetInterruptState[*graphContinuation](ctx)
			if wasInterrupted && !hasState {
				return state, fmt.Errorf("subgraph continuation type mismatch")
			}
			if hasState {
				if progress == nil || progress.Name != subgraph.Name {
					return state, fmt.Errorf("subgraph continuation identity mismatch")
				}
				var err error
				x.state, err = subgraph.codec().Decode(progress.State)
				if err != nil {
					return state, err
				}
				x.frontier, err = subgraph.decodeFrontier(progress.Frontier)
				if err != nil {
					return state, err
				}
			}
			outputState, err := x.run(ctx)
			if err != nil {
				if _, ok := graphWaitingSignal(ctx, err); ok {
					for id, result := range x.frontier.Results {
						if len(result.Output) == 0 {
							data, encodeErr := subgraph.codec().Encode(x.outputs[id])
							if encodeErr != nil {
								return state, encodeErr
							}
							result.Output = data
							writes, encodeErr := encodeWrites(x.writes[id])
							if encodeErr != nil {
								return state, encodeErr
							}
							result.Writes = writes
						}
					}
					encoded, encodeErr := subgraph.codec().Encode(outputState)
					if encodeErr != nil {
						return state, encodeErr
					}
					frontier, encodeErr := subgraph.encodeFrontier(x.frontier)
					if encodeErr != nil {
						return state, encodeErr
					}
					return state, interrupt.CompositeInterrupt(ctx, "subgraph is waiting", &graphContinuation{Name: subgraph.Name, State: encoded, Frontier: frontier}, err)
				}
				return state, fmt.Errorf("subgraph %s failed: %w", name, err)
			}

			// 应用输出映射
			if mapper != nil && mapper.Output != nil {
				return mapper.Output(state, outputState), nil
			}

			return outputState, nil
		},
		Metadata: map[string]any{
			"subgraph":      subgraph,
			"subgraph_name": subgraph.Name,
		},
	}
}

// SubgraphStateMapper 子图状态映射器
//
// 用于在父图和子图之间转换状态
type SubgraphStateMapper[S State] struct {
	// Input 输入映射：将父状态转换为子图输入状态
	Input func(parentState S) S

	// Output 输出映射：将子图输出合并到父状态
	Output func(parentState, subgraphOutput S) S
}

// ============== 动态图 ==============

// DynamicGraph 动态图
//
// 支持运行时动态修改图结构，包括添加/删除节点和边。
// 线程安全，可在执行过程中修改。
type DynamicGraph[S State] struct {
	*Graph[S]

	// 动态修改锁
	dynamicMu sync.RWMutex

	// 版本号，用于检测修改
	version int64

	// 修改回调
	onModified []func(g *DynamicGraph[S])
}

// NewDynamicGraph 创建动态图
func NewDynamicGraph[S State](name string) *DynamicGraph[S] {
	return &DynamicGraph[S]{
		Graph: &Graph[S]{
			Name:             name,
			Nodes:            make(map[string]*Node[S]),
			Edges:            make([]*Edge, 0),
			Metadata:         make(map[string]any),
			adjacency:        make(map[string][]string),
			conditionalEdges: make(map[string][]conditionalEdge[S]),
		},
		version: 0,
	}
}

// AddNodeDynamic 动态添加节点
//
// 与普通 AddNode 不同，此方法可在图已编译后调用。
func (g *DynamicGraph[S]) AddNodeDynamic(name string, handler NodeHandler[S]) error {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	if name == START || name == END {
		return fmt.Errorf("cannot use reserved node name: %s", name)
	}

	if _, exists := g.Nodes[name]; exists {
		return fmt.Errorf("node %s already exists", name)
	}

	g.Nodes[name] = &Node[S]{
		Name:     name,
		Type:     NodeTypeNormal,
		Handler:  handler,
		Metadata: make(map[string]any),
	}

	g.version++
	g.notifyModified()

	return nil
}

// RemoveNodeDynamic 动态移除节点
func (g *DynamicGraph[S]) RemoveNodeDynamic(name string) error {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	if name == START || name == END {
		return fmt.Errorf("cannot remove reserved node: %s", name)
	}

	if _, exists := g.Nodes[name]; !exists {
		return fmt.Errorf("node %s not found", name)
	}

	delete(g.Nodes, name)

	// 移除相关的边
	newEdges := make([]*Edge, 0)
	for _, edge := range g.Edges {
		if edge.From != name && edge.To != name {
			newEdges = append(newEdges, edge)
		}
	}
	g.Edges = newEdges

	// 更新邻接表
	delete(g.adjacency, name)
	for k, neighbors := range g.adjacency {
		newNeighbors := make([]string, 0)
		for _, n := range neighbors {
			if n != name {
				newNeighbors = append(newNeighbors, n)
			}
		}
		g.adjacency[k] = newNeighbors
	}

	// 移除条件边
	delete(g.conditionalEdges, name)

	g.version++
	g.notifyModified()

	return nil
}

// AddEdgeDynamic 动态添加边
func (g *DynamicGraph[S]) AddEdgeDynamic(from, to string) error {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	// 验证节点存在
	if _, exists := g.Nodes[from]; !exists {
		return fmt.Errorf("source node %s not found", from)
	}
	if _, exists := g.Nodes[to]; !exists {
		return fmt.Errorf("target node %s not found", to)
	}

	// 添加边
	g.Edges = append(g.Edges, &Edge{
		From: from,
		To:   to,
		Type: EdgeTypeNormal,
	})

	// 更新邻接表
	g.adjacency[from] = append(g.adjacency[from], to)

	g.version++
	g.notifyModified()

	return nil
}

// RemoveEdgeDynamic 动态移除边
func (g *DynamicGraph[S]) RemoveEdgeDynamic(from, to string) error {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	// 移除边
	newEdges := make([]*Edge, 0)
	found := false
	for _, edge := range g.Edges {
		if edge.From == from && edge.To == to {
			found = true
			continue
		}
		newEdges = append(newEdges, edge)
	}
	if !found {
		return fmt.Errorf("edge %s -> %s not found", from, to)
	}
	g.Edges = newEdges

	// 更新邻接表
	if neighbors, ok := g.adjacency[from]; ok {
		newNeighbors := make([]string, 0)
		for _, n := range neighbors {
			if n != to {
				newNeighbors = append(newNeighbors, n)
			}
		}
		g.adjacency[from] = newNeighbors
	}

	g.version++
	g.notifyModified()

	return nil
}

// ReplaceNodeHandler 替换节点处理函数
func (g *DynamicGraph[S]) ReplaceNodeHandler(name string, handler NodeHandler[S]) error {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	node, exists := g.Nodes[name]
	if !exists {
		return fmt.Errorf("node %s not found", name)
	}

	node.Handler = handler
	g.version++
	g.notifyModified()

	return nil
}

// OnModified 注册修改回调
func (g *DynamicGraph[S]) OnModified(callback func(g *DynamicGraph[S])) {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()
	g.onModified = append(g.onModified, callback)
}

// Version 返回当前版本号
func (g *DynamicGraph[S]) Version() int64 {
	g.dynamicMu.RLock()
	defer g.dynamicMu.RUnlock()
	return g.version
}

// notifyModified 通知修改
func (g *DynamicGraph[S]) notifyModified() {
	for _, callback := range g.onModified {
		callback(g)
	}
}

// Build 构建动态图
func (g *DynamicGraph[S]) Build() (*DynamicGraph[S], error) {
	g.dynamicMu.Lock()
	defer g.dynamicMu.Unlock()

	// 添加 START 和 END 节点
	if _, exists := g.Nodes[START]; !exists {
		g.Nodes[START] = StartNode[S]()
	}
	if _, exists := g.Nodes[END]; !exists {
		g.Nodes[END] = EndNode[S]()
	}

	// 编译图
	if err := g.Graph.compile(); err != nil {
		return nil, err
	}

	return g, nil
}

// ============== 图组合器 ==============

// GraphComposer 图组合器
//
// 用于组合多个图成为一个更大的图
type GraphComposer[S State] struct {
	// name 组合图名称
	name string

	// graphs 子图列表
	graphs []*Graph[S]

	// connections 子图之间的连接
	connections []graphConnection
}

// graphConnection 图连接
type graphConnection struct {
	FromGraph int    // 源图索引
	FromNode  string // 源节点（留空表示图的出口）
	ToGraph   int    // 目标图索引
	ToNode    string // 目标节点（留空表示图的入口）
}

// NewGraphComposer 创建图组合器
func NewGraphComposer[S State](name string) *GraphComposer[S] {
	return &GraphComposer[S]{
		name:        name,
		graphs:      make([]*Graph[S], 0),
		connections: make([]graphConnection, 0),
	}
}

// AddGraph 添加子图
//
// 返回子图的索引，用于后续连接
func (c *GraphComposer[S]) AddGraph(g *Graph[S]) int {
	c.graphs = append(c.graphs, g)
	return len(c.graphs) - 1
}

// Connect 连接两个子图
//
// 参数：
//   - fromGraph: 源图索引
//   - fromNode: 源节点（空字符串表示图的默认出口）
//   - toGraph: 目标图索引
//   - toNode: 目标节点（空字符串表示图的默认入口）
func (c *GraphComposer[S]) Connect(fromGraph int, fromNode string, toGraph int, toNode string) *GraphComposer[S] {
	c.connections = append(c.connections, graphConnection{
		FromGraph: fromGraph,
		FromNode:  fromNode,
		ToGraph:   toGraph,
		ToNode:    toNode,
	})
	return c
}

// Sequential 顺序连接所有子图
func (c *GraphComposer[S]) Sequential() *GraphComposer[S] {
	for i := 0; i < len(c.graphs)-1; i++ {
		c.Connect(i, "", i+1, "")
	}
	return c
}

// Compose 组合成新图
func (c *GraphComposer[S]) Compose() (*Graph[S], error) {
	if len(c.graphs) == 0 {
		return nil, fmt.Errorf("no graphs to compose")
	}

	builder := NewGraph[S](c.name)

	// 为每个子图创建子图节点
	for i, g := range c.graphs {
		nodeName := fmt.Sprintf("%s_%d", g.Name, i)
		subgraphNode := SubgraphNode(nodeName, g)
		builder.AddNodeWithBuilder(subgraphNode)
	}

	// 设置入口点（第一个子图）
	firstNodeName := fmt.Sprintf("%s_%d", c.graphs[0].Name, 0)
	builder.AddEdge(START, firstNodeName)

	// 添加子图之间的连接
	for _, conn := range c.connections {
		fromName := fmt.Sprintf("%s_%d", c.graphs[conn.FromGraph].Name, conn.FromGraph)
		toName := fmt.Sprintf("%s_%d", c.graphs[conn.ToGraph].Name, conn.ToGraph)
		builder.AddEdge(fromName, toName)
	}

	// 设置出口点（最后一个子图）
	lastNodeName := fmt.Sprintf("%s_%d", c.graphs[len(c.graphs)-1].Name, len(c.graphs)-1)
	builder.AddEdge(lastNodeName, END)

	return builder.Build()
}

// ============== 并行子图执行 ==============

// ParallelSubgraphs 创建并行子图执行节点
//
// 同时执行多个子图，并合并结果。
//
// 参数：
//   - name: 节点名称
//   - subgraphs: 要并行执行的子图列表
//   - merger: 状态合并函数
//
// 错误时等待所有已派发子图结束，通过 ParallelExecutionError 保留完整结果，不盲重放成功子图。
func ParallelSubgraphs[S State](name string, subgraphs []*Graph[S], merger StateMerger[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeParallel,
		Handler: func(ctx context.Context, state S) (S, error) {
			if len(subgraphs) == 0 {
				return state, nil
			}

			// 并行执行所有子图
			type result struct {
				idx   int
				state S
				err   error
			}
			results := make(chan result, len(subgraphs))

			for i, sg := range subgraphs {
				i, sg := i, sg
				go func() {
					output, err := runIsolatedNode(ctx, sg.Name, func(ctx context.Context, input S) (S, error) {
						return sg.Run(ctx, input)
					}, state)
					results <- result{idx: i, state: output, err: err}
				}()
			}

			// 收集结果
			outputs := make([]S, len(subgraphs))
			outcomes := make([]ParallelResult[S], len(subgraphs))
			for range subgraphs {
				r := <-results
				outcomes[r.idx] = ParallelResult[S]{Name: subgraphs[r.idx].Name, State: r.state, Error: r.err}
				outputs[r.idx] = r.state
			}
			if err := parallelFailure(name, outcomes); err != nil {
				return state, err
			}

			// 合并结果
			if merger == nil {
				if len(outputs) > 1 {
					return state, &ParallelExecutionError[S]{Name: name, Results: outcomes, Cause: fmt.Errorf("parallel subgraphs %q require an explicit state merger", name)}
				}
				return outputs[len(outputs)-1], nil
			}

			return merger(state, outputs), nil
		},
		Metadata: map[string]any{
			"parallel_subgraphs": len(subgraphs),
		},
	}
}

// StateMerger 状态合并函数
//
// 将多个子图的输出状态合并为一个状态
type StateMerger[S State] func(original S, outputs []S) S

// ============== 条件子图 ==============

// ConditionalSubgraph 创建条件子图节点
//
// 根据条件选择执行不同的子图。
//
// 参数：
//   - name: 节点名称
//   - selector: 选择器函数，返回要执行的子图索引
//   - subgraphs: 可选的子图列表
func ConditionalSubgraph[S State](name string, selector func(S) int, subgraphs ...*Graph[S]) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeConditional,
		Handler: func(ctx context.Context, state S) (S, error) {
			if len(subgraphs) == 0 {
				return state, nil
			}

			// 选择子图
			idx := selector(state)
			if idx < 0 || idx >= len(subgraphs) {
				return state, fmt.Errorf("invalid subgraph index: %d", idx)
			}

			// 执行选中的子图
			return subgraphs[idx].Run(ctx, state)
		},
		Metadata: map[string]any{
			"conditional_subgraphs": len(subgraphs),
		},
	}
}

// ============== 循环子图 ==============

// LoopSubgraph 创建循环子图节点
//
// 重复执行子图直到满足退出条件。
//
// 参数：
//   - name: 节点名称
//   - subgraph: 要循环执行的子图
//   - condition: 继续循环的条件（返回 true 继续，false 退出）
//   - maxIterations: 最大迭代次数（0 表示无限制）
func LoopSubgraph[S State](name string, subgraph *Graph[S], condition func(S, int) bool, maxIterations int) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeNormal,
		Handler: func(ctx context.Context, state S) (S, error) {
			currentState := state
			iteration := 0

			for {
				// 检查最大迭代次数
				if maxIterations > 0 && iteration >= maxIterations {
					break
				}

				// 检查退出条件
				if !condition(currentState, iteration) {
					break
				}

				// 检查 context
				if ctx.Err() != nil {
					return currentState, ctx.Err()
				}

				// 执行子图
				newState, err := subgraph.Run(ctx, currentState)
				if err != nil {
					return currentState, fmt.Errorf("loop iteration %d failed: %w", iteration, err)
				}

				currentState = newState
				iteration++
			}

			return currentState, nil
		},
		Metadata: map[string]any{
			"loop_subgraph":  subgraph.Name,
			"max_iterations": maxIterations,
		},
	}
}

// ============== 图分支合并 ==============

// BranchNode 创建分支节点
//
// 根据状态决定下一步执行哪个分支。
//
// 参数：
//   - name: 节点名称
//   - branches: 分支映射（label -> 子图）
//   - selector: 分支选择器，返回分支的 label
func BranchNode[S State](name string, branches map[string]*Graph[S], selector func(S) string) *Node[S] {
	return &Node[S]{
		Name: name,
		Type: NodeTypeConditional,
		Handler: func(ctx context.Context, state S) (S, error) {
			// 选择分支
			label := selector(state)
			subgraph, ok := branches[label]
			if !ok {
				return state, fmt.Errorf("branch %s not found", label)
			}

			// 执行分支子图
			return subgraph.Run(ctx, state)
		},
		Metadata: map[string]any{
			"branch_node":   true,
			"branch_count":  len(branches),
			"branch_labels": getBranchLabels(branches),
		},
	}
}

// getBranchLabels 获取分支标签列表
func getBranchLabels[S State](branches map[string]*Graph[S]) []string {
	labels := make([]string, 0, len(branches))
	for label := range branches {
		labels = append(labels, label)
	}
	return labels
}

// ============== 图快照和恢复 ==============

// GraphSnapshot 图快照
type GraphSnapshot[S State] struct {
	// Name 图名称
	Name string

	// NodeNames 节点名称列表
	NodeNames []string

	// Edges 边列表
	Edges []EdgeSnapshot

	// Version 版本号
	Version int64

	// Metadata 元数据
	Metadata map[string]any
}

// EdgeSnapshot 边快照
type EdgeSnapshot struct {
	From string
	To   string
	Type EdgeType
}

// Snapshot 创建图快照
func (g *DynamicGraph[S]) Snapshot() *GraphSnapshot[S] {
	g.dynamicMu.RLock()
	defer g.dynamicMu.RUnlock()

	nodeNames := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		nodeNames = append(nodeNames, name)
	}

	edges := make([]EdgeSnapshot, len(g.Edges))
	for i, e := range g.Edges {
		edges[i] = EdgeSnapshot{
			From: e.From,
			To:   e.To,
			Type: e.Type,
		}
	}

	return &GraphSnapshot[S]{
		Name:      g.Name,
		NodeNames: nodeNames,
		Edges:     edges,
		Version:   g.version,
		Metadata:  g.Metadata,
	}
}
