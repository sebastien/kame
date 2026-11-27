package main

import (
	"io"
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/source"
	"kame/operations"
	"kame/ownership"
	"kame/program"
	"os"
	"path"
	"slices"

	"strconv"
	"syscall"
	"time"
)

func runSession(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	inv := cli.ParseRun(args)
	applyInvocationPresentation(&inv)
	defer inv.Free()
	return runParsedSession(inv, in, out, errOut)
}

func runPrimarySession(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	inv := cli.Parse("", args)
	applyInvocationPresentation(&inv)
	defer inv.Free()
	return runParsedSession(inv, in, out, errOut)
}

type exprEffectOutput struct {
	Program *program.Program
	Out     io.Writer
	Err     io.Writer
}

func writeExprEffect(value any, effect eval.Effect) {
	output := value.(*exprEffectOutput)
	previousOutput := setNativeOutputDeadline(output.Program, effect.FinalizationDeadlineNS)
	defer restoreNativeOutputDeadline(previousOutput)
	if effect.Kind == eval.EffectErr {
		dashboardRaw(output.Err, effect.Data, posix.StderrIsTerminal())
		output.Err.Write(effect.Data)
		return
	}
	if effect.Kind == eval.EffectOut {
		dashboardRaw(output.Err, effect.Data, posix.StdoutIsTerminal())
		output.Out.Write(effect.Data)
	}
}

func runParsedSession(inv cli.Invocation, in io.Reader, out io.Writer, errOut io.Writer) int {
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		return 2
	}
	configureDiagnosticPresentation(inv)
	var fragments []program.Fragment
	var storage [][]byte
	var names []string
	var loaded []buildSource
	defer freeRunSources(&loaded)
	for i := range inv.Inputs {
		input := inv.Inputs[i]
		if input.Kind == "interactive" {
			continue
		}
		name, text := input.Value, input.Value
		if input.Kind == "file" || input.Kind == "discover" {
			file := loadRunSourceInput(inv, input, fragments, diagnosticWriter(out, errOut, inv.JSON))
			loaded = append(loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
			}
			if i == 0 && input.Lang == "km" && file.RuleWork {
				inv.NoDefaultGrants = false
			}
			for j := range file.Parts {
				entries := input.Entries
				if j+1 != len(file.Parts) {
					entries = nil
				}
				fragments = append(fragments, program.Fragment{Name: file.Parts[j].Name, Text: file.Parts[j].Text, SourceDirectory: file.Parts[j].SourceDirectory, Signature: file.Parts[j].Signature, Offset: file.Parts[j].Offset, Includes: file.Parts[j].Includes, Lang: file.Parts[j].Lang, Entries: entries, Inline: j+1 != len(file.Parts), SkipStatements: len(input.Entries) != 0, Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
			}
			continue
		} else if input.Kind == "stdin" {
			data, err := io.ReadAll(in)
			if err != nil {
				freeRunInputs(fragments, storage, names)
				sourceError(diagnosticWriter(out, errOut, inv.JSON), "FS_ERR", "cannot read stdin", inv.JSON)
				return 1
			}
			storage = append(storage, data)
			text, name = string(data), "<stdin>"
		} else {

			name = cloneCommandText("<command:" + strconv.Itoa(i+1) + ">")
			names = append(names, name)
		}
		if input.Lang == "km" || input.Lang == "kash" {
			file := inlineRunSourceInput(inv, name, text, input.Lang, fragments, diagnosticWriter(out, errOut, inv.JSON))
			loaded = append(loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
			}
			if i == 0 && input.Lang == "km" && file.RuleWork {
				inv.NoDefaultGrants = false
			}
			for j := range file.Parts {
				entries := input.Entries
				if j+1 != len(file.Parts) {
					entries = nil
				}
				fragments = append(fragments, program.Fragment{Name: file.Parts[j].Name, Text: file.Parts[j].Text, Offset: file.Parts[j].Offset, Includes: file.Parts[j].Includes, Lang: file.Parts[j].Lang, Entries: entries, Inline: true, SkipStatements: len(input.Entries) != 0, Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
			}
			continue
		}
		fragments = append(fragments, program.Fragment{Name: name, Text: text, Lang: input.Lang, Entries: input.Entries, Inline: input.Kind != "file", Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
	}
	defer freeRunInputs(fragments, storage, names)
	directory := inv.Directory
	if !path.IsAbs(directory) {
		buffer := mem.AllocSlice[byte](mem.System, 4096, 4096)
		cwd, err := os.Getwd()
		if err == nil && cwd != "" {
			directory = path.Join(cwd, directory)
		} else {
			directory = cloneCommandText(directory)
		}
		mem.FreeSlice(mem.System, buffer)
	} else {
		directory = cloneCommandText(directory)
	}
	defer mem.FreeString(mem.System, directory)
	defaults := []eval.Grant{{Capability: eval.Read, Names: []string{directory}}, {Capability: eval.Write, Names: []string{directory}}, {Capability: eval.Run}}
	kashFirst := len(inv.Inputs) != 0 && inv.Inputs[0].Lang == "kash"
	if kashFirst {
		defaults = append(defaults, eval.Grant{Capability: eval.Env})
	}
	grants := effectiveGrants(inv.Grants, defaults, inv.NoDefaultGrants || inv.ExplicitGrantsOnly)
	registry := eval.NewRegistry(mem.System)
	defer registry.Free()
	operations.Register(registry)
	environment := mergeEnvironment(posix.Environment(mem.System), inv.Environment)
	defines := inv.Defines
	if inv.Name == "render" {
		defines = nil
	}
	programOptions := program.Options{Host: posix.New(mem.System), Directory: directory, Environment: environment, Defines: defines, Parameters: inv.Parameters, ToolOverrides: inv.ToolOverrides, Shell: inv.Shell, Grants: grants, Jobs: inv.Jobs, CaptureLimit: inv.CaptureLimit, TimeoutMS: inv.TimeoutMS, DryRun: inv.DryRun, Force: inv.Force, CacheDisabled: inv.Force, RetryCount: inv.RetryCount, RetainBytes: inv.RetainBytes, ResolveTool: resolveBuildTool, PrintResult: inv.PrintResult}
	programOptions.RequireRuleWork, programOptions.RequireProcessWork = inv.RequireRuleWork, inv.RequireProcessWork
	for i := range loaded {
		programOptions.CompositionDiscoveries = append(programOptions.CompositionDiscoveries, loaded[i].Discoveries...)
	}
	defer mem.FreeSlice(nil, programOptions.CompositionDiscoveries)
	programOptions.TrackTimings = true
	programOptions.HasInput = inv.Name == "run"
	for i := range inv.Inputs {
		if inv.Inputs[i].Kind == "stdin" {
			programOptions.HasInput = false
		}
	}
	// ponytail: data pipes/files only; interactive TTY input needs foreground
	// process-group handoff rather than the host's isolated cancellation groups.
	if posix.StdinIsTerminal() || inv.Interactive {
		programOptions.HasInput = false
	}
	var ledger *outputLedger
	if ownership.Supported() {
		ledger = mem.Alloc[outputLedger](mem.System)
		ledger.Store = ownership.New(mem.System, directory)
		if len(loaded) == 1 {
			ledger.Source = cleanupSource(&loaded[0])
		}
		ledger.Out, ledger.Err, ledger.JSON = out, errOut, inv.JSON
		programOptions.Ownership, programOptions.OwnershipState = recordProduction, ledger
		programOptions.LockResources, programOptions.UnlockResources, programOptions.ResourcesState = lockProduction, unlockProduction, ledger
		programOptions.AutomaticClean, programOptions.AutomaticCleanState = automaticClean, ledger
	}
	defer freeOutputLedger(ledger)
	compiled := program.CompileSession(mem.System, fragments, registry, programOptions)
	posix.FreeEnvironment(mem.System, environment)
	defer compiled.Free(mem.System)
	if compiled.Session == nil {
		status := 1
		for i := range compiled.Diagnostics {
			emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), compiled.Diagnostics[i], inv.JSON, fragments, loaded)
			if compiled.Diagnostics[i].Code == "OPT_CONFLICT" {
				status = 2
			}
		}
		return status
	}
	session := compiled.Session
	session.AllowExternalSource = kashFirst && !inv.NoDefaultGrants && !inv.ExplicitGrantsOnly && !hasGrant(inv.Grants, eval.Read)
	p := session.Program
	defer session.Free()
	values := make([]core.Value, len(inv.Args))
	for i := range inv.Args {
		values[i] = core.NewString(mem.System, inv.Args[i])
	}
	p.Eval.SetDefinitionArgs(values)
	for i := range values {
		values[i].Free(mem.System)
	}
	mem.FreeSlice(nil, values)
	// A single rule source keeps the established build presentation, parallel
	// target handling, JSON events, freshness and cache path unchanged.
	output := exprEffectOutput{Program: p, Out: out, Err: errOut}
	if inv.JSON && inv.Name != "render" {
		session.SetJSON(true)
	} else {
		p.Eval.SetDefinitionEffectSink(writeExprEffect, &output)
		p.Eval.OutputPassthrough = !inv.Interactive && !inv.Login && !dashboardLive && invocationDescriptors(out, errOut)
	}
	var status int
	if inv.Interactive || inv.Login {
		run := sessionRun{Session: session, Inv: inv, Out: out, Err: errOut, Fragments: fragments, Loaded: loaded, StartedAt: time.Now().UnixNano()}
		status = runInteractive(&run, in)
		run.Last.Free(mem.System)
		if run.Forced {
			return status
		}
	} else {
		status = runSessionWork(session, inv, out, errOut, fragments, loaded)
	}
	p.Eval.BeginFinalization()
	progress := buildProgress{}
	for !p.Eval.AdvanceFinalization() {
		if signal := posix.TakeSignal(); signal < 0 {
			return 128 - signal
		}
		tickCLIProgram(p, 10, errOut, inv.JSON)
		drainEvents(p, out, errOut, inv.JSON, &progress)
	}
	d := p.Eval.FinalizationDiagnostic(mem.System)
	if d.Code != "" {
		emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), d, inv.JSON, fragments, loaded)
		if status == 0 {
			status = processExitStatus(d, inv.Name == "run")
		}
	}
	d.Free(mem.System)
	return status
}

func processExitStatus(d diagnostic.Diagnostic, explicitRun bool) int {
	if !explicitRun || d.Code != "RECIPE_FAIL" || d.Cause.Kind != "process" || !d.Cause.HasStatus {
		return 1
	}
	if d.Cause.HasSignal && d.Cause.Signal != 0 {
		return 128 + d.Cause.Signal
	}
	if d.Cause.Status == 0 {
		return 1
	}
	return d.Cause.Status
}

func runSessionWork(session *program.Session, inv cli.Invocation, out io.Writer, errOut io.Writer, fragments []program.Fragment, loaded []buildSource) int {
	startedAt := time.Now().UnixNano()
	run := sessionRun{Session: session, Inv: inv, Out: out, Err: errOut, Fragments: fragments, Loaded: loaded, StartedAt: startedAt}
	defer run.Last.Free(mem.System)
	for i := 0; i <= len(session.Work); i++ {
		if i == len(session.Work) && !run.joinNeeded() {
			break
		}
		if status, stop := run.step(i); stop {
			return status
		}
	}
	last, hasLast := run.Last, run.HasLast
	if inv.Name == "render" {
		name := "<stdin>"
		if len(fragments) != 0 {
			name = fragments[0].Name
		}
		if inv.JSON {
			action := "render"
			if inv.Check {
				action = "check"
			}
			data := last.Bytes
			if last.Kind == core.String {
				data = []byte(last.Text)
			}
			cli.WriteDataResult(out, "render-result", name, "", action, false, data, !inv.Check)
		} else if inv.Check {
			io.WriteString(errOut, "done render check "+name+"\n")
		} else if hasLast {
			writeValue(out, last)
		}
	} else if hasLast && !inv.JSON && !inv.DryRun {
		clearDashboard(errOut)
		writeValue(out, last)
	}
	return 0
}

// sessionRun drives work items of one session. Batch runs stop at the first
// failure; interactive runs report it and keep the session for the next entry.
type sessionRun struct {
	Session     *program.Session
	Inv         cli.Invocation
	Out, Err    io.Writer
	Fragments   []program.Fragment
	Loaded      []buildSource
	StartedAt   int64
	Progress    buildProgress
	Last        core.Value
	HasLast     bool
	Interactive bool
	// Forced records a forced shutdown: the session ends without finalization.
	Forced bool
	// Capture, when set, receives a clone of the next completed work value.
	Capture *core.Value
	// QuietExits leaves a failed process's exit unreported, as a terminal
	// session does: the program printed its own error and the status is
	// kept. Failure holds the latest interactive diagnostic for explain.
	QuietExits bool
	Failure    diagnostic.Diagnostic
}

func (r *sessionRun) emit(out io.Writer, d diagnostic.Diagnostic, json bool) {
	if r.Interactive && !json {
		r.Failure.Free(mem.System)
		r.Failure = d.Clone(mem.System)
		if r.QuietExits && processExit(d) {
			return
		}
	}
	emitRunDiagnostic(out, d, json, r.Fragments, r.Loaded)
}

// processExit reports a failure whose only cause is a process that exited
// unsuccessfully or died of a signal.
func processExit(d diagnostic.Diagnostic) bool {
	return d.Code == "RECIPE_FAIL" && d.Cause.Kind == "process" && (d.Cause.HasStatus || d.Cause.HasSignal)
}

// joinNeeded reports whether the invocation join has owned work to settle.
func (r *sessionRun) joinNeeded() bool {
	p := r.Session.Program
	return len(p.Eval.Processes) != 0 || len(r.Session.Deferred.Actions) != 0
}

// step runs work item i, or the invocation join when i == len(Work). It
// returns the item status and whether the session must stop.
func (r *sessionRun) step(i int) (int, bool) {
	p := r.Session.Program
	h, status, stop := r.beginWork(i)
	if h == nil {
		return status, stop
	}
	var pending []int64
	if r.Interactive {
		pending = p.PendingNodes()
	}
	for {
		signal := posix.TakeSignal()
		if signal == int(syscall.SIGINT) && r.Interactive {
			return r.interrupt(h, pending, signal)
		}
		if signal != 0 {
			return r.endForSignal(h, signal, false)
		}
		if r.Inv.TimeoutMS > 0 && (time.Now().UnixNano()-r.StartedAt)/1000000 >= r.Inv.TimeoutMS {
			return r.endForTimeout(h)
		}
		tickCLIProgram(p, 10, r.Err, r.Inv.JSON)
		if r.Inv.JSON {
			r.Session.Observe(h)
		}
		drainEvents(p, r.Out, r.Err, r.Inv.JSON, &r.Progress)
		if signal := nativeOutputSignal(); signal != 0 {
			return r.endForSignal(h, signal, true)
		}
		if code := nativeOutputFailure(); code != "" && h.Node.Diagnostic.Code == "" {
			return r.endForOutputFailure(h, code)
		}
		if done, status, stop := r.pollWork(i, h, nil); done {
			return status, stop
		}
	}
}

// endForSignal ends the session on a fatal signal while h runs; h may be
// nil. finalizeFirst begins finalization before cancelling h, as output
// failures do.
func (r *sessionRun) endForSignal(h *program.Handle, signal int, finalizeFirst bool) (int, bool) {
	p := r.Session.Program
	if finalizeFirst {
		p.Eval.BeginFinalization()
	}
	h.Cancel()
	if !finalizeFirst {
		p.Eval.BeginFinalization()
	}
	finishSessionFinalizers(r.Session, h, r.Out, r.Err, r.Inv.JSON, &r.Progress)
	if h != nil && !r.Inv.JSON && len(h.Node.Diagnostic.Notes)+len(h.Node.Diagnostic.Related) != 0 {
		r.emit(r.Err, h.Node.Diagnostic, false)
	}
	h.Free()
	if signal < 0 {
		return 128 - signal, true
	}
	return 128 + signal, true
}

// endForTimeout ends the session when the invocation deadline passes.
func (r *sessionRun) endForTimeout(h *program.Handle) (int, bool) {
	p := r.Session.Program
	p.Eval.BeginFinalizationWith(diagnostic.Diagnostic{Code: "RECIPE_TIMEOUT", Severity: diagnostic.Error, Message: "invocation timed out"})
	h.Cancel()
	finishSessionFinalizers(r.Session, h, r.Out, r.Err, r.Inv.JSON, &r.Progress)
	if h != nil && !r.Inv.JSON && h.Node.Diagnostic.Code == "RECIPE_TIMEOUT" {
		r.emit(r.Err, h.Node.Diagnostic, false)
	} else if !r.Inv.JSON || h == nil || h.Node.Diagnostic.Code != "RECIPE_TIMEOUT" {
		sourceError(diagnosticWriter(r.Out, r.Err, r.Inv.JSON), "RECIPE_TIMEOUT", "invocation timed out", r.Inv.JSON)
	}
	h.Free()
	return 1, true
}

// endForOutputFailure ends the session when cleanup output cannot be delivered.
func (r *sessionRun) endForOutputFailure(h *program.Handle, code string) (int, bool) {
	p := r.Session.Program
	p.Eval.BeginFinalizationWith(diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: "cleanup output delivery failed"})
	h.Cancel()
	finishSessionFinalizers(r.Session, h, r.Out, r.Err, r.Inv.JSON, &r.Progress)
	if h != nil && h.Node.Diagnostic.Code != "" {
		if !r.Inv.JSON {
			r.emit(r.Err, h.Node.Diagnostic, false)
		}
	} else {
		sourceError(diagnosticWriter(r.Out, r.Err, r.Inv.JSON), code, "cleanup output delivery failed", r.Inv.JSON)
	}
	h.Free()
	return 1, true
}

// beginWork starts work item i. On a start failure it reports it and
// returns a nil handle with the item status and whether the session stops.
func (r *sessionRun) beginWork(i int) (*program.Handle, int, bool) {
	started := r.Session.Start(i)
	if started.Diagnostic.Code != "" {
		r.emit(diagnosticWriter(r.Out, r.Err, r.Inv.JSON), started.Diagnostic, r.Inv.JSON)
		status := processExitStatus(started.Diagnostic, r.Inv.Name == "run")
		started.Diagnostic.Free(mem.System)
		return nil, status, !r.Interactive || r.Session.Exit.Requested
	}
	return started.Handle, 0, false
}

// pollWork checks once whether work item i, started as h, has finished.
// When done it reports the item and releases h, returning the item status
// and whether the session must stop.
func (r *sessionRun) pollWork(i int, h *program.Handle, onValue func(core.Value) int) (bool, int, bool) {
	p := r.Session.Program
	if h.Definition && h.Node.Current {
		if r.Interactive {
			r.display(i, h.Node.Latest)
			r.capture(h.Node.Latest)
		}
		if r.Inv.Name == "render" {
			r.Last.Free(mem.System)
			r.Last = h.Node.Latest.Clone(mem.System)
			r.HasLast = true
			h.Free()
			h.Free()
			return true, 0, false
		}
		if i < len(r.Session.Work) && !r.Inv.JSON && !r.Inv.DryRun && r.Session.Work[i].Selected && r.Session.Work[i].Display {
			writeValue(r.Out, h.Node.Latest)
		} else if i < len(r.Session.Work) && !r.Inv.JSON && !r.Inv.DryRun && r.Session.Work[i].Value && r.Session.Work[i].Display {
			r.Last.Free(mem.System)
			r.Last = h.Node.Latest.Clone(mem.System)
			r.HasLast = true
		}
		status := 0
		if onValue != nil {
			status = onValue(h.Node.Latest)
		}
		h.Free()
		h.Free()
		return true, status, false
	}
	polled := h.Poll()
	if !polled.Done {
		return false, 0, false
	}
	if polled.Result.Exit.Requested {
		if polled.Result.Diagnostic.Code != "" && !r.Inv.JSON {
			r.emit(r.Err, polled.Result.Diagnostic, false)
		}
		status := polled.Result.Exit.Status
		polled.Result.Free(mem.System)
		h.Free()
		return true, status, true
	}
	if polled.Result.Diagnostic.Code != "" && r.Interactive && p.Eval.Terminal && polled.Result.Diagnostic.Cause.HasSignal && polled.Result.Diagnostic.Cause.Signal == int(syscall.SIGINT) {
		// Ctrl-C reached the foreground command directly; like a shell,
		// report only the interrupted status.
		r.terminalNewline()
		polled.Result.Free(mem.System)
		h.Free()
		return true, 130, false
	}
	if polled.Result.Diagnostic.Code != "" {
		if !r.Inv.JSON || r.Inv.Name == "render" {
			r.emit(diagnosticWriter(r.Out, r.Err, r.Inv.JSON), polled.Result.Diagnostic, r.Inv.JSON)
		}
		status := processExitStatus(polled.Result.Diagnostic, r.Inv.Name == "run")
		polled.Result.Free(mem.System)
		h.Free()
		return true, status, !r.Interactive
	}
	if r.Interactive {
		r.display(i, polled.Result.Value)
		r.capture(polled.Result.Value)
	}
	if i < len(r.Session.Work) && r.Session.Work[i].Value && r.Session.Work[i].Display {
		r.Last.Free(mem.System)
		r.Last = polled.Result.Value.Clone(mem.System)
		r.HasLast = true
	}
	status := 0
	if onValue != nil {
		status = onValue(polled.Result.Value)
	}
	polled.Result.Free(mem.System)
	h.Free()
	h.Free()
	return true, status, false
}

// display echoes the value of an interactive statement-leading expression.
func (r *sessionRun) display(i int, value core.Value) {
	// Command results were already shown as output; echo only data values.
	if i < len(r.Session.Work) && r.Session.Work[i].Display && value.Kind != core.Nil && !eval.IsProcessResult(value) && !r.Inv.DryRun {
		writeValue(r.Out, value)
		io.WriteString(r.Out, "\n")
	}
}

func (r *sessionRun) capture(value core.Value) {
	if r.Capture != nil {
		r.Capture.Free(mem.System)
		*r.Capture = value.Clone(mem.System)
	}
}

// interrupt cancels one interactive entry and waits for its own host work to
// settle, leaving work started by earlier entries running. A second interrupt
// while settling escalates to the ordinary forced shutdown: every process
// group is cancelled and the session ends without finalization.
func (r *sessionRun) interrupt(h *program.Handle, before []int64, signal int) (int, bool) {
	p := r.Session.Program
	h.Cancel()
	for {
		tickCLIProgram(p, 10, r.Err, r.Inv.JSON)
		drainEvents(p, r.Out, r.Err, r.Inv.JSON, &r.Progress)
		if forced := posix.TakeSignal(); forced < 0 {
			if p.Host != nil {
				p.Host.CancelAll()
			}
			h.Free()
			r.Forced = true
			r.terminalNewline()
			return 128 - forced, true
		}
		settled := true
		for _, node := range p.PendingNodes() {
			if !slices.Contains(before, node) {
				settled = false
				break
			}
		}
		if settled {
			break
		}
	}
	h.Free()
	posix.ResetSignals()
	r.terminalNewline()
	return 128 + signal, false
}

// terminalNewline ends the line on which a terminal echoed ^C.
func (r *sessionRun) terminalNewline() {
	if r.Session.Program.Eval.Terminal {
		io.WriteString(r.Err, "\n")
	}
}

func finishSessionFinalizers(session *program.Session, h *program.Handle, out io.Writer, errOut io.Writer, json bool, progress *buildProgress) {
	p := session.Program
	for p.Eval.Finalizing() {
		tickCLIProgram(p, 10, errOut, json)
	}
	if json {
		session.Observe(h)
	}
	drainEvents(p, out, errOut, json, progress)
}

func freeRunInputs(fragments []program.Fragment, data [][]byte, names []string) {
	for i := range data {
		mem.FreeSlice(mem.System, data[i])
	}
	for i := range names {
		mem.FreeString(mem.System, names[i])
	}
	mem.FreeSlice(nil, data)
	mem.FreeSlice(nil, names)
	mem.FreeSlice(nil, fragments)
}

func freeRunSources(sources *[]buildSource) {
	for i := range *sources {
		freeBuildSource(&(*sources)[i])
	}
	mem.FreeSlice(nil, *sources)
}

func emitRunDiagnostic(out io.Writer, d diagnostic.Diagnostic, json bool, fragments []program.Fragment, loaded []buildSource) {
	if d.SourceText != "" {
		authored := source.Source{Name: d.Source, Text: d.SourceText}
		emitDiagnostic(out, d, json, &authored)
		return
	}
	for i := range loaded {
		for j := range loaded[i].Files {
			file := loaded[i].Files[j]
			if file.Name != d.Source {
				continue
			}
			authored := source.New(mem.System, file.Name, file.Text)
			emitDiagnostic(out, d, json, authored)
			authored.Free(mem.System)
			return
		}
	}
	for i := range fragments {
		if fragments[i].Name != d.Source {
			continue
		}
		authored := source.New(mem.System, fragments[i].Name, fragments[i].Text)
		emitDiagnostic(out, d, json, authored)
		authored.Free(mem.System)
		return
	}
	emitDiagnostic(out, d, json, nil)
}
