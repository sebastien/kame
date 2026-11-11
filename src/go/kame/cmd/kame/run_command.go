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

	"strconv"
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
	if effect.Kind == eval.EffectOut || effect.Kind == eval.EffectYield {
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
		name, text := input.Value, input.Value
		if input.Kind == "file" || input.Kind == "discover" {
			file := loadRunSourceInput(inv, input, fragments, diagnosticWriter(out, errOut, inv.JSON))
			loaded = append(loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
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
		if input.Lang == "km" || input.Lang == "kmk" || input.Lang == "kash" {
			file := inlineRunSourceInput(inv, name, text, input.Lang, fragments, diagnosticWriter(out, errOut, inv.JSON))
			loaded = append(loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
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
	grants := inv.Grants
	defaults := []eval.Grant{{Capability: eval.Read, Names: []string{directory}}, {Capability: eval.Write, Names: []string{directory}}, {Capability: eval.Run}}
	if len(grants) == 0 && !inv.NoDefaultGrants {
		grants = defaults
	}
	registry := eval.NewRegistry(mem.System)
	defer registry.Free()
	operations.Register(registry)
	environment := mergeEnvironment(posix.Environment(mem.System), inv.Environment)
	defines := inv.Defines
	if inv.Name == "render" {
		defines = nil
	}
	programOptions := program.Options{Host: posix.New(mem.System), Directory: directory, Environment: environment, Defines: defines, Parameters: inv.Parameters, ToolOverrides: inv.ToolOverrides, Shell: inv.Shell, Grants: grants, Jobs: inv.Jobs, CaptureLimit: inv.CaptureLimit, TimeoutMS: inv.TimeoutMS, DryRun: inv.DryRun, Force: inv.Force, CacheDisabled: inv.Force, RetryCount: inv.RetryCount, RetainBytes: inv.RetainBytes, ResolveTool: resolveBuildTool, PrintResult: inv.PrintResult}
	programOptions.TrackTimings = true
	for i := range inv.Inputs {
		if inv.Name == "run" && inv.Inputs[i].Lang != "kmk" {
			programOptions.HasInput = true
		}
	}
	for i := range inv.Inputs {
		if inv.Inputs[i].Kind == "stdin" {
			programOptions.HasInput = false
		}
	}
	// ponytail: data pipes/files only; interactive TTY input needs foreground
	// process-group handoff rather than the host's isolated cancellation groups.
	if posix.StdinIsTerminal() {
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
		for i := range compiled.Diagnostics {
			emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), compiled.Diagnostics[i], inv.JSON, fragments, loaded)
		}
		return 1
	}
	session := compiled.Session
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
	}
	status := runSessionWork(session, inv, out, errOut, fragments, loaded)
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
	p := session.Program
	workStart := 0
	startedAt := time.Now().UnixNano()
	if len(inv.Inputs) == 1 && inv.Inputs[0].Lang == "kmk" && inv.TimeoutMS == 0 {
		var targets []string
		for i := range session.Work {
			targets = append(targets, session.Work[i].Target)
		}
		status := materializeTargets(p, targets, out, errOut, inv.JSON)
		mem.FreeSlice(nil, targets)
		if status != 0 {
			return status
		}
		workStart = len(session.Work) // Recipes ran already; only owned async joins remain.
	}
	var last core.Value
	hasLast := false
	defer last.Free(mem.System)
	progress := buildProgress{}
	for i := workStart; i <= len(session.Work); i++ {
		if i == len(session.Work) && len(p.Eval.Processes) == 0 && len(session.Deferred.Actions) == 0 {
			break
		}
		started := session.Start(i)
		if started.Diagnostic.Code != "" {
			emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), started.Diagnostic, inv.JSON, fragments, loaded)
			status := processExitStatus(started.Diagnostic, inv.Name == "run")
			started.Diagnostic.Free(mem.System)
			return status
		}
		h := started.Handle
		for {
			signal := posix.TakeSignal()
			if signal != 0 {
				h.Cancel()
				p.Eval.BeginFinalization()
				finishSessionFinalizers(session, h, out, errOut, inv.JSON, &progress)
				if !inv.JSON && len(h.Node.Diagnostic.Notes)+len(h.Node.Diagnostic.Related) != 0 {
					emitRunDiagnostic(errOut, h.Node.Diagnostic, false, fragments, loaded)
				}
				h.Free()
				if signal < 0 {
					return 128 - signal
				}
				return 128 + signal
			}
			if inv.TimeoutMS > 0 && (time.Now().UnixNano()-startedAt)/1000000 >= inv.TimeoutMS {
				p.Eval.BeginFinalizationWith(diagnostic.Diagnostic{Code: "RECIPE_TIMEOUT", Severity: diagnostic.Error, Message: "invocation timed out"})
				h.Cancel()
				finishSessionFinalizers(session, h, out, errOut, inv.JSON, &progress)
				if !inv.JSON && h.Node.Diagnostic.Code == "RECIPE_TIMEOUT" {
					emitRunDiagnostic(errOut, h.Node.Diagnostic, false, fragments, loaded)
				} else if !inv.JSON || h.Node.Diagnostic.Code != "RECIPE_TIMEOUT" {
					sourceError(diagnosticWriter(out, errOut, inv.JSON), "RECIPE_TIMEOUT", "invocation timed out", inv.JSON)
				}
				h.Free()
				return 1
			}
			tickCLIProgram(p, 10, errOut, inv.JSON)
			if inv.JSON {
				session.Observe(h)
			}
			drainEvents(p, out, errOut, inv.JSON, &progress)
			if signal := nativeOutputSignal(); signal != 0 {
				p.Eval.BeginFinalization()
				h.Cancel()
				finishSessionFinalizers(session, h, out, errOut, inv.JSON, &progress)
				if !inv.JSON && len(h.Node.Diagnostic.Notes)+len(h.Node.Diagnostic.Related) != 0 {
					emitRunDiagnostic(errOut, h.Node.Diagnostic, false, fragments, loaded)
				}
				h.Free()
				if signal < 0 {
					return 128 - signal
				}
				return 128 + signal
			}
			if code := nativeOutputFailure(); code != "" && h.Node.Diagnostic.Code == "" {
				p.Eval.BeginFinalizationWith(diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: "cleanup output delivery failed"})
				h.Cancel()
				finishSessionFinalizers(session, h, out, errOut, inv.JSON, &progress)
				if h.Node.Diagnostic.Code != "" {
					if !inv.JSON {
						emitRunDiagnostic(errOut, h.Node.Diagnostic, false, fragments, loaded)
					}
				} else {
					sourceError(diagnosticWriter(out, errOut, inv.JSON), code, "cleanup output delivery failed", inv.JSON)
				}
				h.Free()
				return 1
			}
			if h.Definition && h.Node.Current {
				if inv.Name == "render" {
					last.Free(mem.System)
					last = h.Node.Latest.Clone(mem.System)
					hasLast = true
					break
				}
				if i < len(session.Work) && !inv.JSON && !inv.DryRun && session.Work[i].Selected && session.Work[i].Display {
					writeValue(out, h.Node.Latest)
				} else if i < len(session.Work) && !inv.JSON && !inv.DryRun && session.Work[i].Value && session.Work[i].Display {
					last.Free(mem.System)
					last = h.Node.Latest.Clone(mem.System)
					hasLast = true
				}
				break
			}
			polled := h.Poll()
			if !polled.Done {
				continue
			}
			if polled.Result.Exit.Requested {
				if polled.Result.Diagnostic.Code != "" && !inv.JSON {
					emitRunDiagnostic(errOut, polled.Result.Diagnostic, false, fragments, loaded)
				}
				status := polled.Result.Exit.Status
				polled.Result.Free(mem.System)
				h.Free()
				return status
			}
			if polled.Result.Diagnostic.Code != "" {
				if !inv.JSON || inv.Name == "render" {
					emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), polled.Result.Diagnostic, inv.JSON, fragments, loaded)
				}
				status := processExitStatus(polled.Result.Diagnostic, inv.Name == "run")
				polled.Result.Free(mem.System)
				h.Free()
				return status
			}
			if i < len(session.Work) && session.Work[i].Value && session.Work[i].Display {
				last.Free(mem.System)
				last = polled.Result.Value.Clone(mem.System)
				hasLast = true
			}
			polled.Result.Free(mem.System)
			break
		}
		h.Free()
	}
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
