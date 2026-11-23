package core

import (
	"fmt"
	"runtime"
	"slices"
	"testing"
	"time"
)

func BenchmarkGraphUpdate(b *testing.B) {
	for _, n := range []int{128, 1024, 8192} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			e, value := graphFixture(n)
			defer e.Free()
			start := time.Now()
			graphSettle(b, e)
			cold := float64(n) / time.Since(start).Seconds()
			samples := make([]time.Duration, b.N)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				*value++
				start := time.Now()
				e.Revalidate(e.nodes[0])
				graphSettle(b, e)
				samples[i] = time.Since(start)
				if e.nodes[2].Latest.Int != *value+2 {
					b.Fatal("stale root value")
				}
			}
			b.StopTimer()
			b.ReportMetric(cold, "initial-nodes/s")
			b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "updates/s")
			if b.N >= 2000 {
				for i, window := range [][]time.Duration{samples[:1000], samples[b.N-1000:]} {
					slices.Sort(window)
					b.ReportMetric(float64(window[499].Nanoseconds()), []string{"early-p50-ns", "late-p50-ns"}[i])
				}
			}
			slices.Sort(samples)
			for _, p := range []int{50, 95, 99} {
				if p != 99 || b.N >= 100 {
					b.ReportMetric(float64(samples[(p*b.N-1)/100].Nanoseconds()), fmt.Sprintf("p%d-ns", p))
				}
			}
		})
	}
}

func graphHeap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

func BenchmarkGraphFootprint(b *testing.B) {
	for _, n := range []int{128, 1024, 8192} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for range b.N {
				e := NewEngine(nil)
				base := graphHeap()
				for i := range n {
					e.Add(ResourceKey{Kind: ResourceDefinition, Name: fmt.Sprintf("n%06d", i)}, graphProduce, nil)
				}
				bare := graphHeap()
				// Independent pairs avoid mixing edge storage with traversal depth.
				for i := 1; i < n; i += 2 {
					e.AddStatic(e.nodes[i], e.nodes[i-1])
				}
				linked := graphHeap()
				for _, node := range e.nodes {
					e.Request(node)
				}
				graphSettle(b, e)
				b.ReportMetric(float64(bare-base)/float64(n), "retained-B/node")
				b.ReportMetric(float64(linked-bare)/float64(n/2), "extra-B/edge")
				b.ReportMetric(float64(graphHeap()-base)/float64(n), "settled-B/node")
				e.Free()
			}
		})
	}
}

// Unlike BenchmarkGraphUpdate, the affected work grows with the fixture.
func BenchmarkGraphJoin(b *testing.B) {
	for _, width := range []int{4, 32, 256} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			e := NewEngine(nil)
			defer e.Free()
			var value int64
			source := e.Add(ResourceKey{Name: "leaf"}, graphProduce, &value)
			ends := []*Node{source}
			for i := range width {
				n := e.Add(ResourceKey{Name: fmt.Sprintf("b-%03d", i)}, graphProduce, source.Key)
				e.AddStatic(n, source)
				if i%2 != 0 {
					m := e.Add(ResourceKey{Name: fmt.Sprintf("m-%03d", i)}, graphProduce, n.Key)
					e.AddStatic(m, n)
					n = m
				}
				ends = append(ends, n)
			}
			var keys []ResourceKey
			for _, n := range ends {
				keys = append(keys, n.Key)
			}
			join := e.Add(ResourceKey{Name: "join"}, sumInputs, keys)
			for _, n := range ends {
				e.AddStatic(join, n)
			}
			e.Request(join)
			graphSettle(b, e)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				value++
				e.Revalidate(source)
				graphSettle(b, e)
				if join.Latest.Int != value*int64(width+1)+int64(width+width/2) {
					b.Fatal("stale join")
				}
			}
			b.StopTimer()
			for _, n := range e.nodes {
				if n.Revision != int64(b.N)+1 {
					b.Fatalf("%s published %d times", n.Key.Name, n.Revision)
				}
			}
		})
	}
}

// Register consumers before their prerequisites so first dispatch has to
// repair the chain without depending on insertion order.
func BenchmarkGraphDeepChain(b *testing.B) {
	for _, size := range []int{128, 1024, 4096} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.StopTimer()
			for range b.N {
				e := NewEngine(nil)
				for i := range size {
					e.Add(ResourceKey{Name: fmt.Sprintf("n%06d", i)}, graphProduce, nil)
				}
				for i := range size - 1 {
					e.AddStatic(e.nodes[i], e.nodes[i+1])
				}
				e.Request(e.nodes[0])
				b.StartTimer()
				first := e.Step()
				b.StopTimer()
				if first != e.nodes[size-1] || e.nodes[0].prerequisiteHeight != size-1 {
					b.Fatal("first dispatch did not repair the dependency chain")
				}
				e.Free()
			}
		})
	}
}
