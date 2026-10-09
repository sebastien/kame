package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"solod.dev/so/slices"
)

func truth(v core.Value) bool { return v.Kind != core.Nil && !(v.Kind == core.Bool && !v.Bool) }
func failure(code string, message string) eval.Result {
	return eval.Result{Diagnostic: core.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message, Owned: false}}
}
func invalidArgument(c *eval.Context, values []core.Value, index int, expected string) eval.Result {
	result := c.InvalidArgument(index, expected, values[index].Kind)
	discardArgCallables(c, values)
	return result
}

// discardArgCallables releases rejected arguments' callable ownership, including
// nested closures, while preserving binding-owned shares. The caller frees
// container storage afterwards. Do not use after transfer into results or state.
func discardArgCallables(c *eval.Context, v []core.Value) {
	for i := range v {
		c.DiscardCallables(&v[i])
	}
}
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
