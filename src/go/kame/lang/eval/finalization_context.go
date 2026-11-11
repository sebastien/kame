package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
)

// Finalization can outlive the caller's stack, lexical scope and host wait.
func retainFinalizerContext(a mem.Allocator, c *Context) Context {
	r := *c
	r.Engine, r.operationArguments = nil, nil
	r.Run = a
	r.Scope.Retain()
	r.Cwd, r.Source, r.CallPath = owned(a, c.Cwd), owned(a, c.Source), owned(a, c.CallPath)
	r.OperationName = "finally!"
	r.Effects, r.WritePaths, r.RenderStack, r.activeCapabilities = nil, nil, nil, nil
	r.Args, r.Inputs, r.Outputs = cloneFinalizerValues(a, c.Args), cloneFinalizerValues(a, c.Inputs), cloneFinalizerValues(a, c.Outputs)
	r.Environment, r.Grants, r.RuleFrames, r.Frames = nil, nil, nil, nil
	for i := range c.Environment {
		r.Environment = append(r.Environment, owned(a, c.Environment[i]))
	}
	for i := range c.Grants {
		g := Grant{Capability: c.Grants[i].Capability}
		for j := range c.Grants[i].Names {
			g.Names = append(g.Names, owned(a, c.Grants[i].Names[j]))
		}
		r.Grants = append(r.Grants, g)
	}
	for i := range c.RuleFrames {
		f := c.RuleFrames[i]
		f.Inputs, f.Outputs, f.NewerInputs = cloneFinalizerValues(a, f.Inputs), cloneFinalizerValues(a, f.Outputs), cloneFinalizerValues(a, f.NewerInputs)
		r.RuleFrames = append(r.RuleFrames, f)
	}
	for i := range c.Frames {
		f := c.Frames[i]
		f.Kind, f.Label, f.Source = owned(a, f.Kind), owned(a, f.Label), owned(a, f.Source)
		r.Frames = append(r.Frames, f)
	}
	return r
}

func cloneFinalizerValues(a mem.Allocator, values []core.Value) []core.Value {
	r := make([]core.Value, len(values))
	for i := range values {
		r[i] = borrowValue(a, values[i])
	}
	return r
}

func freeFinalizerContext(a mem.Allocator, c *Context) {
	c.Scope.Free()
	mem.FreeString(a, c.Cwd)
	mem.FreeString(a, c.Source)
	mem.FreeString(a, c.CallPath)
	freeValuesWithCallables(a, c.Args)
	freeValuesWithCallables(a, c.Inputs)
	freeValuesWithCallables(a, c.Outputs)
	for i := range c.Environment {
		mem.FreeString(a, c.Environment[i])
	}
	mem.FreeSlice(nil, c.Environment)
	for i := range c.Grants {
		for j := range c.Grants[i].Names {
			mem.FreeString(a, c.Grants[i].Names[j])
		}
		mem.FreeSlice(nil, c.Grants[i].Names)
	}
	mem.FreeSlice(nil, c.Grants)
	for i := range c.RuleFrames {
		freeValuesWithCallables(a, c.RuleFrames[i].Inputs)
		freeValuesWithCallables(a, c.RuleFrames[i].Outputs)
		freeValuesWithCallables(a, c.RuleFrames[i].NewerInputs)
	}
	mem.FreeSlice(nil, c.RuleFrames)
	for i := range c.Frames {
		mem.FreeString(a, c.Frames[i].Kind)
		mem.FreeString(a, c.Frames[i].Label)
		mem.FreeString(a, c.Frames[i].Source)
	}
	mem.FreeSlice(nil, c.Frames)
	FreeEffects(a, c.Effects)
	c.dependencyFailure.Free(a)
	for i := range c.WritePaths {
		mem.FreeString(a, c.WritePaths[i])
	}
	mem.FreeSlice(nil, c.WritePaths)
	*c = Context{}
}

func finalizerFailure(a mem.Allocator, primary *diagnostic.Diagnostic, secondary *diagnostic.Diagnostic) {
	if secondary.Code == "" {
		return
	}
	if primary.Code == "" {
		*primary = secondary.Clone(a)
		return
	}
	if primary.Code != secondary.Code || primary.Message != secondary.Message {
		message := owned(a, secondary.Code+": "+secondary.Message)
		if secondary.Source != "" {
			primary.Related = append(primary.Related, diagnostic.Related{Message: message, Source: owned(a, secondary.Source), Span: secondary.Span})
		} else {
			primary.Notes = append(primary.Notes, message)
		}
	}
	for i := range secondary.Notes {
		primary.Notes = append(primary.Notes, owned(a, secondary.Notes[i]))
	}
	for i := range secondary.Related {
		r := secondary.Related[i]
		r.Message, r.Source = owned(a, r.Message), owned(a, r.Source)
		primary.Related = append(primary.Related, r)
	}
	primary.Protected = primary.Protected || secondary.Protected || secondary.Code == "CAP_DENIED" || secondary.Code == "EXEC_CANCELLED"
	primary.Cancelled = primary.Cancelled || secondary.Cancelled || secondary.Code == "EXEC_CANCELLED"
}
