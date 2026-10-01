package cli

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ExpressionRunArgs is only a compatibility spelling, not a second runner.
// Preserve implicit stdin for old callers; all other syntax is do run's grammar.
func ExpressionRunArgs(args []string) []string {
	forwarded := slices.Make[string](mem.System, 2)
	forwarded[0], forwarded[1] = "--lang", "expr"
	for i := range args { forwarded = slices.Append(mem.System, forwarded, args[i]) }
	probe := ParseRun(forwarded)
	if probe.Error.Message == "run requires a source input" {
		stdin := slices.Make[string](mem.System, 3)
		stdin[0], stdin[1], stdin[2] = "--lang", "expr", "-"
		for i := range args { stdin = slices.Append(mem.System, stdin, args[i]) }
		slices.Free(mem.System, forwarded)
		forwarded = stdin
	}
	probe.Free()
	return forwarded
}
