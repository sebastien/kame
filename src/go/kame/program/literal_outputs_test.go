package program

import (
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/script"
	"testing"
)

func TestRejectedRulesDoNotClaimLiteralOutputs(t *testing.T) {
	const source = "arg = 1\ntask run {arg}\ntask run\n" +
		"rule ./owned = \"one\"\nrule ./extra ./sub/../owned\nrule ./extra = \"two\"\n"
	parsed := script.ParseKameFile(mem.System, "rules.km", source)
	defer parsed.Free()
	registry := eval.NewRegistry(mem.System)
	defer registry.Free()
	result := Compile(mem.System, parsed, registry, Options{Directory: "/work"})
	defer result.Free(mem.System)
	if result.Program != nil || len(result.Diagnostics) != 2 {
		t.Fatalf("expected only the two authored conflicts: %+v", result.Diagnostics)
	}
	arg, output := result.Diagnostics[0], result.Diagnostics[1]
	if arg.Code != "TGT_ARGUMENT" || output.Code != "TGT_AMBIG" || len(output.Related) != 1 {
		t.Fatalf("wrong conflict diagnostics: %+v", result.Diagnostics)
	}
	first := output.Related[0].Span
	if source[first.Start:first.End] != "./owned" || source[output.Span.Start:output.Span.End] != "./sub/../owned" {
		t.Fatalf("wrong producer locations: %+v", output)
	}
}
