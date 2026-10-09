package operations

import (
 "kame/core"
 "kame/lang/eval"
)

func opTool(c *eval.Context, state any, values []core.Value) eval.Result {
 _ = state
 if values[0].Kind != core.String {
  c.FreeCallable(&values[0])
  return c.InvalidArgument(0, "string", values[0].Kind)
 }
  return c.TrackedTool(values[0].Text, c.Span)
}
