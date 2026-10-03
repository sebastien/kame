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
 if values[0].Text == "" { return failure("TOOL_MISSING", "tool name is empty") }
 if c.Engine == nil || c.DependencyObserver == nil { return failure("PHASE_INVALID", "tool requires a build or source session") }
 key := core.ResourceKey{Kind: core.ResourceTool, Name: values[0].Text}
 if !c.Dependency(key) { return eval.Result{Waiting: true} }
 current := c.Value(key)
 if !current.OK || current.Value.Kind != core.String || current.Value.Text == "" { return failure("TOOL_MISSING", "tool could not be resolved") }
 file := core.ResourceKey{Kind: core.ResourceFile, Name: current.Value.Text}
 if !c.Dependency(file) { return eval.Result{Waiting: true} }
 return eval.Result{Value: current.Value.Clone(c.Run)}
}
