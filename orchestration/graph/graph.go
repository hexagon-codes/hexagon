// Package graph 提供 Hexagon AI Agent 框架的图编排引擎
//
// Graph 编排引擎允许将多个节点（Agent、Tool、函数）组织成有向图，
// 支持条件路由、并行执行、检查点恢复等高级特性。
//
// 基本用法：
//
//	graph := NewGraph[MyState]("my-graph").
//	    AddNode("step1", step1Handler).
//	    AddNode("step2", step2Handler).
//	    AddEdge(START, "step1").
//	    AddEdge("step1", "step2").
//	    AddEdge("step2", END).
//	    Build()
//
//	result, err := graph.Run(ctx, initialState)
package graph

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hexagon-codes/hexagon/interrupt"
	hr "github.com/hexagon-codes/hexagon/runtime"
)

// Graph 图定义
type Graph[S State] struct {
	// Name 图名称
	Name string

	// Nodes 节点映射
	Nodes map[string]*Node[S]

	// Edges 边列表
	Edges []*Edge

	// EntryPoint 入口点
	EntryPoint string

	// Checkpointer 检查点保存器
	Checkpointer CheckpointSaver

	// Metadata 元数据
	Metadata map[string]any

	// compiled 是否已编译
	compiled bool

	// adjacency 邻接表（编译后生成）
	adjacency map[string][]string

	// conditionalEdges 条件边映射
	conditionalEdges    map[string][]conditionalEdge[S]
	mode                GraphMode
	triggerMode         TriggerMode
	triggerExplicit     bool
	plan                *ExecutionPlan
	merger              StateMerger[S]
	stateCodec          StateCodec[S]
	interruptCodecs     *interrupt.StateCodecs
	fieldReducers       map[string]FieldReducer
	frozen              *Graph[S]
	definitionReference *hr.ContentReference
	codecType           string
	codecVersion        string
}

// conditionalEdge 条件边内部表示
type conditionalEdge[S State] struct {
	router        RouterFunc[S]
	edges         map[string]string // label -> target
	defaultTarget string
}

// GraphBuilder 图构建器
type GraphBuilder[S State] struct {
	graph *Graph[S]
	err   error
}

// NewGraph 创建图构建器
func NewGraph[S State](name string) *GraphBuilder[S] {
	return &GraphBuilder[S]{
		graph: &Graph[S]{
			Name:             name,
			Nodes:            make(map[string]*Node[S]),
			Edges:            make([]*Edge, 0),
			Metadata:         make(map[string]any),
			adjacency:        make(map[string][]string),
			conditionalEdges: make(map[string][]conditionalEdge[S]),
		},
	}
}

// AddNode 添加节点
func (b *GraphBuilder[S]) AddNode(name string, handler NodeHandler[S]) *GraphBuilder[S] {
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

	b.graph.Nodes[name] = &Node[S]{
		Name:     name,
		Type:     NodeTypeNormal,
		Handler:  handler,
		Metadata: make(map[string]any),
	}
	return b
}

// AddNodeWithBuilder 使用构建器添加节点
func (b *GraphBuilder[S]) AddNodeWithBuilder(node *Node[S]) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	if node.Name == START || node.Name == END {
		b.err = fmt.Errorf("cannot use reserved node name: %s", node.Name)
		return b
	}

	if _, exists := b.graph.Nodes[node.Name]; exists {
		b.err = fmt.Errorf("node %s already exists", node.Name)
		return b
	}

	b.graph.Nodes[node.Name] = node
	return b
}

// AddEdge 添加边
func (b *GraphBuilder[S]) AddEdge(from, to string) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	b.graph.Edges = append(b.graph.Edges, &Edge{
		From: from,
		To:   to,
		Type: EdgeTypeNormal,
	})
	return b
}

// AddConditionalEdge 添加条件边
// router 函数返回目标节点的标签
// edges 是标签到目标节点的映射
func (b *GraphBuilder[S]) AddConditionalEdge(from string, router RouterFunc[S], edges map[string]string) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}

	if b.graph.conditionalEdges[from] == nil {
		b.graph.conditionalEdges[from] = make([]conditionalEdge[S], 0)
	}

	b.graph.conditionalEdges[from] = append(b.graph.conditionalEdges[from], conditionalEdge[S]{
		router: router,
		edges:  edges,
	})

	return b
}

// SetEntryPoint 设置入口点
func (b *GraphBuilder[S]) SetEntryPoint(node string) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}
	b.graph.EntryPoint = node
	return b
}

// SetFinishPoint 设置结束点（添加到 END 的边）
func (b *GraphBuilder[S]) SetFinishPoint(nodes ...string) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}
	for _, node := range nodes {
		b.AddEdge(node, END)
	}
	return b
}

// WithCheckpointer 设置检查点保存器
func (b *GraphBuilder[S]) WithCheckpointer(saver CheckpointSaver) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}
	b.graph.Checkpointer = saver
	return b
}

// WithMetadata 设置元数据
func (b *GraphBuilder[S]) WithMetadata(key string, value any) *GraphBuilder[S] {
	if b.err != nil {
		return b
	}
	b.graph.Metadata[key] = value
	return b
}

// Build 构建图
func (b *GraphBuilder[S]) Build() (graph *Graph[S], err error) {
	defer func() {
		if value := recover(); value != nil {
			graph = nil
			err = &NodePanicError{Node: "graph definition " + b.graph.Name, Value: value}
		}
	}()
	if b.err != nil {
		return nil, b.err
	}

	// 添加 START 和 END 节点
	b.graph.Nodes[START] = StartNode[S]()
	b.graph.Nodes[END] = EndNode[S]()

	// 编译图
	if err := b.graph.compile(); err != nil {
		return nil, err
	}

	return b.graph, nil
}

// MustBuild 构建图，失败时 panic
//
// ⚠️ 警告：构建失败时会 panic。
// 仅在初始化时使用，不要在运行时调用。
// 推荐使用 Build() 方法并正确处理错误。
//
// 使用场景：
//   - 程序启动时的全局初始化
//   - 测试代码中
func (b *GraphBuilder[S]) MustBuild() *Graph[S] {
	g, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("graph build failed: %v", err))
	}
	return g
}

// compile 编译图
func (g *Graph[S]) compile() (err error) {
	// 身份回调保护位于共同入口，公开 Compile 和动态图构建也遵守同一错误合同。
	defer func() {
		if value := recover(); value != nil {
			err = &NodePanicError{Node: "graph definition " + g.Name, Value: value}
		}
	}()
	codec := g.stateCodec
	if codec == nil {
		codec = JSONStateCodec[S]{}
	}
	g.codecType, g.codecVersion = codec.Type(), codec.Version()
	if g.interruptCodecs == nil {
		g.interruptCodecs = interrupt.NewStateCodecs()
	}
	if err := interrupt.RegisterJSONState[*graphContinuation](g.interruptCodecs, "graph.continuation", "1"); err != nil {
		return err
	}
	g.adjacency = make(map[string][]string)
	// 构建邻接表
	for _, edge := range g.Edges {
		g.adjacency[edge.From] = append(g.adjacency[edge.From], edge.To)
	}

	// 验证图
	if err := g.validate(); err != nil {
		return err
	}

	// START 保留全部入口分支，不把第一条出边当成唯一入口。
	if g.EntryPoint == "" {
		g.EntryPoint = START
	}
	if _, ok := g.Nodes[g.EntryPoint]; !ok {
		return fmt.Errorf("entry node %q not found", g.EntryPoint)
	}
	plan, err := computeExecutionPlan(g)
	if err != nil {
		return err
	}
	g.plan = plan
	if err := g.validateWriteConflicts(); err != nil {
		return err
	}
	g.freezeDefinition()

	g.compiled = true
	return nil
}

// validate 验证图
func (g *Graph[S]) validate() error {
	for name, node := range g.Nodes {
		if node == nil || (node.Handler == nil && node.IO == nil) {
			return fmt.Errorf("node %q has no handler or typed I/O adapter", name)
		}
		if node.IO != nil {
			if err := validateNodeIO[S](name, node.IO); err != nil {
				return err
			}
		}
	}
	// 检查所有边引用的节点是否存在
	for _, edge := range g.Edges {
		if _, ok := g.Nodes[edge.From]; !ok {
			return fmt.Errorf("node %s not found (referenced in edge)", edge.From)
		}
		if _, ok := g.Nodes[edge.To]; !ok {
			return fmt.Errorf("node %s not found (referenced in edge)", edge.To)
		}
	}

	// 检查条件边引用的节点
	for from, condEdges := range g.conditionalEdges {
		if _, ok := g.Nodes[from]; !ok {
			return fmt.Errorf("node %s not found (referenced in conditional edge)", from)
		}
		for _, ce := range condEdges {
			if ce.defaultTarget != "" {
				if _, ok := g.Nodes[ce.defaultTarget]; !ok {
					return fmt.Errorf("node %s not found (conditional default target)", ce.defaultTarget)
				}
			}
			for _, to := range ce.edges {
				if _, ok := g.Nodes[to]; !ok {
					return fmt.Errorf("node %s not found (referenced in conditional edge target)", to)
				}
			}
		}
	}

	return nil
}

// Run 执行图
func (g *Graph[S]) Run(ctx context.Context, initialState S, opts ...RunOption) (S, error) {
	if !g.compiled {
		return initialState, fmt.Errorf("graph not compiled")
	}

	config := &runConfig{}
	for _, opt := range opts {
		opt(config)
	}

	// 创建执行器
	executor := &graphExecutor[S]{
		graph:   g,
		state:   initialState,
		visited: make(map[string]bool),
		config:  config,
	}

	return executor.run(ctx)
}

// RunOption 运行选项
type RunOption func(*runConfig)

type runConfig struct {
	threadConfig *ThreadConfig
	interrupt    []string
	debug        bool
	retryPolicy  *RetryPolicy
	timeoutMS    int64
}

// WithNodeRetryPolicy 设置本次执行的默认策略；节点非 nil 策略优先，零次重试只调用一次。
func WithNodeRetryPolicy(policy *RetryPolicy) RunOption {
	return func(c *runConfig) { c.retryPolicy = policy }
}

// WithNodeTimeout 设置节点继承的毫秒时限，父 context 的期限始终优先。
func WithNodeTimeout(milliseconds int64) RunOption {
	return func(c *runConfig) { c.timeoutMS = milliseconds }
}

// WithThread 设置线程配置
func WithThread(config *ThreadConfig) RunOption {
	return func(c *runConfig) {
		c.threadConfig = config
	}
}

// WithInterrupt 设置中断节点
func WithInterrupt(nodes ...string) RunOption {
	return func(c *runConfig) {
		c.interrupt = nodes
	}
}

// WithDebug 设置调试模式
func WithDebug(debug bool) RunOption {
	return func(c *runConfig) {
		c.debug = debug
	}
}

// graphExecutor 图执行器
type graphExecutor[S State] struct {
	graph   *Graph[S]
	state   S
	visited map[string]bool
	config  *runConfig
	mu      sync.Mutex
}

// run 执行图
func (e *graphExecutor[S]) run(ctx context.Context) (S, error) {
	x := newPlanExecutor(e.graph, e.state)
	x.defaults = e.config.retryPolicy
	x.defaultTimeout = e.config.timeoutMS
	x.interruptBefore = e.config.interrupt
	return x.run(ctx)
}

// getNextNode 获取下一个节点
func (e *graphExecutor[S]) getNextNode(currentNode string) (string, error) {
	// 先检查条件边
	if condEdges, ok := e.graph.conditionalEdges[currentNode]; ok && len(condEdges) > 0 {
		for _, ce := range condEdges {
			label := ce.router(e.state)
			if ce.edges == nil {
				// 动态路由（如 Command 节点）：router 返回值直接作为目标节点名
				return label, nil
			}
			if target, ok := ce.edges[label]; ok {
				return target, nil
			}
		}
	}

	// 检查普通边
	if targets, ok := e.graph.adjacency[currentNode]; ok && len(targets) > 0 {
		return targets[0], nil
	}

	return "", fmt.Errorf("no outgoing edge from node %s", currentNode)
}

// Stream 流式执行图（返回每个节点的输出）
//
// 返回的 channel 会在以下情况关闭：
//   - 图执行完成
//   - 发生错误
//   - context 被取消
//
// 调用者必须消费返回的 channel，否则可能导致 goroutine 泄露。
// 如果不需要继续消费，应取消传入的 context。
func (g *Graph[S]) Stream(ctx context.Context, initialState S, opts ...RunOption) (<-chan StreamEvent[S], error) {
	if !g.compiled {
		return nil, fmt.Errorf("graph not compiled")
	}

	events := make(chan StreamEvent[S], 10)
	go func() {
		defer close(events)
		config := &runConfig{}
		for _, opt := range opts {
			opt(config)
		}
		x := newPlanExecutor(g, initialState)
		x.defaults = config.retryPolicy
		x.defaultTimeout = config.timeoutMS
		x.interruptBefore = config.interrupt
		var sendMu sync.Mutex
		x.observe = func(event StreamEvent[S]) error {
			sendMu.Lock()
			defer sendMu.Unlock()
			// 为终止错误保留一个槽位，使取消不依赖消费者继续读取。
			var ticker *time.Ticker
			defer func() {
				if ticker != nil {
					ticker.Stop()
				}
			}()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				if len(events) < cap(events)-1 {
					events <- event
					return nil
				}
				if ticker == nil {
					ticker = time.NewTicker(time.Millisecond)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
			}
		}
		state, err := x.run(ctx)
		if err == nil {
			err = x.emit(ctx, EventTypeEnd, "", state, nil)
		}
		if err != nil {
			events <- StreamEvent[S]{Type: EventTypeError, State: state, Error: err}
		}
	}()

	return events, nil
}

// StreamEvent 流事件
type StreamEvent[S State] struct {
	// Type 事件类型
	Type EventType

	// NodeName 节点名称
	NodeName string

	// State 当前状态
	State S

	// Error 错误（仅用于 EventTypeError）
	Error error

	// Metadata 元数据
	Metadata map[string]any
}

// EventType 事件类型
type EventType int

const (
	// EventTypeNodeStart 节点开始
	EventTypeNodeStart EventType = iota
	// EventTypeNodeEnd 节点结束
	EventTypeNodeEnd
	// EventTypeError 错误
	EventTypeError
	// EventTypeEnd 图执行结束
	EventTypeEnd
)

// String 返回事件类型的字符串表示
func (t EventType) String() string {
	switch t {
	case EventTypeNodeStart:
		return "node_start"
	case EventTypeNodeEnd:
		return "node_end"
	case EventTypeError:
		return "error"
	case EventTypeEnd:
		return "end"
	default:
		return "unknown"
	}
}
