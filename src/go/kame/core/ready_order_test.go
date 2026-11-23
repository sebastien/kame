package core

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// Compare the incremental scheduler to a full scan in resource-key order.
func TestReadyMatchesKeyOrderReference(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	build := func(seed int64) (*Engine, []*Node) {
		r := rand.New(rand.NewSource(seed))
		e := NewEngine(nil)
		var nodes []*Node
		// Engine keys are unique; names are shuffled so scan order differs
		// from key order.
		names := r.Perm(80)
		for i := 0; i < 2+r.Intn(30); i++ {
			n := &Node{ID: int64(i + 1), Key: ResourceKey{Kind: ResourceKind(names[i] % 2), Name: fmt.Sprintf("n%02d", names[i]/2)}, Interest: 1, Requested: r.Intn(4) != 0, State: NodeIdle}
			if r.Intn(5) == 0 {
				n.State = NodeComplete
			}
			n.offered = r.Intn(6) == 0
			e.nodes = append(e.nodes, n)
			e.enqueue(n)
			nodes = append(nodes, n)
		}
		if r.Intn(3) != 0 {
			e.lastRun = nodes[r.Intn(len(nodes))]
		}
		return e, nodes
	}
	ids := func(nodes []*Node) []int64 {
		var out []int64
		for _, n := range nodes {
			out = append(out, n.ID)
		}
		return out
	}
	before := func(a, b *Node) bool {
		if a.Key.Name != b.Key.Name {
			return a.Key.Name < b.Key.Name
		}
		return a.Key.Kind < b.Key.Kind
	}
	for trial := 0; trial < 2000; trial++ {
		seed, capacity := random.Int63(), 1+random.Intn(10)
		e, _ := build(seed)
		got := ids(e.Ready(capacity))
		reference, _ := build(seed)
		var want []*Node
		after := reference.lastRun
		for len(want) < capacity {
			var first, later *Node
			for _, n := range reference.nodes {
				if n.State != NodeIdle || !n.Requested || n.offered {
					continue
				}
				if first == nil || before(n, first) {
					first = n
				}
				if after != nil && before(after, n) && (later == nil || before(n, later)) {
					later = n
				}
			}
			selected := later
			if selected == nil {
				selected = first
			}
			if selected == nil {
				break
			}
			selected.offered = true
			want = append(want, selected)
			after = selected
		}
		if !slices.Equal(got, ids(want)) {
			t.Fatalf("seed %d capacity %d: Ready %v, choose %v", seed, capacity, got, ids(want))
		}
	}
}
