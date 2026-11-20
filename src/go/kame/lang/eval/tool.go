package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/source"
	"path/filepath"
	"strings"
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
	if c.Unit != nil {
		if edit, written := c.Unit.environment["PATH"]; written && !strings.ContainsRune(name, '/') {
			return c.toolOnUnitPath(name, edit, span)
		}
		if c.Program != nil && c.Unit.directory != "" && c.Cwd != c.Program.DefinitionCwd && !strings.ContainsRune(name, '/') {
			value, present := c.Unit.EnvironmentValue(c.Environment, "PATH")
			return c.toolOnUnitPath(name, environmentEdit{value: value, present: present}, span)
		}
	}
	if c.Program != nil && c.Program.SessionTool != nil {
		resolved := c.Program.SessionTool(c.Program.SessionToolState, c.Run, name, c.Cwd)
		if resolved == "" {
			return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
		}
		defer mem.FreeString(c.Run, resolved)
		name = resolved
	}
	return c.trackedToolPath(name, span)
}

// Use the usual tracked tool resources for explicit candidates, so each host
// can verify executability and the selected file without seeing the unit store.
func (c *Context) toolOnUnitPath(name string, edit environmentEdit, span source.Span) Result {
	if c.Program != nil {
		prefix := name + "="
		for i := len(c.Program.ToolOverrides) - 1; i >= 0; i-- {
			if override, ok := strings.CutPrefix(c.Program.ToolOverrides[i], prefix); ok {
				name = override
				break
			}
		}
	}
	if strings.ContainsRune(name, '/') {
		candidate := canonicalPath(c.Run, c.Cwd, name)
		defer mem.FreeString(c.Run, candidate)
		return c.trackedToolPath(candidate, span)
	}
	if !edit.present {
		return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
	}
	for _, directory := range strings.Split(edit.value, string(filepath.ListSeparator)) {
		candidate := canonicalPath(c.Run, c.Cwd, filepath.ToSlash(filepath.Join(directory, name)))
		r := c.trackedToolPath(candidate, span)
		mem.FreeString(c.Run, candidate)
		if r.Diagnostic.Code != "TOOL_MISSING" {
			return r
		}
		r.Free(c.Run)
	}
	return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
}

func (c *Context) trackedToolPath(name string, span source.Span) Result {
	if c.DirectHostRequests && strings.ContainsRune(name, '/') {
		name = canonicalPath(c.Run, c.Cwd, name)
		defer mem.FreeString(c.Run, name)
	}
	key := core.ResourceKey{Kind: core.ResourceTool, Name: name}
	if !c.Dependency(key) {
		return Result{Waiting: true}
	}
	current := c.Value(key)
	if !current.OK || current.Value.Kind != core.String || current.Value.Text == "" {
		return failure(c.Run, "TOOL_MISSING", span, "cannot resolve executable: "+name)
	}
	if !c.DirectHostRequests {
		file := core.ResourceKey{Kind: core.ResourceFile, Name: current.Value.Text}
		if !c.Dependency(file) {
			return Result{Waiting: true}
		}
		c.Value(file)
	}
	value := current.Value.Clone(c.Run)
	value.ToolName = core.NewString(c.Run, name).Text
	return Result{Value: value}
}
