package operations

import (
 "kame/core"
 "kame/lang/eval"
)

func opTool(c *eval.Context, state any, values []core.Value) eval.Result {
 _ = state
 if values[0].Kind != core.String {
  return invalidArgument(c, values, 0, "string")
 }
  return c.TrackedTool(values[0].Text, c.Span)
}
