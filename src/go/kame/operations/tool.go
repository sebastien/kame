package operations

import (
	"kame/core"
	"kame/lang/eval"
	"strings"
)

func opTool(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "nonempty executable designation")
	}
	if values[0].Text == "" || strings.IndexByte(values[0].Text, 0) >= 0 {
		return invalidArgument(c, values, 0, "nonempty executable designation without NUL")
	}
	return c.TrackedTool(values[0].Text, c.Span)
}

func opSourceDir(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	_ = values
	return c.SourceDirectory()
}
