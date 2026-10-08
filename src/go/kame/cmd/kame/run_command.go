package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/source"
	"kame/operations"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
	"solod.dev/so/time"
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
	Out io.Writer
	Err io.Writer
}

func writeExprEffect(value any, effect eval.Effect) {
	output := value.(*exprEffectOutput)
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
			loaded = slices.Append(mem.System, loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
			}
			for j := range file.Parts {
				entries := input.Entries
				if j+1 != len(file.Parts) {
					entries = nil
				}
				fragments = slices.Append(mem.System, fragments, program.Fragment{Name: file.Parts[j].Name, Text: file.Parts[j].Text, Offset: file.Parts[j].Offset, Lang: input.Lang, Entries: entries, Inline: j+1 != len(file.Parts), SkipStatements: len(input.Entries) != 0, Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
			}
			continue
		} else if input.Kind == "stdin" {
			data, err := io.ReadAll(mem.System, in)
			if err != nil {
				freeRunInputs(fragments, storage, names)
				sourceError(diagnosticWriter(out, errOut, inv.JSON), "FS_ERR", "cannot read stdin", inv.JSON)
				return 1
			}
			storage = slices.Append(mem.System, storage, data)
			text, name = string(data), "<stdin>"
		} else {
			var buffer [strconv.MaxIntBase10Len]byte
			name = cloneCommandText("<command:" + strconv.Itoa(buffer[:], i+1) + ">")
			names = slices.Append(mem.System, names, name)
		}
		if input.Lang == "km" || input.Lang == "kmk" {
			file := inlineRunSourceInput(inv, name, text, input.Lang, fragments, diagnosticWriter(out, errOut, inv.JSON))
			loaded = slices.Append(mem.System, loaded, file)
			if file.Status != 0 {
				freeRunInputs(fragments, storage, names)
				return 1
			}
			for j := range file.Parts {
				entries := input.Entries
				if j+1 != len(file.Parts) {
					entries = nil
				}
				fragments = slices.Append(mem.System, fragments, program.Fragment{Name: file.Parts[j].Name, Text: file.Parts[j].Text, Offset: file.Parts[j].Offset, Lang: input.Lang, Entries: entries, Inline: true, SkipStatements: len(input.Entries) != 0, Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
			}
			continue
		}
		fragments = slices.Append(mem.System, fragments, program.Fragment{Name: name, Text: text, Lang: input.Lang, Entries: input.Entries, Inline: input.Kind != "file", Comment: inv.Comment, Defines: inv.Defines, Check: inv.Check})
	}
	defer freeRunInputs(fragments, storage, names)
	directory := inv.Directory
	if !path.IsAbs(directory) {
		buffer := mem.AllocSlice[byte](mem.System, os.MaxPathLen, os.MaxPathLen)
		cwd, err := os.Getwd(buffer)
		if err == nil && cwd != "" {
			directory = path.Join(mem.System, cwd, directory)
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
	compiled := program.CompileSession(mem.System, fragments, registry, program.Options{Host: posix.New(mem.System), Directory: directory, Environment: environment, Defines: defines, Parameters: inv.Parameters, ToolOverrides: inv.ToolOverrides, Shell: inv.Shell, Grants: grants, Jobs: inv.Jobs, CaptureLimit: inv.CaptureLimit, TimeoutMS: inv.TimeoutMS, DryRun: inv.DryRun, Force: inv.Force, CacheDisabled: inv.Force, RetryCount: inv.RetryCount, RetainBytes: inv.RetainBytes, ResolveTool: resolveBuildTool})
	posix.FreeEnvironment(mem.System, environment)
	defer compiled.Free(mem.System)
	if compiled.Session == nil {
		for i := range compiled.Diagnostics {
			emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), compiled.Diagnostics[i], inv.JSON, fragments, loaded)
		}
		return 1
	}
	session := compiled.Session
	defer session.Free()
	p := session.Program
	values := slices.Make[core.Value](mem.System, len(inv.Args))
	for i := range inv.Args {
		values[i] = core.NewString(mem.System, inv.Args[i])
	}
	p.Eval.SetDefinitionArgs(values)
	for i := range values {
		values[i].Free(mem.System)
	}
	slices.Free(mem.System, values)
	// A single rule source keeps the established build presentation, parallel
	// target handling, JSON events, freshness and cache path unchanged.
	output := exprEffectOutput{Out: out, Err: errOut}
	if inv.JSON && inv.Name != "render" {
		session.SetJSON(true)
	} else {
		p.Eval.SetDefinitionEffectSink(writeExprEffect, &output)
	}
	workStart := 0
	startedAt := time.Now().UnixNano()
	if len(inv.Inputs) == 1 && inv.Inputs[0].Lang == "kmk" && inv.TimeoutMS == 0 {
		var targets []string
		for i := range session.Work {
			targets = slices.Append(mem.System, targets, session.Work[i].Target)
		}
		status := materializeTargets(p, targets, out, errOut, inv.JSON)
		slices.Free(mem.System, targets)
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
		if i == len(session.Work) && len(p.Eval.Processes) == 0 {
			break
		}
		started := session.Start(i)
		if started.Diagnostic.Code != "" {
			emitRunDiagnostic(diagnosticWriter(out, errOut, inv.JSON), started.Diagnostic, inv.JSON, fragments, loaded)
			started.Diagnostic.Free(mem.System)
			return 1
		}
		h := started.Handle
		for {
			signal := posix.TakeSignal()
			if signal != 0 {
				h.Cancel()
				tickCLIProgram(p, 10, errOut, inv.JSON)
				h.Free()
				if signal < 0 {
					return 128 - signal
				}
				return 128 + signal
			}
			if inv.TimeoutMS > 0 && (time.Now().UnixNano()-startedAt)/1000000 >= inv.TimeoutMS {
				h.Cancel()
				tickCLIProgram(p, 10, errOut, inv.JSON)
				h.Free()
				sourceError(diagnosticWriter(out, errOut, inv.JSON), "RECIPE_TIMEOUT", "invocation timed out", inv.JSON)
				return 1
			}
			tickCLIProgram(p, 10, errOut, inv.JSON)
			session.Observe(h)
			drainEvents(p, out, errOut, inv.JSON, &progress)
			if h.Definition && h.Node.Current {
				if inv.Name == "render" {
					last.Free(mem.System)
					last = h.Node.Latest.Clone(mem.System)
					hasLast = true
					break
				}
				if i < len(session.Work) && !inv.JSON && !inv.DryRun && session.Work[i].Selected {
					writeValue(out, h.Node.Latest)
				} else if i < len(session.Work) && !inv.JSON && !inv.DryRun && session.Work[i].Value {
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
			if polled.Result.Diagnostic.Code != "" {
				if !inv.JSON {
					emitRunDiagnostic(errOut, polled.Result.Diagnostic, false, fragments, loaded)
				}
				polled.Result.Free(mem.System)
				h.Free()
				return 1
			}
			if i < len(session.Work) && session.Work[i].Value {
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

func freeRunInputs(fragments []program.Fragment, data [][]byte, names []string) {
	for i := range data {
		mem.FreeSlice(mem.System, data[i])
	}
	for i := range names {
		mem.FreeString(mem.System, names[i])
	}
	slices.Free(mem.System, data)
	slices.Free(mem.System, names)
	slices.Free(mem.System, fragments)
}

func freeRunSources(sources *[]buildSource) {
	for i := range *sources {
		freeBuildSource(&(*sources)[i])
	}
	slices.Free(mem.System, *sources)
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
