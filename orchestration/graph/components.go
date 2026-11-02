package graph

import (
	"fmt"
	"sort"
)

// compileComponents 复用同一编译计划，给循环 All 明确 bootstrap 与回边的边界。
func (g *Graph[S]) compileComponents(plan *ExecutionPlan) error {
	plan.Components = map[string]int{}
	plan.CyclicComponents = map[int]bool{}
	plan.ComponentEntries = map[int]string{}
	plan.InternalPredecessors = map[string][]string{}
	plan.ExternalPredecessors = map[string][]string{}
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var groups [][]string
	var visit func(string)
	visit = func(name string) {
		index++
		indices[name] = index
		low[name] = index
		stack = append(stack, name)
		onStack[name] = true
		for _, to := range g.allSuccessors(name) {
			if indices[to] == 0 {
				visit(to)
				if low[to] < low[name] {
					low[name] = low[to]
				}
			} else if onStack[to] && indices[to] < low[name] {
				low[name] = indices[to]
			}
		}
		if low[name] == indices[name] {
			var group []string
			for {
				last := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[last] = false
				group = append(group, last)
				if last == name {
					break
				}
			}
			sort.Strings(group)
			groups = append(groups, group)
		}
	}
	for _, name := range plan.TopologicalOrder {
		if indices[name] == 0 {
			visit(name)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	for id, names := range groups {
		for _, name := range names {
			plan.Components[name] = id
		}
		if len(names) > 1 {
			plan.CyclicComponents[id] = true
		} else {
			for _, to := range g.allSuccessors(names[0]) {
				if to == names[0] {
					plan.CyclicComponents[id] = true
				}
			}
		}
	}
	for _, name := range plan.TopologicalOrder {
		for _, pred := range plan.Dependencies[name] {
			if plan.Components[name] == plan.Components[pred] {
				plan.InternalPredecessors[name] = append(plan.InternalPredecessors[name], pred)
			} else {
				plan.ExternalPredecessors[name] = append(plan.ExternalPredecessors[name], pred)
			}
		}
	}
	if plan.Mode != GraphModePregel || plan.TriggerMode != TriggerAllPredecessors {
		return nil
	}
	for name, node := range g.Nodes {
		if node.commandHandler != nil {
			return fmt.Errorf("Pregel All does not support unresolved command routes at %q; declare route targets", name)
		}
	}
	for from, edges := range g.conditionalEdges {
		for _, edge := range edges {
			if edge.edges == nil {
				return fmt.Errorf("Pregel All does not support unresolved dynamic routes at %q; declare route targets", from)
			}
		}
	}
	cyclicCount := 0
	for id := range plan.CyclicComponents {
		cyclicCount++
		var entries []string
		for _, name := range groups[id] {
			if len(plan.ExternalPredecessors[name]) > 0 || g.EntryPoint == name {
				entries = append(entries, name)
			}
		}
		if len(entries) != 1 {
			return fmt.Errorf("Pregel All component %v requires exactly one explicit entry; found %v", groups[id], entries)
		}
		entry := entries[0]
		plan.ComponentEntries[id] = entry
		nodes := map[string]*Node[S]{}
		deps := map[string][]string{}
		for _, name := range groups[id] {
			nodes[name] = g.Nodes[name]
			if name != entry {
				deps[name] = plan.InternalPredecessors[name]
			}
		}
		if _, err := topologicalSort(nodes, deps); err != nil {
			return fmt.Errorf("Pregel All component %v has an internal cycle beyond entry %q", groups[id], entry)
		}
	}
	if cyclicCount > 1 {
		return fmt.Errorf("Pregel All currently supports one cyclic component per plan; found %d", cyclicCount)
	}
	// 同一个汇合不混用一次性来源与循环来源，避免将旧输入拼进后续代次。
	var recurrent func(string, map[string]bool) bool
	recurrent = func(name string, seen map[string]bool) bool {
		if plan.CyclicComponents[plan.Components[name]] {
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		for _, pred := range plan.Dependencies[name] {
			if recurrent(pred, seen) {
				return true
			}
		}
		return false
	}
	for _, name := range plan.TopologicalOrder {
		if plan.CyclicComponents[plan.Components[name]] {
			continue
		}
		haveRecurring, haveOnce := false, false
		for _, pred := range plan.Dependencies[name] {
			if recurrent(pred, map[string]bool{}) {
				haveRecurring = true
			} else {
				haveOnce = true
			}
		}
		if haveRecurring && haveOnce {
			return fmt.Errorf("Pregel All join %q mixes recurrent and one-shot predecessors", name)
		}
	}
	return nil
}
