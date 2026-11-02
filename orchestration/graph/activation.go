package graph

import (
	"fmt"
	"sort"
)

// arrivePregelAll 将到达绑定到目标代次，不把旧代次或重复前驱用于另一份激活。
func (x *planExecutor[S]) arrivePregelAll(from, to string, sourceGeneration uint64, sourceTaskID string) error {
	return x.arrivePregelAllOutcome(from, to, sourceGeneration, sourceTaskID, true)
}

func (x *planExecutor[S]) arrivePregelAllOutcome(from, to string, sourceGeneration uint64, sourceTaskID string, selected bool) error {
	if sourceTaskID != x.taskIDFor(from, sourceGeneration) {
		return fmt.Errorf("predecessor %q activation identity mismatch", from)
	}
	plan := x.graph.plan
	generation := sourceGeneration
	sameComponent := plan.Components[from] == plan.Components[to]
	loopEntry := plan.CyclicComponents[plan.Components[to]] && plan.ComponentEntries[plan.Components[to]] == to
	if sameComponent && loopEntry {
		generation++
	}
	id := x.taskIDFor(to, generation)
	expected := plan.Dependencies[to]
	if plan.CyclicComponents[plan.Components[to]] {
		if plan.ComponentEntries[plan.Components[to]] == to && generation == 0 {
			expected = plan.ExternalPredecessors[to]
		} else {
			expected = plan.InternalPredecessors[to]
		}
	}
	member := false
	for _, pred := range expected {
		member = member || pred == from
	}
	if !member {
		return fmt.Errorf("predecessor %q is not expected by activation %q", from, id)
	}
	if _, ok := x.frontier.Expected[id]; !ok {
		x.frontier.Expected[id] = append([]string(nil), expected...)
	}
	if x.frontier.ActivationArrivals[id] == nil {
		x.frontier.ActivationArrivals[id] = map[string]predecessorArrival{}
	}
	if previous, exists := x.frontier.ActivationArrivals[id][from]; exists {
		if previous.SourceTaskID == sourceTaskID && previous.Skipped == !selected {
			return nil
		}
		return fmt.Errorf("predecessor %q has conflicting arrivals for activation %q", from, id)
	}
	if completed := x.frontier.Results[id]; completed != nil && completed.Adopted {
		return nil
	}
	x.frontier.ActivationArrivals[id][from] = predecessorArrival{SourceTaskID: sourceTaskID, SourceGeneration: sourceGeneration, TargetGeneration: generation, Skipped: !selected}
	anySelected := false
	for _, pred := range x.frontier.Expected[id] {
		arrival, ok := x.frontier.ActivationArrivals[id][pred]
		if !ok {
			return nil
		}
		anySelected = anySelected || !arrival.Skipped
	}
	if !anySelected {
		// Pregel 的跳过事实按任务身份保存，不能污染下一代同名节点。
		x.frontier.Skipped[id] = true
		// 全部回边未选时循环已经结束，不用跳过传播继续制造下一代。
		// 部分回边选中时仍需保留其余回边的 skipped 到达以满足本轮汇合。
		if loopEntry && generation > 0 {
			return nil
		}
		for _, next := range x.graph.allSuccessors(to) {
			if err := x.arrivePregelAllOutcome(to, next, generation, id, false); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ready := range x.frontier.ReadyGenerations[to] {
		if ready == generation {
			return nil
		}
	}
	x.frontier.ReadyGenerations[to] = append(x.frontier.ReadyGenerations[to], generation)
	sort.Slice(x.frontier.ReadyGenerations[to], func(i, j int) bool { return x.frontier.ReadyGenerations[to][i] < x.frontier.ReadyGenerations[to][j] })
	x.frontier.Ready = appendUnique(x.frontier.Ready, to)
	return nil
}
