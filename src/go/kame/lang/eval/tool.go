package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/source"
)

// TrackedTool resolves a tool through the driver's lookup policy, then retains
// the selected executable's internal metadata without granting user file reads.
func (c *Context) TrackedTool(name string, span source.Span) Result {
	if c.PurePredicate {
		return failure(c.Run, "KASH_PREDICATE_EFFECT", span, "stream predicates cannot resolve executables")
	}
	if name == "" {
		return failure(c.Run, "TOOL_MISSING", span, "tool name is empty")
	}
	if c.Engine == nil || (c.DependencyObserver == nil && c.DependencyContextObserver == nil) {
		return failure(c.Run, "PHASE_INVALID", span, "tool requires a build or source session")
	}
	if c.Program != nil && c.Program.SessionTool != nil {
		resolved := c.Program.SessionTool(c.Program.SessionToolState, c.Run, name, c.Cwd)
		if resolved == "" {
			return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
		}
		defer mem.FreeString(c.Run, resolved)
		name = resolved
	}
	key := core.ResourceKey{Kind: core.ResourceTool, Name: name}
	if !c.Dependency(key) {
		return Result{Waiting: true}
	}
	current := c.Value(key)
	if !current.OK || current.Value.Kind != core.String || current.Value.Text == "" {
		return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
	}
	file := core.ResourceKey{Kind: core.ResourceFile, Name: current.Value.Text}
	if !c.Dependency(file) {
		return Result{Waiting: true}
	}
	c.Value(file)
	value := current.Value.Clone(c.Run)
	value.ToolName = core.NewString(c.Run, name).Text
	return Result{Value: value}
}
