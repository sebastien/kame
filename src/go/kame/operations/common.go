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
	freeArgCallables(c, values)
	return result
}

// freeArgCallables releases top-level callables in rejected arguments. The
// caller shallow-frees v afterwards, so wrappers must be freed here or they
// leak their scope retain. Nested shares are untouched: derived results may
// still own them, and nested rejects cannot be freed from this package.
func freeArgCallables(c *eval.Context, v []core.Value) {
	for i := range v {
		if v[i].Kind == core.Callable {
			c.FreeCallable(&v[i])
		}
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
