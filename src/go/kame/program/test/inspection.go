package program_test

import (
	"kame/core"
	"kame/host"
	"kame/host/wasm"
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
	d := compiled.Program.WriteInspection(&first, roots, -1, "plan")
	if d.Code != "" || !strings.Contains(first.String(), "\"status\":\"unknown\"") { t.Error("unobserved artifact status was guessed") }
	d.Free(a)
	d = compiled.Program.WriteInspection(&second, roots, -1, "plan")
	if d.Code != "" || first.String() != second.String() { t.Error("artifact status was not repeat-stable") }
	d.Free(a)
	first.Free(); second.Free()
	compiled.Program.Free(); compiled.Free(a); parsed.Free(); registry.Free()
}

func TestInspectionDownstreamPatternsDepthAndRepeatedQueries(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./build/{name}.o ./build/{name}.map : ./src/{name}.c\n./app : ./build/one.o ./unrelated\n./unused : ./elsewhere\n./ordered : | ./src/one.c\ndefault : ./app\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a); defer compiled.Program.Free()
	operands := compiled.Program.NormalizeTargetOperands([]string{"src/one.c"})
	defer program.FreeStrings(a, operands)
	if len(operands) != 1 || operands[0] != "./src/one.c" { t.Error("CLI path was not normalized"); return }
	var first = bytes.NewBuffer(a, nil)
	var second = bytes.NewBuffer(a, nil)
	defer first.Free(); defer second.Free()
	d := compiled.Program.WriteInspection(&first, operands, -1, "outputs")
	if d.Code != "" { t.Error(d.Code) }
	d.Free(a)
	text := first.String()
	if !strings.Contains(text, "\"direction\":\"downstream\"") || !strings.Contains(text, "\"target\":\"./build/one.o\"") || !strings.Contains(text, "\"display\":\"./build/one.map\"") || !strings.Contains(text, "\"target\":\"./app\"") || strings.Contains(text, "\"target\":\"./unused\"") || strings.Contains(text, "\"target\":\"./ordered\"") { t.Error("incorrect consumer projection") }
	d = compiled.Program.WriteInspection(&second, operands, -1, "outputs")
	if d.Code != "" || first.String() != second.String() { t.Error("consumer query not repeat-stable") }
	d.Free(a)
	second.Free(); second = bytes.NewBuffer(a, nil)
	d = compiled.Program.WriteInspection(&second, operands, 1, "outputs")
	if d.Code != "" || !strings.Contains(second.String(), "\"truncated\":true") || strings.Contains(second.String(), "\"target\":\"./app\"") { t.Error("depth did not count consumer hops") }
	d.Free(a)
	second.Free(); second = bytes.NewBuffer(a, nil)
	d = compiled.Program.WriteInspection(&second, operands, -1, "plan")
	if d.Code != "" || !strings.Contains(second.String(), "\"producers\":[]") { t.Error("source root requires a producer") }
	d.Free(a)
}

func TestCLIFileOperandsPreserveLogicalNamesAndArguments(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "seed :\n./release.zip : ./seed\ntask deploy {mode=debug} :\n./out : ./input\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	if compiled.Program == nil { t.Error("compile failed"); compiled.Free(a); parsed.Free(); registry.Free(); return }
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a); defer compiled.Program.Free()
	operands := compiled.Program.NormalizeTargetOperands([]string{"seed", "release.zip", "out", "input", "../other", "deploy", "mode=release"})
	defer program.FreeStrings(a, operands)
	expected := []string{"seed", "./release.zip", "./out", "./input", "../other", "deploy mode=release"}
	if len(operands) != len(expected) { t.Error("operand arity changed"); return }
	for i := range expected { if operands[i] != expected[i] { t.Error("incorrect operand normalization") } }
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	d := compiled.Program.WriteInspection(&buffer, operands[:1], -1, "plan")
	if d.Code != "" || strings.Contains(buffer.String(), "\"kind\":\"file\"") { t.Error("exact logical name became a file") }
	d.Free(a)
}

func TestInspectionReversePositionalReferencesAndLogicalPatterns(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./out/{name} : ./src/{_0}\n./pair/{left}-{right} : ./pair-src/{_1}-{_0}\nbuild-{name} : seed-{name}\nseed-one :\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a)
	if compiled.Program == nil { t.Error("compile failed"); return }
	defer compiled.Program.Free()
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	roots := []string{"./src/one", "./pair-src/two-one", "seed-one"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "outputs")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"target\":\"./out/one\"") || !strings.Contains(buffer.String(), "\"target\":\"./pair/one-two\"") || !strings.Contains(buffer.String(), "\"target\":\"build-one\"") || strings.Contains(buffer.String(), "pattern-captures-unbound") { t.Error("reverse capture references or logical consumers lost") }
	d.Free(a)
	forward := compiled.Program.Plan("./pair/one-two")
	if forward.Diagnostic.Code != "" || len(forward.Plan.Inputs) != 1 || forward.Plan.Inputs[0] != "./pair-src/two-one" { t.Error("reverse inference disagrees with forward rendering") }
	forward.Plan.Free(a); forward.Diagnostic.Free(a)
}

func TestInspectionConsumerPlanningErrorsAreScopedToAffectedProducers(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./out : ./seed\n./bad : @(:true) ./elsewhere\n./computed-bad : @(\"./computed-seed\") @(:true)\n./late-computed-bad : @(:true) @(\"./late-computed-seed\")\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a)
	if compiled.Program == nil { t.Error("compile failed"); return }
	defer compiled.Program.Free()
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	roots := []string{"./seed"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "outputs")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"target\":\"./out\"") || strings.Contains(buffer.String(), "\"target\":\"./bad\"") { t.Error("unrelated planning error aborted discovery") }
	d.Free(a)
	buffer.Free(); buffer = bytes.NewBuffer(a, nil)
	roots[0] = "./elsewhere"
	d = compiled.Program.WriteInspection(&buffer, roots, -1, "affected")
	if d.Code != "EXPR_INVALID" || d.Target != "./bad" || len(buffer.Bytes()) != 0 { t.Error("affected planning error was hidden or output was not atomic") }
	d.Free(a)
	buffer.Free(); buffer = bytes.NewBuffer(a, nil)
	d = compiled.Program.WriteInspection(&buffer, roots, 0, "affected")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"producers\":[]") { t.Error("depth zero resolved an excluded producer") }
	d.Free(a)
	seeds := []string{"./computed-seed", "./late-computed-seed"}
	producers := []string{"./computed-bad", "./late-computed-bad"}
	for i := range seeds {
		buffer.Free(); buffer = bytes.NewBuffer(a, nil)
		roots[0] = seeds[i]
		d = compiled.Program.WriteInspection(&buffer, roots, -1, "outputs")
		if d.Code != "EXPR_INVALID" || d.Target != producers[i] || len(buffer.Bytes()) != 0 { t.Error("computed edge around planning failure was lost") }
		d.Free(a)
	}
}

func TestInspectionExcludedSeedDoesNotResolveItsInputs(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./seed : @((read ./secret))\n./out : ./seed\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: ".", ForwardRequests: true, Grants: []eval.Grant{{Capability: eval.Read}}})
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a)
	if compiled.Program == nil { t.Error("compile failed"); return }
	defer compiled.Program.Free()
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	roots := []string{"./seed"}
	for depth := 0; depth <= 1; depth++ {
		d := compiled.Program.WriteInspection(&buffer, roots, depth, "outputs")
		// Depth one may yield artifact status reads, but never an input resolver
		// for the excluded seed. Depth zero must not issue any host request.
		if d.Code != "" { t.Error(d.Code) }
		d.Free(a)
		for {
			next := compiled.Program.NextOutbound()
			if !next.OK { break }
			if depth == 0 || strings.Contains(host.PayloadText(next.Request.Payload, host.FieldPath), "secret") { t.Error("excluded seed issued a read") }
			next.Request.Free(a)
		}
		if depth == 0 && compiled.Program.InspectionWaiting { t.Error("depth zero yielded to the host") }
		buffer.Free(); buffer = bytes.NewBuffer(a, nil)
	}
}

func TestInspectionConsumerReadErrorsRemainAuthorizedAndScoped(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./out : ./seed\n./bad : ./elsewhere @((read ./missing))\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Host: wasm.NewMemoryHost(a), Directory: ".", Grants: []eval.Grant{{Capability: eval.Read}}})
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a)
	if compiled.Program == nil { t.Error("compile failed"); return }
	defer compiled.Program.Free()
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	roots := []string{"./seed"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "outputs")
	if d.Code != "" || !strings.Contains(buffer.String(), "\"target\":\"./out\"") { t.Error("unrelated input read failure aborted discovery") }
	d.Free(a)
	buffer.Free(); buffer = bytes.NewBuffer(a, nil)
	roots[0] = "./elsewhere"
	d = compiled.Program.WriteInspection(&buffer, roots, -1, "outputs")
	if d.Code != "FS_ERR" || d.Target != "./bad" || len(buffer.Bytes()) != 0 { t.Error("affected input read failure was hidden") }
	d.Free(a)
}

func TestInspectionAffectedConsumerReadStillRequiresGrant(t *testing.T) {
	a := t.Allocator()
	parsed := script.Parse(a, "<test>", "./out : ./seed @((read ./secret))\n")
	registry := eval.NewRegistry(a)
	operations.Register(registry)
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: ".", ForwardRequests: true})
	defer registry.Free(); defer parsed.Free(); defer compiled.Free(a)
	if compiled.Program == nil { t.Error("compile failed"); return }
	defer compiled.Program.Free()
	var buffer = bytes.NewBuffer(a, nil)
	defer buffer.Free()
	roots := []string{"./seed"}
	d := compiled.Program.WriteInspection(&buffer, roots, -1, "affected")
	if d.Code != "CAP_DENIED" || d.Target != "./out" || len(buffer.Bytes()) != 0 || compiled.Program.InspectionWaiting { t.Error("affected input read bypassed authorization") }
	d.Free(a)
	next := compiled.Program.NextOutbound()
	if next.OK { t.Error("denied read reached the host"); next.Request.Free(a) }
}
