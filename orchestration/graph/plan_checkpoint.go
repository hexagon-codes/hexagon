package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hexagon-codes/hexagon/interrupt"
)

func (g *Graph[S]) encodeFrontier(frontier *graphFrontier) ([]byte, error) {
	copy := *frontier
	copy.Signals = map[string]*interrupt.PersistedSignal{}
	for name, signal := range frontier.Waiting {
		saved, err := interrupt.EncodeSignal(signal, g.interruptCodecs)
		if err != nil {
			return nil, fmt.Errorf("encode waiting node %q: %w", name, err)
		}
		copy.Signals[name] = saved
	}
	return json.Marshal(copy)
}

func (g *Graph[S]) decodeFrontier(data []byte) (*graphFrontier, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("checkpoint is missing its graph execution specification")
	}
	var f graphFrontier
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decode graph frontier: %w", err)
	}
	if g.plan == nil || f.PlanID != g.plan.Identity {
		return nil, fmt.Errorf("checkpoint graph plan mismatch")
	}
	if f.CodecType != g.codec().Type() || f.CodecVersion != g.codec().Version() {
		return nil, fmt.Errorf("checkpoint state codec mismatch: %s/%s", f.CodecType, f.CodecVersion)
	}
	if f.Completed == nil || f.Skipped == nil || f.Arrivals == nil || f.Results == nil {
		return nil, fmt.Errorf("checkpoint graph frontier is incomplete")
	}
	if f.ReadyGenerations == nil || f.Expected == nil || f.ActivationArrivals == nil {
		return nil, fmt.Errorf("checkpoint activation specification is incomplete")
	}
	f.Waiting = map[string]*interrupt.InterruptSignal{}
	for name, saved := range f.Signals {
		signal, err := interrupt.DecodeSignal(saved, g.interruptCodecs)
		if err != nil {
			return nil, fmt.Errorf("decode waiting node %q: %w", name, err)
		}
		f.Waiting[name] = signal
	}
	for id, result := range f.Results {
		if result == nil || result.TaskID != id || result.Node == "" {
			return nil, fmt.Errorf("checkpoint task result identity mismatch")
		}
		if _, err := g.codec().Decode(result.Output); err != nil {
			return nil, fmt.Errorf("decode saved task %q: %w", id, err)
		}
	}
	return &f, nil
}

func (x *planExecutor[S]) activateResumeTargets(ctx context.Context) {
	for name, signal := range x.frontier.Waiting {
		for _, point := range interrupt.ToInterruptContexts(signal) {
			if interrupt.IsResumeRequested(ctx, point.ID) {
				x.frontier.Ready = appendUnique(x.frontier.Ready, name)
				break
			}
		}
	}
}

func (x *planExecutor[S]) waitingError(ctx context.Context) error {
	names := make([]string, 0, len(x.frontier.Waiting))
	for name := range x.frontier.Waiting {
		names = append(names, name)
	}
	sort.Strings(names)
	var signals []error
	for _, name := range names {
		signals = append(signals, x.frontier.Waiting[name])
	}
	return interrupt.CompositeInterrupt(ctx, "graph is waiting", nil, signals...)
}

func (r *CheckpointRunner[S]) saveFrontier(ctx context.Context, state S, frontier *graphFrontier) error {
	encoded, err := r.graph.codec().Encode(state)
	if err != nil {
		return fmt.Errorf("encode checkpoint state: %w", err)
	}
	data, err := r.graph.encodeFrontier(frontier)
	if err != nil {
		return err
	}
	r.currentCheckpoint.State = encoded
	r.currentCheckpoint.Frontier = data
	r.currentCheckpoint.PendingNodes = append([]string(nil), frontier.Ready...)
	r.currentCheckpoint.CompletedNodes = nil
	for _, name := range r.graph.plan.TopologicalOrder {
		if frontier.Completed[name] {
			r.currentCheckpoint.CompletedNodes = append(r.currentCheckpoint.CompletedNodes, name)
		}
	}
	return r.saver.SaveEnhanced(ctx, r.currentCheckpoint)
}

// ResumeTargets 恢复指定中断身份；未选中的等待点保留原身份和状态。
func (r *CheckpointRunner[S]) ResumeTargets(ctx context.Context, checkpointID string, targets map[string]any) (S, error) {
	var zero S
	cp, err := r.saver.LoadEnhancedByID(ctx, checkpointID)
	if err != nil {
		return zero, err
	}
	frontier, err := r.graph.decodeFrontier(cp.Frontier)
	if err != nil {
		return zero, err
	}
	addresses := map[string]interrupt.Address{}
	states := map[string]any{}
	for _, signal := range frontier.Waiting {
		addrs, values := interrupt.SignalToPersistenceMaps(signal)
		for id, addr := range addrs {
			addresses[id] = addr
		}
		for id, value := range values {
			states[id] = value
		}
	}
	for id := range targets {
		if _, ok := addresses[id]; !ok {
			return zero, fmt.Errorf("resume target %q does not belong to checkpoint", id)
		}
	}
	ctx = interrupt.PopulateResumeInfo(ctx, addresses, states)
	ctx = interrupt.BatchResumeWithData(ctx, targets)
	return r.Resume(ctx, checkpointID)
}
