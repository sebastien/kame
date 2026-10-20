package expr_test

import (
	"kame/lang/expr"
	"solod.dev/so/testing"
)

func TestClosedConfigurationLiterals(t *testing.T) {
	valid := []string{`:false`, `:nil`, `42`, `1.25`, `""`, `:release`, `./src`, `["py" "js"]`, `[flag: :true nested: [1 [x: "y"]]]`, `"$(printf inert)"`, `"""@(missing) *"""`}
	for i := range valid {
		parsed := expr.Parse(t.Allocator(), "literal", valid[i])
		if len(parsed.Diagnostics) != 0 || !expr.IsLiteral(parsed.Expr) {
			t.Error("closed literal was rejected: " + valid[i])
		}
		parsed.Free()
	}
	invalid := []string{`name`, `name.field`, `(nop 1)`, `([x] x)`, `$(printf bad)`, `"@(name)"`, `./@(name)/file`, `[1 name]`, `[x: (env "HOME")]`, `./*.c`, `"{name:*}"`}
	for i := range invalid {
		parsed := expr.Parse(t.Allocator(), "literal", invalid[i])
		if len(parsed.Diagnostics) == 0 && expr.IsLiteral(parsed.Expr) {
			t.Error("executable or patterned configuration was accepted: " + invalid[i])
		}
		parsed.Free()
	}
}
