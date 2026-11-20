package operations

import (
	"kame/core"
	"kame/lang/eval"
)

func opEval(c *eval.Context, _ any, values []core.Value) eval.Result {
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "string")
	}
	return c.EvalText(values[0].Text)
}

func opFmt(c *eval.Context, _ any, values []core.Value) eval.Result {
	text, invalid := eval.FormatValueSource(c.Run, values[0])
	if invalid != "" {
		discardArgCallables(c, values)
		return c.InvalidOperation("cannot format " + invalid + " as value source")
	}
	return eval.Result{Value: core.Value{Kind: core.String, Text: text}}
}
