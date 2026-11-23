package programcheck

import (
	"fmt"
	"kame/core"
	"kame/host/wasm"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"runtime"
	"strings"
	"testing"
)

type graphReadHost struct {
	*wasm.MemoryHost
	reads int
}

func (h *graphReadHost) ReadFile(a mem.Allocator, name string) ([]byte, error) {
	h.reads++
	return h.MemoryHost.ReadFile(a, name)
}

func BenchmarkProgramGraph(b *testing.B) {
	for _, n := range []int{128, 1024, 8192} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			h := &graphReadHost{MemoryHost: wasm.NewMemoryHost(mem.System)}
			var source strings.Builder
			for i := range n {
				if i < 16 {
					fmt.Fprintf(&source, "v%d = text (read ./file%d)\n", i, i)
					h.SetFile(fmt.Sprintf("file%d", i), []byte("initial"))
				} else {
					fmt.Fprintf(&source, "v%d = %d\n", i, i)
				}
			}
			parsed := script.ParseKameFile(mem.System, "bench.km", source.String())
			defer parsed.Free()
			reg := eval.NewRegistry(mem.System)
			operations.Register(reg)
			defer reg.Free()
			result := program.Compile(mem.System, parsed, reg, program.Options{Host: h, Directory: ".", Watch: true, Grants: []eval.Grant{{Capability: eval.Read}}})
			defer result.Free(mem.System)
			p := result.Program
			if p == nil {
				b.Fatal("fixture did not compile")
			}
			defer p.Free()
			var roots []*program.Handle
			for i := range n {
				root := p.Start(fmt.Sprintf("v%d", i)).Handle
				if root == nil {
					b.Fatalf("missing root %d", i)
				}
				roots = append(roots, root)
				for tick := 0; !root.Node.Current; tick++ {
					if tick > 100 {
						b.Fatalf("root %d did not settle", i)
					}
					p.Tick(0)
				}
			}
			defer func() {
				for _, root := range roots {
					root.Free()
				}
			}()
			runtime.GC()
			runtime.ReadMemStats(&after)
			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			sequence := 0 // Subbenchmarks share the session, including Go's calibration run.
			for _, batch := range []struct{ offered, distinct int }{{1, 1}, {16, 16}, {16, 1}} {
				b.Run(fmt.Sprintf("offered%d-distinct%d", batch.offered, batch.distinct), func(b *testing.B) {
					initial := h.reads
					keys := make([]core.ResourceKey, batch.distinct)
					for i := range keys {
						keys[i] = core.ResourceKey{Kind: core.ResourceFile, Name: fmt.Sprintf("file%d", i)}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := range b.N {
						sequence++
						want := fmt.Sprint(sequence)
						for _, key := range keys {
							h.SetFile(key.Name, []byte(want))
						}
						for j := range batch.offered {
							p.InvalidateResource(keys[j%len(keys)])
						}
						for _, root := range roots[:len(keys)] {
							for tick := 0; ; tick++ {
								p.Tick(0)
								r := root.PollRetained()
								ok := r.Done && r.Result.Value.Text == want
								r.Result.Free(mem.System)
								if ok {
									break
								}
								if tick > 256 {
									b.Fatalf("update %d did not settle", i)
								}
							}
						}
						for e := p.NextEvent(); e.OK; e = p.NextEvent() {
							e.Event.Free(mem.System)
						}
					}
					b.StopTimer()
					b.ReportMetric(float64(h.reads-initial)/float64(b.N), "reads/cycle")
					b.ReportMetric(float64(retained), "settled-program-B")
					b.ReportMetric(float64(b.N*len(keys))/b.Elapsed().Seconds(), "settled-keys/s")
				})
			}
		})
	}
}
