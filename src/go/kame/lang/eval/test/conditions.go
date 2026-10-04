package eval_test

import (
	"kame/core"
	"kame/lang/eval"
	"kame/lang/script"
	"solod.dev/so/testing"
)

func TestUncomposedConditionalDeclarationsCannotRegisterDefinitions(t *testing.T) {
	a := t.Allocator()
	engine := core.NewEngine(a)
	registry := eval.NewRegistry(a)
	parsed := script.Parse(a, "uncomposed.kmk", "when :false\nhidden = \"value\"\nend\n")
	checked := eval.CompileChecked(a, engine, parsed, registry)
	if checked.Program != nil || len(checked.Diagnostics) == 0 || checked.Diagnostics[0].Code != "FEATURE_UNSUP" {
		t.Error("checked compilation registered unselected declarations")
	}
	checked.Free(a)
	compiled := eval.Compile(a, engine, parsed, registry)
	if compiled.Valid || compiled.Definition("hidden") != nil || len(compiled.Diagnostics) == 0 || compiled.Diagnostics[0].Code != "FEATURE_UNSUP" {
		t.Error("unchecked compilation registered unselected declarations")
	}
	engine.Free()
	compiled.Free()
	parsed.Free()
	registry.Free()
}
