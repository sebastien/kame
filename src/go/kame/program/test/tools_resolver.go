package program_test

import (
 "kame/core"
 "kame/host/posix"
 "kame/lang/eval"
 "kame/lang/script"
 "kame/operations"
 "kame/program"
 "solod.dev/so/mem"
 "solod.dev/so/testing"
)

var toolResolveCalls int
var toolResolveName string

func countedToolResolver(a mem.Allocator, name, cwd string, environment []string) string {
 _ = cwd; _ = environment
 toolResolveCalls++
 toolResolveName = name
 value := core.NewString(a, "/bin/sh")
 return value.Text
}

func TestToolResolverIsSharedTrackedAndRecordedInPlans(t *testing.T) {
 a := t.Allocator()
 toolResolveCalls = 0
 toolResolveName = ""
 parsed := script.Parse(a, "tools.kmk", "compiler = (tool \"chosen\")\ntask default :\n\t@(out compiler)\n")
 registry := eval.NewRegistry(a)
 operations.Register(registry)
 compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), ResolveTool: countedToolResolver, ToolOverrides: []string{"chosen=/bin/sh"}})
 if compiled.Program == nil { t.Error("compile tool"); compiled.Free(a); parsed.Free(); registry.Free(); return }
 p := compiled.Program
 result := p.Materialize("compiler")
 if result.Diagnostic.Code != "" || result.Value.Text != "/bin/sh" { t.Error("tool resolution did not reach definition") }
 result.Free(a)
 plan := p.Plan("default")
 if len(plan.Plan.Tools) != 1 || plan.Plan.Tools[0].Name != "chosen" || plan.Plan.Tools[0].Path != "/bin/sh" || toolResolveCalls != 1 || toolResolveName != "/bin/sh" { t.Error("tool override, shared resolution or plan record failed") }
 node := p.Eval.Definition("compiler")
 toolEdge, fileEdge := false, false
 for i := range node.Dynamic {
  if node.Dynamic[i].Key.Kind == core.ResourceTool { toolEdge = true }
  if node.Dynamic[i].Key.Kind == core.ResourceFile && node.Dynamic[i].Key.Name == "/bin/sh" { fileEdge = true }
 }
 if !toolEdge || !fileEdge { t.Error("tool identity and executable file were not tracked") }
 plan.Plan.Free(a); p.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
