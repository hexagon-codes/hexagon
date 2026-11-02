package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/hexagon-codes/hexagon/interrupt"
)

// GraphMode 决定构建期拓扑规则，运行入口不能临时切换它。
type GraphMode int

// GraphPersistenceError 表示派发或采用未取得持久确认，不能据此重跑节点。
type GraphPersistenceError struct{ Cause error }

func (e *GraphPersistenceError) Error() string {
	return fmt.Sprintf("graph persistence failed: %v", e.Cause)
}
func (e *GraphPersistenceError) Unwrap() error      { return e.Cause }
func (*GraphPersistenceError) UnsafeToReplay() bool { return true }

const (
	GraphModeDAG GraphMode = iota
	GraphModePregel
)

// WithExecutionMode 在 Build 前选择调度模式；零值为 DAG。
func (b *GraphBuilder[S]) WithExecutionMode(mode GraphMode) *GraphBuilder[S] {
	b.graph.mode = mode
	return b
}

// WithTriggerMode 在 Build 前选择前驱触发规则。
func (b *GraphBuilder[S]) WithTriggerMode(mode TriggerMode) *GraphBuilder[S] {
	b.graph.triggerMode, b.graph.triggerExplicit = mode, true
	return b
}

// WithStateMerger 为同一激活中的完整状态结果声明合并规则。
// 写集合节点使用字段 reducer，不经过完整状态合并器。
func (b *GraphBuilder[S]) WithStateMerger(merger StateMerger[S]) *GraphBuilder[S] {
	b.graph.merger = merger
	return b
}

// StateCodec 固定持久状态格式；Encode 返回合法 JSON，不兼容格式必须使用不同版本。
type StateCodec[S State] interface {
	Type() string
	Version() string
	Encode(S) ([]byte, error)
	Decode([]byte) (S, error)
}

// JSONStateCodec 适用于具有明确 JSON 合同的状态。
// interface 字段需要宿主提供保留具体类型的 codec。
type JSONStateCodec[S State] struct{}

func (JSONStateCodec[S]) Type() string    { return reflect.TypeFor[S]().String() }
func (JSONStateCodec[S]) Version() string { return "1" }
func (JSONStateCodec[S]) Encode(s S) ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var restored S
	if err := json.Unmarshal(data, &restored); err != nil {
		return nil, err
	}
	if err := sameConcreteTypes(reflect.ValueOf(s), reflect.ValueOf(restored)); err != nil {
		return nil, fmt.Errorf("JSON state codec cannot preserve dynamic types; configure a state codec: %w", err)
	}
	return data, nil
}
func (JSONStateCodec[S]) Decode(data []byte) (S, error) {
	var s S
	err := json.Unmarshal(data, &s)
	return s, err
}

func (b *GraphBuilder[S]) WithStateCodec(codec StateCodec[S]) *GraphBuilder[S] {
	b.graph.stateCodec = codec
	return b
}

func (b *GraphBuilder[S]) WithInterruptCodecs(codecs *interrupt.StateCodecs) *GraphBuilder[S] {
	b.graph.interruptCodecs = codecs
	return b
}

func (g *Graph[S]) codec() StateCodec[S] {
	var codec StateCodec[S] = JSONStateCodec[S]{}
	if g.stateCodec != nil {
		codec = g.stateCodec
	}
	return guardedStateCodec[S]{codec: codec, name: g.Name, typeName: g.codecType, version: g.codecVersion}
}

// AddConditionalEdgeWithDefault 显式声明未匹配标签的默认目标。
func (b *GraphBuilder[S]) AddConditionalEdgeWithDefault(from string, router RouterFunc[S], edges map[string]string, fallback string) *GraphBuilder[S] {
	b.AddConditionalEdge(from, router, edges)
	if b.err == nil {
		i := len(b.graph.conditionalEdges[from]) - 1
		b.graph.conditionalEdges[from][i].defaultTarget = fallback
	}
	return b
}

func appendUnique(values []string, value string) []string {
	for _, old := range values {
		if old == value {
			return values
		}
	}
	return append(values, value)
}

func planIdentity[S State](g *Graph[S], p *ExecutionPlan) string {
	var parts []string
	parts = append(parts, g.Name, fmt.Sprint(p.Mode), fmt.Sprint(p.TriggerMode), g.EntryPoint, g.codec().Type(), g.codec().Version())
	for _, name := range p.TopologicalOrder {
		parts = append(parts, name, strings.Join(p.Dependencies[name], ","))
		node := g.Nodes[name]
		parts = append(parts, fmt.Sprintf("timeout=%d", node.Timeout))
		if policy := node.RetryPolicy; policy != nil {
			parts = append(parts, fmt.Sprintf("retry=%d/%d/%d/%g", policy.MaxRetries, policy.InitialDelay, policy.MaxDelay, policy.Multiplier))
		}
		if io := node.IO; io != nil {
			parts = append(parts, io.InType.String(), io.OutType.String(), fmt.Sprint(io.Input), fmt.Sprint(io.Output))
		}
		if node.commandHandler != nil {
			parts = append(parts, "command")
		}
		var routes []string
		for _, edge := range g.conditionalEdges[name] {
			for label, to := range edge.edges {
				routes = append(routes, label+"="+to)
			}
			routes = append(routes, "default="+edge.defaultTarget)
		}
		sort.Strings(routes)
		parts = append(parts, routes...)
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:])
}

// selectSuccessors 合并普通边与全部路由后去重，普通边的完成不会被条件 skip 覆盖。
func (g *Graph[S]) selectSuccessors(name string, state S) (selected, skipped []string, err error) {
	return g.selectSuccessorsWithTarget(name, state, nil)
}

func (g *Graph[S]) selectSuccessorsWithTarget(name string, state S, commandTarget *string) (selected, skipped []string, err error) {
	if name == END {
		return nil, nil, nil
	}
	defer func() {
		if value := recover(); value != nil {
			err = &NodePanicError{Node: name, Value: value}
		}
	}()
	selected = append([]string(nil), g.adjacency[name]...)
	if commandTarget != nil {
		if _, ok := g.Nodes[*commandTarget]; !ok {
			return nil, nil, fmt.Errorf("command at node %q selected unknown node %q", name, *commandTarget)
		}
		selected = appendUnique(selected, *commandTarget)
	}
	if len(selected) == 0 && len(g.conditionalEdges[name]) == 0 {
		return nil, nil, fmt.Errorf("no outgoing edge from node %q", name)
	}
	var possible []string
	for _, edge := range g.conditionalEdges[name] {
		copy, cloneErr := state.Clone()
		if cloneErr != nil {
			return nil, nil, cloneErr
		}
		typed, ok := copy.(S)
		if !ok {
			return nil, nil, fmt.Errorf("router state clone type mismatch at %q", name)
		}
		label := edge.router(typed)
		target := label
		if edge.edges != nil {
			var ok bool
			target, ok = edge.edges[label]
			if !ok {
				target = edge.defaultTarget
			}
			if target == "" {
				return nil, nil, fmt.Errorf("router at node %q has no target for label %q", name, label)
			}
		}
		if _, ok := g.Nodes[target]; !ok {
			return nil, nil, fmt.Errorf("router at node %q selected unknown node %q", name, target)
		}
		selected = appendUnique(selected, target)
		for _, to := range edge.edges {
			possible = appendUnique(possible, to)
		}
		if edge.defaultTarget != "" {
			possible = appendUnique(possible, edge.defaultTarget)
		}
	}
	var unique []string
	for _, to := range selected {
		unique = appendUnique(unique, to)
	}
	selected = unique
	for _, to := range possible {
		found := false
		for _, active := range selected {
			if active == to {
				found = true
				break
			}
		}
		if !found {
			skipped = append(skipped, to)
		}
	}
	sort.Strings(selected)
	sort.Strings(skipped)
	return selected, skipped, nil
}

// graphFrontier 将执行位置、前驱身份与结果采用状态保存在同一快照中。
type graphFrontier struct {
	PlanID             string                                   `json:"plan_id"`
	CodecType          string                                   `json:"codec_type"`
	CodecVersion       string                                   `json:"codec_version"`
	Activation         uint64                                   `json:"activation"`
	Ready              []string                                 `json:"ready"`
	Completed          map[string]bool                          `json:"completed"`
	Skipped            map[string]bool                          `json:"skipped"`
	Arrivals           map[string]map[string]bool               `json:"arrivals"`
	Results            map[string]*savedNodeResult              `json:"results"`
	Waiting            map[string]*interrupt.InterruptSignal    `json:"-"`
	Signals            map[string]*interrupt.PersistedSignal    `json:"signals,omitempty"`
	ReadyGenerations   map[string][]uint64                      `json:"ready_generations"`
	Expected           map[string][]string                      `json:"expected"`
	ActivationArrivals map[string]map[string]predecessorArrival `json:"activation_arrivals"`
}

type predecessorArrival struct {
	SourceTaskID     string `json:"source_task_id"`
	SourceGeneration uint64 `json:"source_generation"`
	TargetGeneration uint64 `json:"target_generation"`
	// Skipped 为未选分支；旧记录没有该字段时仍表示实际完成到达。
	Skipped bool `json:"skipped,omitempty"`
}

type savedNodeResult struct {
	TaskID        string          `json:"task_id"`
	Node          string          `json:"node"`
	Activation    uint64          `json:"activation"`
	Output        json.RawMessage `json:"output"`
	Writes        []encodedWrite  `json:"writes,omitempty"`
	CommandTarget *string         `json:"command_target,omitempty"`
	Adopted       bool            `json:"adopted"`
}

type planExecutor[S State] struct {
	graph           *Graph[S]
	state           S
	frontier        *graphFrontier
	defaults        *RetryPolicy
	defaultTimeout  int64
	interruptBefore []string
	observe         func(StreamEvent[S]) error
	persist         func(context.Context, S, *graphFrontier) error
	beforeDispatch  func(context.Context, S, *graphFrontier, []string) error
	pregel          *PregelConfig
	outputs         map[string]S
	writes          map[string][]StateWrite
	failed          map[string]error
}

func newPlanExecutor[S State](g *Graph[S], initial S) *planExecutor[S] {
	if g.frozen != nil {
		g = g.frozen
	}
	f := &graphFrontier{PlanID: g.plan.Identity, CodecType: g.codec().Type(), CodecVersion: g.codec().Version(), Ready: []string{g.EntryPoint}, Completed: map[string]bool{}, Skipped: map[string]bool{}, Arrivals: map[string]map[string]bool{}, Results: map[string]*savedNodeResult{}, Waiting: map[string]*interrupt.InterruptSignal{}, ReadyGenerations: map[string][]uint64{g.EntryPoint: {0}}, Expected: map[string][]string{}, ActivationArrivals: map[string]map[string]predecessorArrival{}}
	return &planExecutor[S]{graph: g, state: initial, frontier: f, pregel: DefaultPregelConfig(), outputs: map[string]S{}, writes: map[string][]StateWrite{}, failed: map[string]error{}}
}

func (x *planExecutor[S]) taskID(node string) string {
	return x.taskIDFor(node, x.taskGeneration(node))
}

func (x *planExecutor[S]) taskGeneration(node string) uint64 {
	if x.graph.plan.Mode == GraphModePregel && x.graph.plan.TriggerMode == TriggerAllPredecessors {
		if generations := x.frontier.ReadyGenerations[node]; len(generations) > 0 {
			return generations[0]
		}
	}
	return x.frontier.Activation
}
func (x *planExecutor[S]) taskIDFor(node string, generation uint64) string {
	return fmt.Sprintf("%s/%d/%s", x.frontier.PlanID, generation, node)
}

func (x *planExecutor[S]) save(ctx context.Context) error {
	if x.persist != nil {
		if err := x.persist(ctx, x.state, x.frontier); err != nil {
			return &GraphPersistenceError{Cause: err}
		}
	}
	return nil
}

func (x *planExecutor[S]) emit(ctx context.Context, kind EventType, name string, state S, err error) error {
	if x.observe == nil {
		return nil
	}
	if (name == START || name == END) && (kind == EventTypeNodeStart || kind == EventTypeNodeEnd) {
		return nil
	}
	copy, cloneErr := state.Clone()
	if cloneErr != nil {
		return cloneErr
	}
	typed, ok := copy.(S)
	if !ok {
		return fmt.Errorf("event state clone has type %T, expected %T", copy, state)
	}
	return x.observe(StreamEvent[S]{Type: kind, NodeName: name, State: typed, Error: err})
}

func (x *planExecutor[S]) run(ctx context.Context) (S, error) {
	if x.frontier.PlanID != x.graph.plan.Identity {
		return x.state, fmt.Errorf("graph plan mismatch")
	}
	if x.graph.plan.Mode == GraphModePregel {
		return x.runPregel(ctx)
	}
	x.activateResumeTargets(ctx)
	for len(x.frontier.Ready) > 0 {
		if err := ctx.Err(); err != nil {
			return x.state, err
		}
		x.sortReady()
		name := x.frontier.Ready[0]
		if err := x.runOne(ctx, name); err != nil {
			if _, ok := interrupt.IsInterruptSignal(err); ok {
				x.frontier.Ready = x.frontier.Ready[1:]
				continue
			}
			return x.state, err
		}
	}
	if len(x.frontier.Waiting) > 0 {
		return x.state, x.waitingError(ctx)
	}
	for name, incoming := range x.frontier.Arrivals {
		if !x.frontier.Completed[name] && !x.frontier.Skipped[name] && len(incoming) > 0 {
			return x.state, fmt.Errorf("graph scheduling stalled at node %q: unresolved predecessors", name)
		}
	}
	return x.state, nil
}

func (x *planExecutor[S]) sortReady() {
	index := map[string]int{}
	for i, name := range x.graph.plan.TopologicalOrder {
		index[name] = i
	}
	sort.SliceStable(x.frontier.Ready, func(i, j int) bool { return index[x.frontier.Ready[i]] < index[x.frontier.Ready[j]] })
}

func (x *planExecutor[S]) runOne(ctx context.Context, name string) error {
	for _, target := range x.interruptBefore {
		if target == name {
			return fmt.Errorf("interrupted at node: %s", name)
		}
	}
	id := x.taskID(name)
	result := x.frontier.Results[id]
	if result == nil {
		if x.beforeDispatch != nil {
			if err := x.beforeDispatch(ctx, x.state, x.frontier, []string{name}); err != nil {
				return &GraphPersistenceError{Cause: err}
			}
		}
		if err := x.emit(ctx, EventTypeNodeStart, name, x.state, nil); err != nil {
			return err
		}
		nodeCtx := interrupt.AppendAddressSegment(ctx, interrupt.SegmentNode, name, "")
		output, writes, commandTarget, err := x.execute(ctx, nodeCtx, name, x.state)
		if err != nil {
			if signal, ok := interrupt.IsInterruptSignal(err); ok {
				x.frontier.Waiting[name] = signal
			} else {
				x.failed[id] = err
			}
			return fmt.Errorf("node %q failed: %w", name, err)
		}
		var data []byte
		if x.persist != nil {
			data, err = x.graph.codec().Encode(output)
			if err != nil {
				return fmt.Errorf("encode node %q result: %w", name, err)
			}
		}
		x.outputs[id] = output
		x.writes[id] = writes
		encodedWrites, err := encodeWrites(writes)
		if err != nil {
			return err
		}
		result = &savedNodeResult{TaskID: id, Node: name, Activation: x.taskGeneration(name), Output: data, Writes: encodedWrites, CommandTarget: commandTarget}
		x.frontier.Results[id] = result
		// 完成结果先保存，采用失败或进程重启不要求重新执行处理器。
		if err := x.save(ctx); err != nil {
			return err
		}
	}
	nextState := x.state
	if !result.Adopted {
		output, err := x.resultState(result)
		if err != nil {
			return fmt.Errorf("decode node %q result: %w", name, err)
		}
		if x.graph.Nodes[name].IO != nil {
			writes, err := x.resultWrites(result)
			if err != nil {
				return err
			}
			output, err = x.graph.applyWrites(x.state, writes)
			if err != nil {
				return err
			}
		}
		nextState = output
	}
	selected, skipped, err := x.graph.selectSuccessorsWithTarget(name, nextState, result.CommandTarget)
	if err != nil {
		return err
	}
	for _, to := range selected {
		if to != END && x.frontier.Completed[to] {
			return fmt.Errorf("DAG route from %q reactivates completed node %q", name, to)
		}
	}
	x.state = nextState
	result.Adopted = true
	x.frontier.Ready = x.frontier.Ready[1:]
	x.frontier.Completed[name] = true
	delete(x.frontier.Waiting, name)
	for _, to := range selected {
		x.arrive(to, name, true)
	}
	for _, to := range skipped {
		x.arrive(to, name, false)
	}
	if err := x.save(ctx); err != nil {
		return err
	}
	return x.emit(ctx, EventTypeNodeEnd, name, x.state, nil)
}

func (x *planExecutor[S]) execute(ctx, nodeCtx context.Context, name string, input S) (S, []StateWrite, *string, error) {
	node, ok := x.graph.Nodes[name]
	if !ok {
		return input, nil, nil, fmt.Errorf("node %q not found", name)
	}
	if node.Timeout == 0 && x.defaultTimeout != 0 {
		copy := *node
		copy.Timeout = x.defaultTimeout
		node = &copy
	}
	if node.Type == NodeTypeBarrier {
		if merger, ok := node.Metadata["__barrier_merger"].(BarrierMerger[S]); ok && merger != nil {
			outputs := map[string]S{}
			for _, pred := range x.graph.plan.Dependencies[name] {
				if x.graph.Nodes[pred].IO != nil {
					continue
				}
				if r := x.predecessorResult(pred); r != nil {
					s, err := x.resultState(r)
					if err != nil {
						return input, nil, nil, err
					}
					outputs[pred] = s
				}
			}
			result, err := runIsolatedNode(nodeCtx, name, func(_ context.Context, s S) (S, error) { return merger(s, outputs), nil }, input)
			return result, nil, nil, err
		}
	}
	if node.IO != nil {
		local := *node
		var writes []StateWrite
		local.Handler = func(callCtx context.Context, s S) (S, error) {
			var err error
			writes, err = invokeMapped(callCtx, node, s)
			return s, err
		}
		output, err := executeNodeHandler(nodeCtx, &local, input, x.defaults)
		return output, writes, nil, err
	}
	output, commandTarget, err := executeCommandNode(nodeCtx, node, input, x.defaults)
	return output, nil, commandTarget, err
}

func (x *planExecutor[S]) predecessorResult(name string) *savedNodeResult {
	var latest *savedNodeResult
	for _, result := range x.frontier.Results {
		if result.Node == name && result.Activation <= x.frontier.Activation && (latest == nil || result.Activation > latest.Activation) {
			latest = result
		}
	}
	return latest
}

func (x *planExecutor[S]) resultState(result *savedNodeResult) (S, error) {
	if output, ok := x.outputs[result.TaskID]; ok {
		copy, err := output.Clone()
		if err != nil {
			return output, err
		}
		typed, ok := copy.(S)
		if !ok {
			return output, fmt.Errorf("saved result clone has type %T, expected %T", copy, output)
		}
		return typed, nil
	}
	return x.graph.codec().Decode(result.Output)
}

func (x *planExecutor[S]) arrive(to, from string, selected bool) {
	if x.frontier.Completed[to] || x.frontier.Skipped[to] {
		return
	}
	if x.frontier.Arrivals[to] == nil {
		x.frontier.Arrivals[to] = map[string]bool{}
	}
	if _, exists := x.frontier.Arrivals[to][from]; exists {
		return
	}
	x.frontier.Arrivals[to][from] = selected
	all := true
	any := false
	for _, pred := range x.graph.plan.Dependencies[to] {
		value, ok := x.frontier.Arrivals[to][pred]
		all = all && ok
		any = any || value
	}
	if len(x.graph.plan.Dependencies[to]) == 0 {
		all = true
		any = selected
	}
	if x.graph.plan.TriggerMode == TriggerAnyPredecessor && selected {
		x.frontier.Ready = appendUnique(x.frontier.Ready, to)
		return
	}
	if !all {
		return
	}
	if any {
		x.frontier.Ready = appendUnique(x.frontier.Ready, to)
		return
	}
	x.frontier.Skipped[to] = true
	for _, next := range x.graph.allSuccessors(to) {
		x.arrive(next, to, false)
	}
}

func (g *Graph[S]) allSuccessors(from string) []string {
	all := append([]string(nil), g.adjacency[from]...)
	for _, edge := range g.conditionalEdges[from] {
		for _, to := range edge.edges {
			all = appendUnique(all, to)
		}
		if edge.defaultTarget != "" {
			all = appendUnique(all, edge.defaultTarget)
		}
	}
	sort.Strings(all)
	return all
}

func (x *planExecutor[S]) resultWrites(result *savedNodeResult) ([]StateWrite, error) {
	if writes, ok := x.writes[result.TaskID]; ok {
		return writes, nil
	}
	return decodeWrites(x.graph.Nodes[result.Node].IO, result.Writes)
}
