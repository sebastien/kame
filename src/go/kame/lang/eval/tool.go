package eval

import (
	"kame/core"
	"kame/lang/source"
)

// TrackedTool resolves a tool through the driver's startup policy, then retains
// the selected executable's internal metadata without granting user file reads.
func (c *Context) TrackedTool(name string, span source.Span) Result {
	if name == "" { return failure(c.Run, "TOOL_MISSING", span, "tool name is empty") }
	if c.Engine == nil || (c.DependencyObserver == nil && c.DependencyContextObserver == nil) {
		return failure(c.Run, "PHASE_INVALID", span, "tool requires a build or source session")
	}
	key := core.ResourceKey{Kind: core.ResourceTool, Name: name}
	if !c.Dependency(key) { return Result{Waiting: true} }
	current := c.Value(key)
	if !current.OK || current.Value.Kind != core.String || current.Value.Text == "" {
		return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
	}
	file := core.ResourceKey{Kind: core.ResourceFile, Name: current.Value.Text}
	if !c.Dependency(file) { return Result{Waiting: true} }
	c.Value(file)
	value := current.Value.Clone(c.Run)
	value.ToolName = core.NewString(c.Run, name).Text
	return Result{Value: value}
}
