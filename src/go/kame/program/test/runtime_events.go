package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/testing"
)

func TestOutAndErrEmitEvents(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "run :\n\t@(out \"out\")\n\t@(err \"err\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("effects failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seenOut, seenErr := false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.Stdout && next.Event.Target == "run" && string(next.Event.Data) == "out" { seenOut = true }
		if next.Event.Kind == program.Stderr && next.Event.Target == "run" && string(next.Event.Data) == "err" { seenErr = true }
		next.Event.Free(a)
	}
	if !seenOut || !seenErr { t.Error("out and err events were not emitted with target identity") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOutCoercesScalarValues(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "run :\n\t@(out 42)\n\t@(out :true)\n\t@(out :nil)\n\t@(out 1.5)\n\t@(out (list 1 2))\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { t.Error("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("effects failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	index := 0
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.Stdout && next.Event.Target == "run" {
			text := string(next.Event.Data)
			if index == 0 && text != "42" { t.Error("out did not render an integer") }
			if index == 1 && text != "true" { t.Error("out did not render a boolean") }
			if index == 2 && text != "nil" { t.Error("out did not render nil") }
			if index == 3 && text != "1.5" { t.Error("out did not render a float") }
			if index == 4 && text != "[1,2]" { t.Error("out did not render a list") }
			index++
		}
		next.Event.Free(a)
	}
	if index != 5 { t.Error("out did not emit one event per coerced value") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestOperationDependencyEmitsEvent(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "input :\n\ttrue\noutput : input\n\t@(depends \"x\")\n\ttrue\n")
	registry := eval.NewRegistry(a)
	if !registry.Add(eval.Operation{Name: "depends", Call: observeDependency, MinArity: 1, MaxArity: 1}) { t.Fatal("operation registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if len(compiled.Diagnostics) != 0 || compiled.Program == nil { if len(compiled.Diagnostics) != 0 { t.Errorf("compile failed: %s %s", compiled.Diagnostics[0].Code, compiled.Diagnostics[0].Message) } else { t.Error("compile failed") }; return }
	result := compiled.Program.Materialize("output")
	if result.Diagnostic.Code != "" { t.Errorf("dependency operation failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	seen := false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.Kind == program.DependencyDiscovered && next.Event.Target == "output" && next.Event.DependencyKey.Name == "input" { seen = true }
		next.Event.Free(a)
	}
	if !seen { t.Error("dependency operation did not emit discovery event") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func containsSubstring(text string, sub string) bool {
	if len(sub) == 0 || len(sub) > len(text) { return false }
	for i := 0; i+len(sub) <= len(text); i++ {
		match := true
		for j := 0; j < len(sub); j++ { if text[i+j] != sub[j] { match = false; break } }
		if match { return true }
	}
	return false
}

func TestRecipeFailureRetainsBodySpan(t *testing.T) {
	a := t.Allocator()
	source := "./output :\n\tfalse\n"
	parsed := script.Parse(a, "test.kmk", source)
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("./output")
	if result.Diagnostic.Code != "RECIPE_FAIL" || result.Diagnostic.Span.Start <= 0 || result.Diagnostic.Span.End > len(source) { t.Errorf("recipe failure span = %s %d..%d", result.Diagnostic.Code, result.Diagnostic.Span.Start, result.Diagnostic.Span.End) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestRecipeFailureCarriesNestedTargetStack(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "root : leaf\n\ttrue\nleaf :\n\tfalse\n")
	registry := eval.NewRegistry(a)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("root")
	if result.Diagnostic.Code != "RECIPE_FAIL" || len(result.Diagnostic.TargetStack) != 2 || result.Diagnostic.TargetStack[0] != "root" || result.Diagnostic.TargetStack[1] != "leaf" { t.Errorf("target stack = %s %v", result.Diagnostic.Code, result.Diagnostic.TargetStack) }
	result.Free(a)
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestTargetEventsCarrySharedIdentity(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "test.kmk", "run :\n\t@(nop \"\")\n")
	registry := eval.NewRegistry(a)
	if !operations.Register(registry) { t.Fatal("library registration failed"); return }
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: "."})
	if compiled.Program == nil { t.Fatal("compile failed"); return }
	result := compiled.Program.Materialize("run")
	if result.Diagnostic.Code != "" { t.Errorf("materialize failed: %s", result.Diagnostic.Code) }
	result.Free(a)
	var nodeID int64
	started, value, completed := false, false, false
	for {
		next := compiled.Program.NextEvent()
		if !next.OK { break }
		if next.Event.NodeID == 0 || next.Event.Key.Name == "" { t.Error("event lacked node identity") }
		if nodeID == 0 { nodeID = next.Event.NodeID }
		if next.Event.NodeID != nodeID { t.Error("target events did not share node identity") }
		if next.Event.Kind == program.TargetStarted { started = true }
		if next.Event.Kind == program.TargetValue { value = true }
		if next.Event.Kind == program.TargetCompleted { completed = true }
		next.Event.Free(a)
	}
	if !started || !value || !completed { t.Error("target lifecycle events were incomplete") }
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
