package eval

import (
	"kame/core"
	"kame/internal/mem"
)

// This is not a valid environment variable name. Only its digest is persisted.
const ProcessEnvironmentName = "@process-environment"

// ProcessEnvironmentSignature ignores assignment order, not child environment values.
func ProcessEnvironmentSignature(a mem.Allocator, environment []string) core.Signature {
	values := make([]core.Value, len(environment))
	for i := range environment {
		values[i] = core.Value{Kind: core.String, Text: environment[i]}
		for j := i; j > 0 && values[j-1].Text > values[j].Text; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
	signature := core.ValueSignature(core.Value{Kind: core.List, List: values})
	mem.FreeSlice(nil, values)
	return signature
}
