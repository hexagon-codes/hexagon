package graph

import (
	"context"
	"fmt"

	"github.com/hexagon-codes/hexagon/interrupt"
)

func (x *planExecutor[S]) runPregel(ctx context.Context) (S, error) {
	x.activateResumeTargets(ctx)
	for len(x.frontier.Ready) > 0 {
		if err := ctx.Err(); err != nil {
			return x.state, err
		}
		if int(x.frontier.Activation) >= x.pregel.MaxSupersteps {
			return x.state, fmt.Errorf("max supersteps (%d) exceeded", x.pregel.MaxSupersteps)
		}
		x.sortReady()
		names := append([]string(nil), x.frontier.Ready...)
		generations := map[string]uint64{}
		taskIDs := map[string]string{}
		for _, name := range names {
			generations[name] = x.taskGeneration(name)
			taskIDs[name] = x.taskID(name)
		}
		if x.pregel.TerminationCheck != nil && x.pregel.TerminationCheck(int(x.frontier.Activation), names) {
			return x.state, nil
		}
		type outcome struct {
			name          string
			state         S
			err           error
			writes        []StateWrite
			commandTarget *string
		}
		completed := make(chan outcome, len(names))
		// 任务只借用本轮派发前的结果视图，收集线程可独立保存新完成事实。
		worker := *x
		view := *x.frontier
		view.Results = make(map[string]*savedNodeResult, len(x.frontier.Results))
		for id, result := range x.frontier.Results {
			view.Results[id] = result
		}
		worker.frontier = &view
		worker.outputs = make(map[string]S, len(x.outputs))
		for id, state := range x.outputs {
			worker.outputs[id] = state
		}
		count := 0
		if x.beforeDispatch != nil {
			if err := x.beforeDispatch(ctx, x.state, x.frontier, names); err != nil {
				return x.state, &GraphPersistenceError{Cause: err}
			}
		}
		for _, name := range names {
			if x.frontier.Results[x.taskID(name)] != nil {
				continue
			}
			if signal := x.frontier.Waiting[name]; signal != nil {
				selected := false
				for _, point := range interrupt.ToInterruptContexts(signal) {
					selected = selected || interrupt.IsResumeRequested(ctx, point.ID)
				}
				if !selected {
					continue
				}
			}
			if err := x.emit(ctx, EventTypeNodeStart, name, x.state, nil); err != nil {
				return x.state, err
			}
			count++
			invoke := func(name string) {
				nodeCtx := interrupt.AppendAddressSegment(ctx, interrupt.SegmentNode, name, "")
				state, writes, commandTarget, err := worker.execute(ctx, nodeCtx, name, x.state)
				completed <- outcome{name: name, state: state, err: err, writes: writes, commandTarget: commandTarget}
			}
			if x.pregel.ParallelExecution {
				go invoke(name)
			} else {
				invoke(name)
			}
		}
		var firstError error
		var signals []error
		for i := 0; i < count; i++ {
			result := <-completed
			if result.err != nil {
				if signal, ok := interrupt.IsInterruptSignal(result.err); ok {
					x.frontier.Waiting[result.name] = signal
					signals = append(signals, signal)
				} else if firstError == nil {
					firstError = fmt.Errorf("node %q failed: %w", result.name, result.err)
				}
				if _, ok := interrupt.IsInterruptSignal(result.err); !ok {
					x.failed[x.taskID(result.name)] = result.err
				}
				continue
			}
			id := x.taskID(result.name)
			x.outputs[id] = result.state
			delete(x.frontier.Waiting, result.name)
			x.writes[id] = result.writes
			var data []byte
			if x.persist != nil {
				var err error
				data, err = x.graph.codec().Encode(result.state)
				if err != nil && firstError == nil {
					firstError = err
				}
			}
			encoded, err := encodeWrites(result.writes)
			if err != nil && firstError == nil {
				firstError = err
			}
			x.frontier.Results[id] = &savedNodeResult{TaskID: id, Node: result.name, Activation: generations[result.name], Output: data, Writes: encoded, CommandTarget: result.commandTarget}
			if err := x.save(ctx); err != nil && firstError == nil {
				firstError = err
			}
		}
		if firstError != nil {
			return x.state, firstError
		}
		if len(signals) > 0 || len(x.frontier.Waiting) > 0 {
			return x.state, x.waitingError(ctx)
		}
		var states []S
		for _, name := range names {
			if name != START && name != END && x.graph.Nodes[name].IO == nil {
				state, err := x.resultState(x.frontier.Results[x.taskID(name)])
				if err != nil {
					return x.state, err
				}
				states = append(states, state)
			}
		}
		if len(states) > 1 && x.graph.merger == nil {
			return x.state, fmt.Errorf("parallel full-state results %v require an explicit state merger", names)
		}
		nextState := x.state
		if len(states) == 1 {
			nextState = states[0]
		} else if len(states) > 1 {
			merged, err := runIsolatedNode(ctx, "graph merger", func(_ context.Context, base S) (S, error) { return x.graph.merger(base, states), nil }, x.state)
			if err != nil {
				return x.state, err
			}
			nextState = merged
		}
		for _, name := range names {
			if x.graph.Nodes[name].IO != nil {
				writes, err := x.resultWrites(x.frontier.Results[x.taskID(name)])
				if err != nil {
					return x.state, err
				}
				next, err := x.graph.applyWrites(nextState, writes)
				if err != nil {
					return x.state, err
				}
				nextState = next
			}
		}
		routes := map[string][]string{}
		for _, name := range names {
			selected, _, err := x.graph.selectSuccessorsWithTarget(name, nextState, x.frontier.Results[taskIDs[name]].CommandTarget)
			if err != nil {
				return x.state, err
			}
			routes[name] = selected
		}
		x.state = nextState
		x.frontier.Ready = nil
		for _, name := range names {
			x.frontier.Results[taskIDs[name]].Adopted = true
			x.frontier.Completed[name] = true
			if x.graph.plan.TriggerMode == TriggerAllPredecessors {
				queue := x.frontier.ReadyGenerations[name]
				if len(queue) > 0 {
					x.frontier.ReadyGenerations[name] = queue[1:]
					if len(queue) > 1 {
						x.frontier.Ready = appendUnique(x.frontier.Ready, name)
					}
				}
			}
			if name == END {
				continue
			}
			for _, to := range routes[name] {
				if x.graph.plan.TriggerMode == TriggerAnyPredecessor {
					x.frontier.Ready = appendUnique(x.frontier.Ready, to)
					continue
				}
				if err := x.arrivePregelAll(name, to, generations[name], taskIDs[name]); err != nil {
					return x.state, err
				}
			}
		}
		x.frontier.Activation++
		if err := x.save(ctx); err != nil {
			return x.state, err
		}
		for _, name := range names {
			if err := x.emit(ctx, EventTypeNodeEnd, name, x.state, nil); err != nil {
				return x.state, err
			}
		}
	}
	for name, preds := range x.frontier.Arrivals {
		if len(preds) > 0 {
			return x.state, fmt.Errorf("Pregel scheduling stalled at node %q: unresolved predecessors", name)
		}
	}
	for id, preds := range x.frontier.ActivationArrivals {
		if result := x.frontier.Results[id]; (result == nil || !result.Adopted) && len(preds) > 0 {
			return x.state, fmt.Errorf("Pregel scheduling stalled at activation %q: unresolved predecessors", id)
		}
	}
	return x.state, nil
}
