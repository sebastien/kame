package operations

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/eval"
	"solod.dev/so/slices"
)

func truth(v core.Value) bool { return v.Kind != core.Nil && !(v.Kind == core.Bool && !v.Bool) }
func failure(code string, message string) eval.Result {
	return eval.Result{Diagnostic: core.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message, Owned: false}}
}
func invalid() eval.Result { return failure("EXPR_INVALID", "invalid operation arguments") }
func text(v core.Value) (string, bool) {
	if v.Kind != core.String {
		return "", false
	}
	return v.Text, true
}
func freeValues(c *eval.Context, values []core.Value) {
	for i := range values {
		values[i].Free(c.Run)
	}
	slices.Free(c.Run, values)
}
