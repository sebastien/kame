package eval

import (
	"kame/core"
	"kame/host"
	"kame/lang/script"
	"testing"
)

func TestGeneratorInvocationState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		stop  bool
		final core.StreamInvocationState
	}{
		{"end", "return", false, core.StreamCompleted},
		{"failure", "yield missing", false, core.StreamFailed},
		{"early stop", "yield 2", true, core.StreamCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := core.NewEngine(nil)
			registry := NewRegistry(nil)
			defer registry.Free()
			parsed := script.ParseKash(nil, "generator.kash", "def generate-items ()\n\tyield (inspect-state ())\n\t"+tc.body+"\n")
			defer parsed.Free()
			if len(parsed.Diagnostics) != 0 {
				t.Fatalf("generator parse failed: %v", parsed.Diagnostics)
			}
			program := Compile(nil, engine, parsed, registry)
			defer program.Free()
			defer engine.Free()
			ctx := Context{Program: program, Scope: program.Scope, Phase: EvaluatePhase, Requests: program.Requests}
			generated := program.newGenerator(parsed.Items[0].Definition.Expression, program.Scope, &ctx)
			defer generated.Free(nil)
			stream := generated.Value.Stream
			if stream.InvocationState != core.StreamSuspended {
				t.Fatalf("new invocation is %s, want suspended", stream.InvocationState)
			}
			registry.Add(Operation{Name: "inspect-state", Call: func(c *Context, _ any, _ []core.Value) Result {
				if stream.InvocationState != core.StreamRunning {
					t.Errorf("pulled invocation is %s, want running", stream.InvocationState)
				}
				if c.Completion().RequestID == 0 {
					if c.Submit(host.RequestCustom, core.Value{}) == 0 {
						return Result{Diagnostic: core.Diagnostic{Code: "HOST_FAIL"}}
					}
					return Result{Waiting: true}
				}
				_ = c.TakeCompletion()
				return Result{Value: core.Value{Kind: core.Int, Int: 1}}
			}, MaxArity: 0})
			g := streamGenerator(generated.Value)
			node := engine.Add(core.ResourceKey{Kind: core.ResourceDefinition, Name: "consumer"}, func(e *core.EngineContext, _ int64) core.ProducerResult {
				ctx.Resume(e)
				first := g.pull(&ctx, 1)
				if first.Waiting {
					return core.ProducerWaiting
				}
				if !first.Yielded || first.Value.Int != 1 || stream.InvocationState != core.StreamSuspended {
					t.Errorf("first pull did not yield and suspend: %s (%s)", stream.InvocationState, first.Diagnostic.Code)
				}
				first.Free(nil)
				var last Result
				if tc.stop {
					last = g.stop(&ctx)
				} else {
					last = g.pull(&ctx, 1)
				}
				if last.Waiting || last.Yielded || stream.InvocationState != tc.final {
					t.Errorf("last pull left invocation %s, want %s (%s)", stream.InvocationState, tc.final, last.Diagnostic.Code)
				}
				last.Free(nil)
				e.Publish(core.Value{})
				return core.ProducerCompleted
			}, nil)
			engine.Request(node)
			engine.Step()
			next := program.Requests.Next()
			if !next.OK || stream.InvocationState != core.StreamRunning {
				t.Fatalf("pending host work left invocation %s, want running", stream.InvocationState)
			}
			request := next.Request
			engine.Complete(core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID, HasValue: true})
			request.Free(nil)
			for i := 0; i < 8 && !node.Current; i++ {
				engine.Step()
			}
			if !node.Current || stream.InvocationState != tc.final {
				t.Fatalf("consumer left invocation %s, want %s", stream.InvocationState, tc.final)
			}
		})
	}
}
