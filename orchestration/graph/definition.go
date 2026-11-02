package graph

import "fmt"

func (g *Graph[S]) freezeDefinition() {
	copy := *g
	copy.frozen = nil
	copy.compiled = true
	if g.definitionReference != nil {
		reference := *g.definitionReference
		reference.Content = append([]byte(nil), reference.Content...)
		copy.definitionReference = &reference
	}
	copy.Nodes = make(map[string]*Node[S], len(g.Nodes))
	for name, node := range g.Nodes {
		cloned := *node
		if node.RetryPolicy != nil {
			retry := *node.RetryPolicy
			cloned.RetryPolicy = &retry
		}
		if node.IO != nil {
			io := *node.IO
			io.Input = append([]FieldMapping(nil), io.Input...)
			io.Output = append([]FieldMapping(nil), io.Output...)
			cloned.IO = &io
		}
		cloned.Metadata = make(map[string]any, len(node.Metadata))
		for key, value := range node.Metadata {
			cloned.Metadata[key] = value
		}
		copy.Nodes[name] = &cloned
	}
	copy.adjacency = make(map[string][]string, len(g.adjacency))
	for from, targets := range g.adjacency {
		copy.adjacency[from] = append([]string(nil), targets...)
	}
	copy.conditionalEdges = make(map[string][]conditionalEdge[S], len(g.conditionalEdges))
	for from, edges := range g.conditionalEdges {
		for _, edge := range edges {
			cloned := edge
			if edge.edges != nil {
				cloned.edges = map[string]string{}
				for label, to := range edge.edges {
					cloned.edges[label] = to
				}
			}
			copy.conditionalEdges[from] = append(copy.conditionalEdges[from], cloned)
		}
	}
	copy.fieldReducers = make(map[string]FieldReducer, len(g.fieldReducers))
	for key, value := range g.fieldReducers {
		copy.fieldReducers[key] = value
	}
	g.frozen = &copy
}

func (g *Graph[S]) validateWriteConflicts() error {
	writers := map[string][]string{}
	for _, name := range g.plan.TopologicalOrder {
		node := g.Nodes[name]
		if node.IO == nil {
			continue
		}
		for _, mapping := range node.IO.Output {
			writers[mapping.To] = append(writers[mapping.To], name)
		}
	}
	reaches := func(from, to string) bool {
		seen := map[string]bool{}
		queue := []string{from}
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			if seen[next] {
				continue
			}
			seen[next] = true
			for _, target := range g.allSuccessors(next) {
				if target == to {
					return true
				}
				queue = append(queue, target)
			}
		}
		return false
	}
	for path, names := range writers {
		if g.fieldReducers[path] != nil {
			continue
		}
		for i, from := range names {
			for _, to := range names[i+1:] {
				if g.plan.Mode == GraphModePregel || (!reaches(from, to) && !reaches(to, from)) {
					return fmt.Errorf("nodes %q and %q both write field %q without a reducer", from, to, path)
				}
			}
		}
	}
	return nil
}
