package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/program"
	"solod.dev/so/encoding/json"
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
	parsed.Targets = targets
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	failed := false
	for i := range parsed.Targets {
		result := session.Program.Plan(parsed.Targets[i])
		if result.Diagnostic.Code != "" {
			annotateTargetDiagnostic(&result.Diagnostic, parsed.Targets[i])
			emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Diagnostic, parsed.JSON, session.Parsed.Source)
			result.Diagnostic.Free(mem.System)
			failed = true
			continue
		}
		writePlan(out, result.Plan)
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
		parsed.Targets = selectTargets(session.Program, parsed.Targets)
		return checkTargetTools(session.Program, parsed, out, errOut)
	}
	e := json.NewEncoder(out)
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
	io.WriteString(out, "\n")
	return 0
}

func checkTargetTools(p *program.Program, parsed buildArguments, out io.Writer, errOut io.Writer) int {
	failed := false
	for i := range parsed.Targets {
		result := p.RequiredTools(parsed.Targets[i])
		if result.Diagnostic.Code != "" {
			emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Diagnostic, parsed.JSON, p.Parsed.Source)
			failed = true
		} else {
			for j := range result.Uses {
				use := result.Uses[j]
				if p.ResolveTool(use.Name) != "" {
					continue
				}
				d := diagnostic.Diagnostic{Code: "TOOL_MISSING", Severity: diagnostic.Error, Message: "required tool not found or not executable: " + use.Name, Source: use.Source, Span: use.Span, Target: use.Target, TargetStack: use.TargetStack, Tips: []string{"install the tool or add its executable directory to PATH"}}
				emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), d, parsed.JSON, p.Parsed.Source)
				failed = true
			}
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
	parsed.Targets = targets
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, parsed.JSON)
	}
	if len(parsed.Targets) != 1 {
		cliError(errOut, "OPT_VALUE_INVALID", "cat requires exactly one target")
		return 2
	}
	started := session.Program.Start(parsed.Targets[0])
	if started.Diagnostic.Code != "" {
		// Materializing an existing file target needs no rule (006): cat prints
		// its exact bytes instead of reporting TGT_NO_RULE.
		if started.Diagnostic.Code == "TGT_NO_RULE" {
			name := parsed.Targets[0]
			if !path.IsAbs(name) {
				name = path.Join(mem.System, parsed.Directory, name)
			}
			data, readErr := os.ReadFile(mem.System, name)
			if name != parsed.Targets[0] {
				mem.FreeString(mem.System, name)
			}
			if readErr == nil {
				out.Write(data)
				if len(data) != 0 {
					mem.FreeSlice(mem.System, data)
				}
				started.Diagnostic.Free(mem.System)
				return 0
			}
		}
		annotateTargetDiagnostic(&started.Diagnostic, parsed.Targets[0])
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
			writeValue(out, handle.Node.Latest)
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
		out.Write(data)
		if len(data) != 0 {
			mem.FreeSlice(mem.System, data)
		}
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
	graph.Build.Targets = targets
	if len(targets) == 0 {
		return reportNoDefault(session.Program, out, errOut, graph.Build.JSON)
	}
	if len(graph.Build.Targets) != 1 {
		cliError(errOut, "OPT_VALUE_INVALID", kind+" requires exactly one target")
		return 2
	}
	var graphDiagnostic diagnostic.Diagnostic
	if kind == "inputs" || kind == "outputs" {
		graphDiagnostic = session.Program.WriteGraph(out, graph.Build.Targets[0], graph.Depth, kind)
	} else {
		graphDiagnostic = session.Program.WriteSpan(out, graph.Build.Targets[0], graph.Depth, graph.Expand)
	}
	if graphDiagnostic.Code != "" {
		annotateTargetDiagnostic(&graphDiagnostic, graph.Build.Targets[0])
		emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), graphDiagnostic, graph.Build.JSON, session.Parsed.Source)
		graphDiagnostic.Free(mem.System)
		return 1
	}
	return 0
}

func parseGraphArguments(args []string, errOut io.Writer, allowExpand bool) graphArguments {
	command := "inputs"
	if allowExpand {
		command = "span"
	}
	inv := cli.Parse(command, args)
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
