package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/expr"
	"kame/lang/source"
	"path"
	"strings"
)

type directoryChange struct {
	directory string
	request   int64
	done      bool
}

func (p *Program) directoryCommand(graph, stage, head *expr.Expr, alias []string, c *Context) Result {
	if len(graph.Items) != 1 || graph.Topology || graph.Async || len(graph.Body) != 0 || c.PrepareDeferred != nil || !commandOnlyWords(stage) {
		return failure(c.Run, "EXPR_INVALID", head.Span, "cd requires one foreground stage without redirection or setup")
	}
	if len(alias)+len(stage.Items)-1 > 1 {
		return failure(c.Run, "EXPR_INVALID", head.Span, "cd accepts at most one directory")
	}
	r := p.changeDirectory(graph, stage.Items[1:], alias, c)
	if r.Waiting || r.Completed || (r.Diagnostic.Code != "" && !eligibleCommandExit(r.Diagnostic)) {
		return r
	}
	if graph.ActionPrefix == "$?(" {
		status := int64(r.Diagnostic.Cause.Status)
		r.Free(c.Run)
		completion := core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: status}}}}
		return captureOutcome(c, graph, completion, core.Value{Kind: core.String})
	}
	if r.Diagnostic.Code != "" && !graph.AcceptExit {
		return r
	}
	r.Free(c.Run)
	if graph.Kind == expr.CommandCapture {
		return Result{Value: core.NewString(c.Run, "")}
	}
	return Result{}
}

func (p *Program) changeDirectory(graph *expr.Expr, words []*expr.Expr, alias []string, c *Context) Result {
	if p.DryRun {
		return Result{}
	}
	if c.Engine == nil || c.Requests == nil {
		return failure(c.Run, "HOST_FAIL", graph.Span, "cd requires an execution host")
	}
	start, end, path := c.operationStart, c.operationEnd, c.CallPath
	key := path + "/directory"
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, key
	state, _ := c.OperationState().(*directoryChange)
	c.operationStart, c.operationEnd, c.CallPath = start, end, path
	if state == nil {
		directory, r := p.directoryOperand(graph.Span, words, alias, c)
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r
		}
		state = &directoryChange{directory: directory}
		// The builtin probes only directory metadata. File access still uses
		// the unit's normal read and write grants.
		payload := host.FilePayload(c.Run, host.OpDirectoryCheck, state.directory)
		state.request = c.Requests.Submit(c.Engine.NodeID(), c.Engine.Generation(), c.Engine.Attempt(), host.RequestReadFile, payload)
		payload.Free(c.Run)
		if state.request == 0 {
			return failure(c.Run, "HOST_FAIL", graph.Span, "cd directory check was not accepted")
		}
		c.Engine.Submit(state.request)
		c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, key
		c.SetOperationState(state, nil)
		c.operationStart, c.operationEnd, c.CallPath = start, end, path
		return Result{Waiting: true}
	}
	if state.done {
		return Result{}
	}
	if c.Completion().RequestID != state.request {
		return Result{Waiting: true}
	}
	completion := c.TakeCompletion()
	defer completion.Value.Free(c.Run)
	defer completion.Diagnostic.Free(c.Run)
	if completion.Diagnostic.Code != "" && completion.Diagnostic.Code != "FS_ERR" {
		return Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
	}
	c.UnvalidatedRead()
	if completion.Diagnostic.Code != "" || completion.Value.Kind != core.Bool || !completion.Value.Bool {
		return directoryFailure(c, graph.Span, "cd: not a directory: "+state.directory)
	}
	if c.Unit == nil {
		c.Unit = &ExecutionUnit{}
	}
	c.Unit.previous, c.Unit.directory = strings.Clone(c.Cwd), state.directory
	c.Cwd = state.directory
	state.done = true
	return Result{}
}

func (p *Program) directoryOperand(span source.Span, words []*expr.Expr, alias []string, c *Context) (string, Result) {
	home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
	directory := home
	if len(alias) != 0 {
		directory = alias[0]
	}
	if len(words) != 0 {
		previous := c.sequencedOperand
		c.sequencedOperand = true
		r := p.commandWord(words[0], c, "cd")
		c.sequencedOperand = previous
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return "", r
		}
		defer r.Free(c.Run)
		if !r.Value.IsPathText() || r.Value.Text == "" || strings.ContainsRune(r.Value.Text, 0) {
			return "", failure(c.Run, "EXPR_INVALID", words[0].Span, "cd requires a nonempty directory path")
		}
		directory = r.Value.Text
	}
	if directory == "-" {
		if c.Unit == nil || c.Unit.previous == "" {
			return "", directoryFailure(c, span, "cd: no previous directory")
		}
		directory = c.Unit.previous
	} else if directory == "~" || strings.HasPrefix(directory, "~/") {
		if home == "" {
			return "", directoryFailure(c, span, "cd: HOME is unset")
		}
		directory = home + directory[1:]
	}
	if directory == "" {
		return "", directoryFailure(c, span, "cd: HOME is unset")
	}
	if !path.IsAbs(directory) {
		directory = path.Join(c.Cwd, directory)
	}
	return strings.Clone(path.Clean(directory)), Result{}
}

func directoryFailure(c *Context, span source.Span, message string) Result {
	r := failure(c.Run, "RECIPE_FAIL", span, message)
	r.Diagnostic.Cause = diagnostic.Cause{Kind: owned(c.Run, "process"), Status: 1, HasStatus: true}
	return r
}
