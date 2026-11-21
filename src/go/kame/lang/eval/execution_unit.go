package eval

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"maps"
	"slices"
	"strings"
)

type environmentEdit struct {
	value   string
	present bool
}

// ExecutionUnit holds mutations shared by the functions of a script, session,
// or recipe. A capture inherits a copy of the parent's edits.
type ExecutionUnit struct {
	environment map[string]environmentEdit
	directory   string
	previous    string
	shells      map[string]*foreignShell
}

func (u *ExecutionUnit) Directory(base string) string {
	if u != nil && u.directory != "" {
		return u.directory
	}
	return base
}

func (u *ExecutionUnit) EnvironmentValue(base []string, name string) (string, bool) {
	if u != nil {
		if edit, ok := u.environment[name]; ok {
			return edit.value, edit.present
		}
	}
	for i := len(base) - 1; i >= 0; i-- {
		if key, value, ok := strings.Cut(base[i], "="); ok && key == name {
			return value, true
		}
	}
	return "", false
}

func (c *Context) commandDirectory() string {
	if c.StageDirectory != "" {
		return c.StageDirectory
	}
	return c.Cwd
}

func (c *Context) ShellPayload(script string) core.Value {
	directory := c.commandDirectory()
	exact := c.HasCommandEnvironment()
	if exact || c.StageDirectory != "" || (c.Unit != nil && c.Unit.directory != "") {
		return host.ShellProcessPayload(c.Run, script, directory, c.CommandEnvironment(), exact)
	}
	return host.ProcessPayload(c.Run, script)
}

func (c *Context) EnvironmentEdit(name string) (core.Value, bool) {
	if c.Unit != nil {
		if edit, ok := c.Unit.environment[name]; ok {
			if edit.present {
				return core.NewString(c.Run, edit.value), true
			}
			c.Observe(core.ResourceKey{Kind: core.ResourceEnvironment, Name: absentEnvironmentPrefix + name},
				core.ObservationValue, core.ValueSignature(core.Value{Kind: core.Nil}))
			return core.Value{Kind: core.Nil}, true
		}
	}
	return core.Value{}, false
}

func (c *Context) setEnvironment(name, value string, present bool) {
	if c.Unit == nil {
		c.Unit = &ExecutionUnit{}
	}
	if c.Unit.environment == nil {
		c.Unit.environment = make(map[string]environmentEdit)
	}
	c.Unit.environment[strings.Clone(name)] = environmentEdit{value: strings.Clone(value), present: present}
}

func (c *Context) effectiveEnvironment() []string {
	if c.Unit == nil || len(c.Unit.environment) == 0 {
		return c.Environment
	}
	edits := c.Unit.environment
	environment := make([]string, 0, len(c.Environment)+len(edits))
	for _, assignment := range c.Environment {
		name, _, _ := strings.Cut(assignment, "=")
		if _, edited := edits[name]; !edited {
			environment = append(environment, assignment)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(edits)) {
		if edit := edits[name]; edit.present {
			environment = append(environment, name+"="+edit.value)
		}
	}
	return environment
}

// CommandEnvironment applies inherited stage overrides over the current unit.
func (c *Context) CommandEnvironment() []string {
	environment := c.effectiveEnvironment()
	if len(c.StageEnvironment) == 0 {
		return environment
	}
	environment = slices.Clone(environment)
	for _, assignment := range c.StageEnvironment {
		environment = replaceEnvironmentAssignment(environment, assignment)
	}
	return environment
}

func (c *Context) HasCommandEnvironment() bool {
	return c.hasUnitEnvironment() || len(c.StageEnvironment) != 0
}

func (c *Context) hasUnitEnvironment() bool {
	return c.HasEnvironment || (c.Unit != nil && len(c.Unit.environment) != 0)
}

func (c *Context) environmentOverrides(assignments []core.Value) []string {
	edits := 0
	if c.Unit != nil {
		edits = len(c.Unit.environment)
	}
	count := edits + len(c.StageEnvironment) + len(assignments)
	if count == 0 {
		return nil
	}
	names := make(map[string]bool, count)
	if c.Unit != nil {
		for name := range c.Unit.environment {
			names[name] = true
		}
	}
	for _, assignment := range c.StageEnvironment {
		name, _, _ := strings.Cut(assignment, "=")
		names[name] = true
	}
	for _, assignment := range assignments {
		name, _, _ := strings.Cut(assignment.Text, "=")
		names[name] = true
	}
	return slices.Sorted(maps.Keys(names))
}

// Prepared cleanup retains the stage overrides, then binds the unit's current
// environment when the cleanup actually starts.
func setStageEnvironment(a mem.Allocator, setup *core.Value, c *Context) {
	exact := c.hasUnitEnvironment()
	base := c.StageEnvironment
	if exact {
		base = c.CommandEnvironment()
	}
	var masks []string
	for i := range setup.Record {
		field := &setup.Record[i]
		if field.Key != host.FieldEnvironment {
			continue
		}
		masks = c.environmentOverrides(field.Value.List)
		environment := slices.Clone(base)
		for _, assignment := range field.Value.List {
			environment = replaceEnvironmentAssignment(environment, assignment.Text)
		}
		replacement := make([]core.Value, len(environment))
		for i, assignment := range environment {
			replacement[i] = core.Value{Kind: core.String, Text: assignment}
		}
		updated := core.NewList(a, replacement)
		field.Value.Free(a)
		field.Value = updated
		break
	}
	if len(masks) != 0 {
		values := make([]core.Value, len(masks))
		for i, name := range masks {
			values[i] = core.Value{Kind: core.String, Text: name}
		}
		setup.Record = append(setup.Record, core.RecordField{
			Key: owned(a, stageEnvironmentOverrides), Value: core.NewList(a, values),
		})
	}
	if exact {
		setup.Record = append(setup.Record, core.RecordField{Key: owned(a, host.FieldEnvironmentExact), Value: core.Value{Kind: core.Bool, Bool: true}})
	}
}

func replaceEnvironmentAssignment(environment []string, assignment string) []string {
	name, _, _ := strings.Cut(assignment, "=")
	prefix := name + "="
	for i, value := range environment {
		if strings.HasPrefix(value, prefix) {
			environment[i] = assignment
			return environment
		}
	}
	return append(environment, assignment)
}

func (p *Program) withCaptureUnit(e *expr.Expr, c *Context, run func(*expr.Expr, *Context) Result) Result {
	parent, directory := c.Unit, c.Cwd
	start, end, path := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = e.Span.Start, e.Span.End, path+"/capture-unit"
	unit, _ := c.OperationState().(*ExecutionUnit)
	if unit == nil {
		unit = &ExecutionUnit{directory: directory}
		if parent != nil {
			unit.environment = maps.Clone(parent.environment)
			unit.previous = parent.previous
		}
		c.SetOperationState(unit, nil)
	}
	c.operationStart, c.operationEnd, c.CallPath = start, end, path
	c.Unit, c.Cwd = unit, unit.Directory(directory)
	r := run(e, c)
	c.Unit, c.Cwd = parent, directory
	if r.Diagnostic.Exit.Requested && (e.Kind == expr.CommandCapture || e.CommandListCapture && e.ActionPrefix != "$&(") {
		return finishCaptureExit(e, c, r)
	}
	return r
}

func finishCaptureExit(e *expr.Expr, c *Context, r Result) Result {
	inspect := e.ActionPrefix == "$?("
	if !r.Diagnostic.ExitOnly() && (!inspect || !inspectCaptureExit(r.Diagnostic)) {
		r.Diagnostic = captureExitFailure(r.Diagnostic)
		return r
	}
	if inspect && r.Value.Kind == core.Record {
		r.Diagnostic.Free(c.Run)
		return Result{Value: r.Value}
	}
	status := int64(r.Diagnostic.Exit.Status)
	defer r.Diagnostic.Free(c.Run)
	stdout := r.Value
	if stdout.Kind == core.Nil {
		stdout = core.NewString(c.Run, "")
	}
	defer freeValueWithCallables(c.Run, &stdout)
	completion := core.Value{Kind: core.Record, Record: []core.RecordField{
		{Key: "status", Value: core.Value{Kind: core.Int, Int: status}},
		{Key: "stdout", Value: stdout},
	}}
	if inspect {
		return captureExitOutcome(c, e, completion, stdout, r.Diagnostic)
	}
	return captureValue(c.Run, completion, e.Span, e.AcceptExit, false)
}
