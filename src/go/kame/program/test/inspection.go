package program_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/operations"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

// The tracked allocator also checks repeated-query and failure-path ownership.
func TestInspectionSharingSequencesAndRepeatQueries(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "default : left, right\nleft : shared\nright : shared later\nshared :\nlater :\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	var first = bytes.NewBuffer(a, nil)
	var second = bytes.NewBuffer(a, nil)
	roots := []string{"default"}
	d := compiled.Program.WriteInspection(&first, roots, -1, "plan")
	if d.Code != "" { t.Error(d.Code) }
	d.Free(a)
	d = compiled.Program.WriteInspection(&second, roots, -1, "plan")
	if d.Code != "" || first.String() != second.String() { t.Error("query was not repeat-stable") }
	d.Free(a)
	var doc core.Value
	if !core.ParseJSON(a, first.Bytes(), &doc) { t.Error("invalid JSON") }
	if !strings.Contains(first.String(), "\"number\":1,\"producers\":[4]") || !strings.Contains(first.String(), "\"number\":3,\"producers\":[5]") {
		t.Error("shared prerequisite delayed, or exclusive later prerequisite not gated")
	}
	doc.Free(a)
	first.Free(); second.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestInspectionBoundArgumentsAndCycles(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "task build {flavor=debug} : @(cat \"./\" flavor)\ndefault : \"build flavor=one\" \"build flavor=two\"\ncycle : loop\nloop : cycle\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	var buffer = bytes.NewBuffer(a, nil)
	roots := []string{"default"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "plan")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"display\":\"./one\"") || !strings.Contains(buffer.String(), "\"display\":\"./two\"") { t.Error("bound input instances not preserved") }
	d.Free(a)
	buffer.Free()
	buffer = bytes.NewBuffer(a, nil)
	roots[0] = "cycle"
	d = compiled.Program.WriteInspection(&buffer, roots, -1, "plan")
	if d.Code != "DEP_CYCLE" || len(d.TargetStack) != 3 || d.TargetStack[0] != d.TargetStack[2] || len(buffer.Bytes()) != 0 { t.Error("cycle path or failure atomicity lost") }
	d.Free(a)
	buffer.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestInspectionCoincidentOrigins(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "default : left ./source @(\"./source\") right\nleft :\nright :\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	var buffer = bytes.NewBuffer(a, nil)
	roots := []string{"default"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "plan")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"resource\":3,\"origin\":\"declared\",\"orderOnly\":false,\"group\":1") || !strings.Contains(buffer.String(), "\"resource\":3,\"origin\":\"computed\",\"orderOnly\":false,\"group\":1") {
		t.Error("coincident provenance lost")
	}
	d.Free(a)
	buffer.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestInspectionArtifactWithoutHostStaysUnknownAndRepeatStable(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./result : ./seed\n\ttouch forbidden\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	var first = bytes.NewBuffer(a, nil)
	var second = bytes.NewBuffer(a, nil)
	roots := []string{"./result"}
	d := compiled.Program.WriteInspection(&first, roots, -1, "outputs")
	if d.Code != "" || !strings.Contains(first.String(), "\"status\":\"unknown\"") { t.Error("unobserved artifact status was guessed") }
	d.Free(a)
	d = compiled.Program.WriteInspection(&second, roots, -1, "outputs")
	if d.Code != "" || first.String() != second.String() { t.Error("artifact status was not repeat-stable") }
	d.Free(a)
	first.Free(); second.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}
