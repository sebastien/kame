package eval

import "kame/core"

// A binding can own a closure whose parent chain reaches the binding's scope.
// Trial deletion counts the references held within the reachable scope graph.
// Anything with an outside owner, including active invocations and returned
// values that have not been stored, keeps the scopes it reaches alive.
func (s *Scope) collectCaptureCycles() {
	type candidate struct {
		scope   *Scope
		outside int
		live    bool
	}
	var graph []candidate
	indices := make(map[*Scope]int)
	add := func(scope *Scope) int {
		if i, ok := indices[scope]; ok {
			return i
		}
		i := len(graph)
		indices[scope] = i
		graph = append(graph, candidate{scope: scope, outside: scope.References})
		return i
	}
	add(s)
	for i := 0; i < len(graph); i++ {
		graph[i].scope.visitOwnedScopes(func(scope *Scope) {
			if scope.References > 0 && !scope.breakingCycles {
				index := add(scope)
				graph[index].outside--
			}
		})
	}

	var live []*Scope
	mark := func(scope *Scope) {
		if i, ok := indices[scope]; ok && !graph[i].live {
			graph[i].live = true
			live = append(live, scope)
		}
	}
	for i := range graph {
		if graph[i].outside > 0 {
			mark(graph[i].scope)
		}
	}
	for i := 0; i < len(live); i++ {
		live[i].visitOwnedScopes(mark)
	}

	// Pin the unreachable scopes during edge removal. A released closure can
	// otherwise destroy a scope whose bindings are still being visited.
	for _, node := range graph {
		if !node.live {
			node.scope.breakingCycles = true
			node.scope.Retain()
		}
	}
	for _, node := range graph {
		if !node.live {
			for i := range node.scope.Bindings {
				b := &node.scope.Bindings[i]
				if index, ok := indices[b.Source]; ok && !graph[index].live {
					capture := b.Source
					b.Source = nil
					capture.Free()
				}
			}
			node.scope.visitOwnedClosures(func(value *core.Value, scope *Scope) {
				if i, ok := indices[scope]; ok && !graph[i].live {
					freeCallables(node.scope.Alloc, value)
				}
			})
		}
	}
	for _, node := range graph {
		if !node.live {
			node.scope.Free()
		}
	}
}

func (s *Scope) visitOwnedScopes(visit func(*Scope)) {
	if s.Parent != nil {
		visit(s.Parent)
	}
	for i := range s.Bindings {
		if capture := s.Bindings[i].Source; capture != nil {
			visit(capture)
		}
	}
	s.visitOwnedClosures(func(_ *core.Value, capture *Scope) { visit(capture) })
}

func (s *Scope) visitOwnedClosures(visit func(*core.Value, *Scope)) {
	for i := range s.Bindings {
		visitValueClosures(&s.Bindings[i].Value, visit)
	}
	for i := range s.Section {
		visitValueClosures(&s.Section[i], visit)
	}
	if s.pipeInput != nil {
		visitValueClosures(&s.pipeInput.result.Value, visit)
	}
}

func visitValueClosures(value *core.Value, visit func(*core.Value, *Scope)) {
	if value.Shared != nil {
		return
	}
	switch value.Kind {
	case core.Callable:
		if fn, ok := value.Callable.(*Function); ok && fn.Scope != nil &&
			(fn.Kind == FunctionTemporary || fn.Kind == FunctionBorrowed) {
			visit(value, fn.Scope)
		}
	case core.List:
		for i := range value.List {
			visitValueClosures(&value.List[i], visit)
		}
	case core.Record:
		for i := range value.Record {
			visitValueClosures(&value.Record[i].Value, visit)
		}
	}
}
