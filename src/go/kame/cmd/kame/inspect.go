package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/encoding/json"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
)

func runPlan(args []string, out io.Writer, errOut io.Writer) int {
	parsed := parseBuildArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	session := openBuildSession(parsed, errOut, true)
	defer session.Free()
	if session.Status != 0 {
		return session.Status
	}
	targets := selectTargets(session.Program, parsed.Targets)
	parsed.Targets = nil
	defer program.FreeStrings(mem.System, targets)
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	failed := false
	for i := range targets {
		result := session.Program.Plan(targets[i])
		if result.Diagnostic.Code != "" {
			annotateTargetDiagnostic(&result.Diagnostic, targets[i])
			emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Diagnostic, parsed.JSON, session.Parsed.Source)
			result.Diagnostic.Free(mem.System)
			failed = true
			continue
		}
		if parsed.JSON {
			writePlan(out, result.Plan)
		} else {
			var buffer = bytes.NewBuffer(mem.System, nil)
			writePlan(&buffer, result.Plan)
			cli.WriteReport(out, "plan "+result.Plan.Target, buffer.String(), stdoutColor)
			if result.Plan.Rule != nil && session.Program.Parsed.Source != nil {
				src := session.Program.Parsed.Source
				location := session.Program.Eval.LocateSource(src.Name, source.Span{Start: result.Plan.RuleSpan.Start, End: result.Plan.RuleSpan.End})
				authored, loaded := diagnosticSource(location.Source, src)
				if authored != nil {
					position := authored.Position(location.Span.Start)
					fmt.Fprintf(out, "  source: %s:%d:%d\n", location.Source, position.Line, position.Column)
				} else {
					io.WriteString(out, "  source: "+location.Source+"\n")
				}
				if loaded {
					authored.Free(mem.System)
				}
				start, end := result.Plan.RuleSpan.Start, result.Plan.RuleSpan.End
				if start >= 0 && end <= len(src.Text) && end >= start {
					for j := start; j < end; j++ {
						if src.Text[j] == '\n' {
							end = j
							break
						}
					}
					io.WriteString(out, "  rule: "+src.Text[start:end]+"\n")
				}
			}
			buffer.Free()
		}
		result.Plan.Free(mem.System)
	}
	if failed {
		return 1
	}
	return 0
}

func runTools(args []string, out io.Writer, errOut io.Writer) int {
	check := len(args) != 0 && args[0] == "check"
	if check {
		args = args[1:]
	}
	parsed := parseBuildArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	if !check && len(parsed.Targets) != 0 {
		cliError(errOut, "OPT_VALUE_INVALID", "tools does not accept targets")
		return 2
	}
	if check && len(parsed.Targets) == 0 {
		cliError(errOut, "OPT_VALUE_INVALID", "tools check requires at least one target")
		return 2
	}
	session := openBuildSessionForTools(parsed, errOut, true, !check)
	defer session.Free()
	if session.Status != 0 {
		return session.Status
	}
	if check {
		targets := selectTargets(session.Program, parsed.Targets)
		parsed.Targets = nil
		status := checkTargetTools(session.Program, parsed.JSON, targets, out, errOut)
		program.FreeStrings(mem.System, targets)
		return status
	}
	var buffer = bytes.NewBuffer(mem.System, nil)
	defer buffer.Free()
	e := json.NewEncoder(&buffer)
	e.BeginArray()
	for i := range session.Program.Tools {
		e.BeginObject()
		e.Str("name")
		e.Str(session.Program.Tools[i].Name)
		e.Str("path")
		e.Str(session.Program.Tools[i].Path)
		e.EndObject()
	}
	e.EndArray()
	e.Flush()
	if parsed.JSON {
		io.WriteString(out, buffer.String())
		io.WriteString(out, "\n")
	} else {
		cli.WriteReport(out, "tools", buffer.String(), stdoutColor)
	}
	return 0
}

func checkTargetTools(p *program.Program, machine bool, targets []string, out io.Writer, errOut io.Writer) int {
	failed := false
	for i := range targets {
		result := p.RequiredTools(targets[i])
		available := true
		if result.Diagnostic.Code != "" {
			emitDiagnostic(diagnosticWriter(out, errOut, machine), result.Diagnostic, machine, p.Parsed.Source)
			failed = true
		} else {
			for j := range result.Uses {
				use := result.Uses[j]
				if p.ResolveTool(use.Name) != "" {
					continue
				}
				d := diagnostic.Diagnostic{Code: "TOOL_MISSING", Severity: diagnostic.Error, Message: "required tool not found or not executable: " + use.Name, Source: use.Source, Span: use.Span, Target: use.Target, TargetStack: use.TargetStack, Tips: []string{"install the tool or add its executable directory to PATH"}}
				emitDiagnostic(diagnosticWriter(out, errOut, machine), d, machine, p.Parsed.Source)
				failed = true
				available = false
			}
		}
		if machine {
			p.WriteToolsCheckResult(out, targets[i], result.Uses)
		} else if available && result.Diagnostic.Code == "" {
			cli.Style(errOut, "status.success", "done ", diagnosticColor == "always")
			io.WriteString(errOut, "tools check "+targets[i]+"\n")
		}
		result.Free(mem.System)
	}
	if failed {
		return 1
	}
	return 0
}

func writePlan(out io.Writer, plan program.Plan) {
	program.WritePlan(out, &plan)
}

func runCat(args []string, out io.Writer, errOut io.Writer) int {
	parsed := parseBuildArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	session := openBuildSession(parsed, errOut, true)
	defer session.Free()
	if session.Status != 0 {
		return session.Status
	}
	targets := selectTargets(session.Program, parsed.Targets)
	parsed.Targets = nil
	defer program.FreeStrings(mem.System, targets)
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	if len(targets) != 1 {
		cliError(errOut, "OPT_VALUE_INVALID", "cat requires exactly one target")
		return 2
	}
	started := session.Program.Start(targets[0])
	if started.Diagnostic.Code != "" {
		// Materializing an existing file target needs no rule (006): cat prints
		// its exact bytes instead of reporting TGT_NO_RULE.
		if started.Diagnostic.Code == "TGT_NO_RULE" {
			name := targets[0]
			if !path.IsAbs(name) {
				name = path.Join(mem.System, parsed.Directory, name)
			}
			data, readErr := os.ReadFile(mem.System, name)
			if name != targets[0] {
				mem.FreeString(mem.System, name)
			}
			if readErr == nil {
				writeArtifact(out, targets[0], data)
				mem.FreeSlice(mem.System, data)
				started.Diagnostic.Free(mem.System)
				return 0
			}
		}
		annotateTargetDiagnostic(&started.Diagnostic, targets[0])
		emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), started.Diagnostic, parsed.JSON, session.Parsed.Source)
		started.Diagnostic.Free(mem.System)
		return 1
	}
	handle := started.Handle
	defer handle.Free()
	for {
		session.Program.Tick(10)
		discardEvents(session.Program)
		if handle.Definition && handle.Node.Current {
			if parsed.JSON {
				program.WriteJSONEvent(out, program.Event{Kind: program.TargetValue, Target: targets[0], NodeID: handle.Node.ID, Generation: handle.Node.Generation, Attempt: handle.Node.Attempt, Key: handle.Node.Key, Value: handle.Node.Latest})
			} else {
				writeValue(out, handle.Node.Latest)
			}
			return 0
		}
		result := handle.Poll()
		if !result.Done {
			continue
		}
		if result.Result.Diagnostic.Code != "" {
			emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Result.Diagnostic, parsed.JSON, session.Parsed.Source)
			result.Result.Free(mem.System)
			return 1
		}
		if result.Result.Path == "" {
			result.Result.Free(mem.System)
			cliError(errOut, "NO_ARTIFACT", "target has no readable artifact")
			return 1
		}
		name := result.Result.Path
		if !path.IsAbs(name) {
			name = path.Join(mem.System, parsed.Directory, name)
		}
		data, readErr := os.ReadFile(mem.System, name)
		if name != result.Result.Path {
			mem.FreeString(mem.System, name)
		}
		result.Result.Free(mem.System)
		if readErr != nil {
			cliError(errOut, "FS_ERR", "cannot read artifact")
			return 1
		}
		writeArtifact(out, targets[0], data)
		mem.FreeSlice(mem.System, data)
		return 0
	}
}

type graphArguments struct {
	Build  buildArguments
	Depth  int
	Expand bool
	OK     bool
}

func (arguments *graphArguments) Free() { arguments.Build.Free(); *arguments = graphArguments{} }

func runGraph(args []string, out io.Writer, errOut io.Writer, kind string) int {
	graph := parseGraphArguments(args, errOut, kind == "span")
	defer graph.Free()
	if !graph.OK {
		return 2
	}
	session := openBuildSession(graph.Build, errOut, true)
	defer session.Free()
	if session.Status != 0 {
		return session.Status
	}
	targets := selectTargets(session.Program, graph.Build.Targets)
	graph.Build.Targets = nil
	defer program.FreeStrings(mem.System, targets)
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, graph.Build.JSON)
	}
	if len(targets) != 1 {
		cliError(errOut, "OPT_VALUE_INVALID", kind+" requires exactly one target")
		return 2
	}
	var graphDiagnostic diagnostic.Diagnostic
	var buffer = bytes.NewBuffer(mem.System, nil)
	defer buffer.Free()
	if kind == "inputs" || kind == "outputs" {
		graphDiagnostic = session.Program.WriteGraph(&buffer, targets[0], graph.Depth, kind)
	} else {
		graphDiagnostic = session.Program.WriteSpan(&buffer, targets[0], graph.Depth, graph.Expand)
	}
	if graphDiagnostic.Code != "" {
		annotateTargetDiagnostic(&graphDiagnostic, targets[0])
		emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), graphDiagnostic, graph.Build.JSON, session.Parsed.Source)
		graphDiagnostic.Free(mem.System)
		return 1
	}
	if graph.Build.JSON {
		io.WriteString(out, buffer.String())
	} else {
		var heading = bytes.NewBuffer(mem.System, nil)
		fmt.Fprintf(&heading, "%s %s · depth %d", kind, targets[0], graph.Depth)
		cli.WriteReport(out, heading.String(), buffer.String(), stdoutColor)
		heading.Free()
	}
	return 0
}

func writeArtifact(out io.Writer, target string, data []byte) {
	if !cliDiagnosticJSON {
		out.Write(data)
		return
	}
	if len(data) == 0 {
		program.WriteDataResult(out, "artifact", "", target, "", false, nil, true)
		return
	}
	for start := 0; start < len(data); start += 32768 {
		end := start + 32768
		if end > len(data) {
			end = len(data)
		}
		program.WriteDataResult(out, "artifact", "", target, "", false, data[start:end], true)
	}
}

// Field borrows a JSON record field; it must not be freed independently.
func documentField(value core.Value, name string) core.Value {
	for i := range value.Record {
		if value.Record[i].Key == name {
			return value.Record[i].Value
		}
	}
	return core.Value{}
}

func parseGraphArguments(args []string, errOut io.Writer, allowExpand bool) graphArguments {
	command := "inputs"
	if allowExpand {
		command = "span"
	}
	inv := cli.Parse(command, args)
	applyInvocationPresentation(&inv)
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		inv.Free()
		return graphArguments{}
	}
	return graphArguments{Build: inv, Depth: inv.Depth, Expand: inv.Expand, OK: inv.OK}
}

func discardEvents(p *program.Program) {
	for {
		next := p.NextEvent()
		if !next.OK {
			return
		}
		next.Event.Free(mem.System)
	}
}
