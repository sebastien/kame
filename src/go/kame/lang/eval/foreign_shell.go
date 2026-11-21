package eval

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
	"maps"
	"path"
	"slices"
	"strings"
)

type foreignShell struct {
	id         int64
	adapter    string
	functions  map[string]bool
	env        map[string]string
	unsets     map[string]bool
	introduced map[string]string
}

type foreignShellStep struct {
	id     int64
	status int64
	done   bool
	worker *foreignShell
	close  func(int64)
}

func freeForeignShellStep(_ mem.Allocator, value any) {
	step := value.(*foreignShellStep)
	if !step.done {
		step.close(step.id)
		if step.worker != nil && step.worker.id != 0 {
			step.close(step.worker.id)
			step.worker.id = 0
		}
	}
}

func (p *Program) foreignShellCommand(graph, stage *expr.Expr, head string, c *Context) (Result, bool) {
	loading := head == "source"
	namespace, function, qualified := strings.Cut(head, "/")
	var worker *foreignShell
	if qualified && c.DirectUnit != nil {
		worker = c.DirectUnit.shells[namespace]
	}
	if !loading && worker == nil {
		return Result{}, false
	}
	if c.DirectUnit == nil || c.Unit != c.DirectUnit || c.asyncHandle != nil ||
		c.pureValueBlock || c.Finalizing || c.PrepareDeferred != nil || c.functionStage != nil || c.InputClosed ||
		len(graph.Items) != 1 || graph.Topology || graph.Async || graph.Kind != expr.CommandGraph ||
		len(graph.Body) != 0 || len(c.StageEnvironment) != 0 || c.StageDirectory != "" || !commandOnlyWords(stage) {
		return failure(c.Run, "FEATURE_UNSUP", graph.Span, "foreign shells require a direct foreground command"), true
	}
	if c.SupportsForeignShell == nil || c.CloseForeignShell == nil {
		return failure(c.Run, "FEATURE_UNSUP", graph.Span, "host does not support foreign shells"), true
	}
	words := stage.Items
	unsets := map[string]bool{}
	var adapter string
	if loading {
		if len(words) < 5 || !literalSetupWord(words[1]) || !literalSetupWord(words[3]) ||
			words[3].Parts[0].Text != "as" || !literalSetupWord(words[4]) || !validForeignNamespace(words[4].Parts[0].Text) {
			return failure(c.Run, "PARSE_ERR", graph.Span, "expected source SHELL PATH as NAME [unset ENV-NAME …]"), true
		}
		adapter = words[1].Parts[0].Text
		if !expr.ValidBindingName(adapter) || !c.SupportsForeignShell(adapter) {
			return failure(c.Run, "FEATURE_UNSUP", words[1].Span, "unsupported foreign shell"), true
		}
		namespace = words[4].Parts[0].Text
		worker = c.Unit.shells[namespace]
		if len(words) > 5 {
			if len(words) == 6 || !literalSetupWord(words[5]) || words[5].Parts[0].Text != "unset" {
				return failure(c.Run, "PARSE_ERR", graph.Span, "expected unset ENV-NAME …"), true
			}
			for _, word := range words[6:] {
				if !literalSetupWord(word) || !expr.ValidEnvironmentName(word.Parts[0].Text) || unsets[word.Parts[0].Text] {
					return failure(c.Run, "PARSE_ERR", word.Span, "invalid or duplicate foreign shell unset name"), true
				}
				unsets[word.Parts[0].Text] = true
			}
		}
	} else {
		adapter = worker.adapter
	}
	if !authorizeExecutable(c, adapter) {
		return failure(c.Run, "CAP_DENIED", graph.Span, "foreign shells require unrestricted run authority"), true
	}
	if p.DryRun {
		return Result{}, true
	}
	start, end, callPath := c.operationStart, c.operationEnd, c.CallPath
	c.operationStart, c.operationEnd, c.CallPath = graph.Span.Start, graph.Span.End, callPath+"/foreign-shell"
	defer func() { c.operationStart, c.operationEnd, c.CallPath = start, end, callPath }()
	step, _ := c.OperationState().(*foreignShellStep)
	if step != nil {
		if step.done {
			return foreignShellStatus(c, graph, step.status), true
		}
		if c.Completion().RequestID != step.id {
			return Result{Waiting: true}, true
		}
		completion := c.TakeCompletion()
		defer completion.Value.Free(c.Run)
		if completion.Diagnostic.Code != "" {
			if loading {
				c.CloseForeignShell(step.id)
			} else {
				invalidateForeignShell(c, worker)
			}
			step.done = true
			return Result{Diagnostic: completion.Diagnostic}, true
		}
		if loading {
			worker = &foreignShell{id: step.id, adapter: strings.Clone(adapter), unsets: unsets, introduced: map[string]string{}}
		}
		step.status = host.PayloadInt(completion.Value, "status")
		if loading && step.status != 0 {
			c.CloseForeignShell(worker.id)
			step.done = true
			return foreignShellStatus(c, graph, step.status), true
		}
		if r := applyForeignReply(c, graph.Span, loading, worker, completion.Value); r.Diagnostic.Code != "" {
			invalidateForeignShell(c, worker)
			step.done = true
			return r, true
		}
		if loading {
			if c.Unit.shells == nil {
				c.Unit.shells = make(map[string]*foreignShell)
			}
			c.Unit.shells[strings.Clone(namespace)] = worker
		}
		step.done = true
		return foreignShellStatus(c, graph, step.status), true
	}
	if loading && worker != nil && worker.id != 0 {
		return failure(c.Run, "DEF_INVALID", graph.Span, "foreign shell namespace is already active"), true
	}
	if !loading && (worker.id == 0 || !worker.functions[function]) {
		return failure(c.Run, "REF_MISSING", graph.Span, "foreign shell function is not available: "+head), true
	}
	var args []string
	command := function
	if loading {
		r := p.commandWord(words[2], c, "")
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r, true
		}
		name, ok := processScalar(c.Run, r.Value)
		r.Free(c.Run)
		if !ok || name == "" {
			return failure(c.Run, "EXPR_INVALID", words[2].Span, "source requires a nonempty path"), true
		}
		defer mem.FreeString(c.Run, name)
		filename := name
		if rest, homeRelative := strings.CutPrefix(name, "~/"); homeRelative {
			home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
			if !path.IsAbs(home) {
				return failure(c.Run, "RES_INVALID", words[2].Span, "foreign source requires an absolute HOME for ~/"), true
			}
			filename = path.Join(home, rest)
		}
		command = c.CanonicalPath(filename)
		defer mem.FreeString(c.Run, command)
		if !path.IsAbs(command) {
			return failure(c.Run, "RES_INVALID", words[2].Span, "foreign source requires a local file path"), true
		}
		if !c.Allows(Read, command) {
			return failure(c.Run, "CAP_DENIED", words[2].Span, "foreign source path requires read authority"), true
		}
	} else {
		var r Result
		args, r = p.foreignShellArgs(stage, c)
		if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
			return r, true
		}
		defer freeForeignStrings(c.Run, args)
	}
	fields := []core.RecordField{
		{Key: "shell", Value: core.NewString(c.Run, adapter)},
		{Key: "command", Value: core.NewString(c.Run, command)},
		{Key: host.FieldCwd, Value: core.NewString(c.Run, c.Cwd)},
		{Key: "args", Value: foreignStringList(c.Run, args)},
	}
	if loading {
		fields = append(fields, core.RecordField{Key: host.FieldEnvironment, Value: foreignStringList(c.Run, c.effectiveEnvironment())})
	} else {
		var set, unset []string
		env := foreignEnvironment(c.effectiveEnvironment())
		for _, name := range slices.Sorted(maps.Keys(env)) {
			if value, ok := worker.env[name]; !ok || value != env[name] {
				set = append(set, name+"="+env[name])
			}
		}
		for _, name := range slices.Sorted(maps.Keys(worker.env)) {
			if _, ok := env[name]; !ok {
				unset = append(unset, name)
			}
		}
		fields = append(fields, core.RecordField{Key: "worker", Value: core.Value{Kind: core.Int, Int: worker.id}},
			core.RecordField{Key: "set", Value: foreignStringList(c.Run, set)},
			core.RecordField{Key: "unset", Value: foreignStringList(c.Run, unset)})
	}
	payload := core.Value{Kind: core.Record, Record: fields}
	id := c.Submit(host.RequestForeignShell, payload)
	for i := range fields {
		fields[i].Value.Free(c.Run)
	}
	if id == 0 {
		return failure(c.Run, "HOST_FAIL", graph.Span, "foreign shell request was not accepted"), true
	}
	c.SetOperationState(&foreignShellStep{id: id, worker: worker, close: c.CloseForeignShell}, freeForeignShellStep)
	return Result{Waiting: true}, true
}

func validForeignNamespace(name string) bool {
	switch name {
	case "x", "env", "std", "module":
		return false
	}
	return expr.ValidBindingName(name)
}

func foreignStringList(a mem.Allocator, strings []string) core.Value {
	values := make([]core.Value, 0, len(strings))
	for _, text := range strings {
		values = append(values, core.NewString(a, text))
	}
	return core.Value{Kind: core.List, List: values}
}

func freeForeignStrings(a mem.Allocator, values []string) {
	for _, value := range values {
		mem.FreeString(a, value)
	}
}

func (p *Program) foreignShellArgs(stage *expr.Expr, c *Context) ([]string, Result) {
	args := commandArguments(stage)
	r := p.argumentValues(c.Scope, args, c, stage.Span)
	if r.Result.Waiting || r.Result.Completed || r.Result.Diagnostic.Code != "" {
		return nil, r.Result
	}
	defer freeValuesWithCallables(c.Run, r.Values)
	var argv []string
	for i, value := range r.Values {
		values := []core.Value{value}
		if args[i].Kind == expr.ListSpread {
			if value.Kind != core.List {
				freeForeignStrings(c.Run, argv)
				return nil, failure(c.Run, "EXPR_INVALID", args[i].Span, "foreign command spread requires a list")
			}
			values = value.List
		} else if value.Kind == core.Nil {
			continue
		}
		for _, item := range values {
			text, ok := processScalar(c.Run, item)
			if !ok {
				freeForeignStrings(c.Run, argv)
				return nil, failure(c.Run, "EXPR_INVALID", args[i].Span, "foreign command arguments must be argv scalars")
			}
			argv = append(argv, text)
		}
	}
	return argv, Result{}
}

func foreignEnvironment(values []string) map[string]string {
	result := map[string]string{}
	for _, assignment := range values {
		name, value, ok := strings.Cut(assignment, "=")
		if ok && expr.ValidEnvironmentName(name) {
			result[strings.Clone(name)] = strings.Clone(value)
		}
	}
	return result
}

func applyForeignReply(c *Context, span source.Span, loading bool, worker *foreignShell, value core.Value) Result {
	before := foreignSnapshot(host.PayloadList(value, "before"))
	after := foreignSnapshot(host.PayloadList(value, "after"))
	if before == nil || after == nil {
		return failure(c.Run, "HOST_FAIL", span, "invalid foreign shell snapshots")
	}
	current := foreignEnvironment(c.effectiveEnvironment())
	for name := range before {
		if _, kept := after[name]; kept {
			continue
		}
		old, present := current[name]
		introduced, owned := worker.introduced[name]
		if present && !worker.unsets[name] && (!owned || introduced != old) {
			return failure(c.Run, "CAP_DENIED", span, "foreign shell removal was not authorized for "+name)
		}
	}
	for name := range before {
		if _, kept := after[name]; !kept {
			c.setEnvironment(name, "", false)
			delete(worker.introduced, name)
		}
	}
	for name, next := range after {
		if old, exists := before[name]; exists && old == next {
			continue
		}
		old, present := current[name]
		if introduced, owned := worker.introduced[name]; !present || owned && introduced == old {
			worker.introduced[strings.Clone(name)] = strings.Clone(next)
		}
		c.setEnvironment(name, next, true)
	}
	if loading {
		worker.functions = map[string]bool{}
		for _, value := range host.PayloadList(value, "functions") {
			name := value.Text
			if name != "" && !strings.Contains(name, "/") {
				worker.functions[strings.Clone(name)] = true
			}
		}
	}
	worker.env = foreignEnvironment(c.effectiveEnvironment())
	return Result{}
}

func foreignSnapshot(values []core.Value) map[string]string {
	result := make(map[string]string, len(values))
	for _, entry := range values {
		name, value, ok := strings.Cut(entry.Text, "=")
		if entry.Kind != core.String || !ok || !expr.ValidEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return nil
		}
		if _, duplicate := result[name]; duplicate {
			return nil
		}
		result[name] = value
	}
	return result
}

func invalidateForeignShell(c *Context, worker *foreignShell) {
	c.CloseForeignShell(worker.id)
	worker.id = 0
}

func foreignShellStatus(c *Context, graph *expr.Expr, status int64) Result {
	if status == 0 || graph.AcceptExit {
		return Result{}
	}
	value := core.Value{Kind: core.Record, Record: []core.RecordField{{Key: "status", Value: core.Value{Kind: core.Int, Int: status}}}}
	return captureValue(c.Run, value, graph.Span, false, false)
}
