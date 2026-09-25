package main

import (
	"littlemake/diagnostic"
	"littlemake/program"
	"solod.dev/so/encoding/json"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
)

func runPlan(args []string, out io.Writer, errOut io.Writer) int {
	parsed := parseBuildArguments(args, errOut)
	if !parsed.OK { return 2 }
	if len(parsed.Targets) == 0 { cliError(errOut, "OPT_NO_VALUE", "plan requires at least one target"); return 2 }
	session := openBuildSession(parsed, errOut, true)
	if session.Status != 0 { return session.Status }
	defer session.Free()
	failed := false
	for i := range parsed.Targets {
		result := session.Program.Plan(parsed.Targets[i])
		if result.Diagnostic.Code != "" { annotateTargetDiagnostic(&result.Diagnostic, parsed.Targets[i]); emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Diagnostic, parsed.JSON); result.Diagnostic.Free(mem.System); failed = true; continue }
		writePlan(out, result.Plan)
		result.Plan.Free(mem.System)
	}
	if failed { return 1 }
	return 0
}

func writePlan(out io.Writer, plan program.Plan) {
	e := json.NewEncoder(out)
	e.BeginObject(); e.Str("schema"); e.Int(1); e.Str("type"); e.Str("plan"); e.Str("target"); e.Str(plan.Target)
	e.Str("inputs"); stringArray(&e, plan.Inputs)
	e.Str("outputs"); stringArray(&e, plan.Outputs)
	e.Str("captures"); e.BeginArray(); for i := range plan.Captures { e.BeginObject(); e.Str("name"); e.Str(plan.Captures[i].Name); e.Str("value"); e.Str(plan.Captures[i].Text); e.EndObject() }; e.EndArray()
	e.Str("freshness"); if plan.Freshness == program.Fresh { e.Str("fresh") } else if plan.Freshness == program.Stale { e.Str("stale") } else { e.Str("unknown") }
	if plan.Rule != nil { e.Str("rule"); e.BeginObject(); e.Str("start"); e.Int(int64(plan.RuleSpan.Start)); e.Str("end"); e.Int(int64(plan.RuleSpan.End)); e.EndObject() }
	e.EndObject(); e.Flush(); io.WriteString(out, "\n")
}

func stringArray(e *json.Encoder, values []string) { e.BeginArray(); for i := range values { e.Str(values[i]) }; e.EndArray() }

func runCat(args []string, out io.Writer, errOut io.Writer) int {
	parsed := parseBuildArguments(args, errOut)
	if !parsed.OK { return 2 }
	if len(parsed.Targets) != 1 { cliError(errOut, "OPT_VALUE_INVALID", "cat requires exactly one target"); return 2 }
	session := openBuildSession(parsed, errOut, true)
	if session.Status != 0 { return session.Status }
	defer session.Free()
	started := session.Program.Start(parsed.Targets[0])
	if started.Diagnostic.Code != "" {
		// Materializing an existing file target needs no rule (006): cat prints
		// its exact bytes instead of reporting TGT_NO_RULE.
		if started.Diagnostic.Code == "TGT_NO_RULE" {
			name := parsed.Targets[0]
			if !path.IsAbs(name) { name = path.Join(mem.System, parsed.Directory, name) }
			data, readErr := os.ReadFile(mem.System, name)
			if name != parsed.Targets[0] { mem.FreeString(mem.System, name) }
			if readErr == nil {
				out.Write(data)
				if len(data) != 0 { mem.FreeSlice(mem.System, data) }
				started.Diagnostic.Free(mem.System)
				return 0
			}
		}
		annotateTargetDiagnostic(&started.Diagnostic, parsed.Targets[0])
		emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), started.Diagnostic, parsed.JSON); started.Diagnostic.Free(mem.System); return 1
	}
	handle := started.Handle
	defer handle.Free()
	for {
		session.Program.Tick(10)
		discardEvents(session.Program)
		if handle.Definition && handle.Node.Current { writeValue(out, handle.Node.Latest); return 0 }
		result := handle.Poll()
		if !result.Done { continue }
		if result.Result.Diagnostic.Code != "" { emitDiagnostic(diagnosticWriter(out, errOut, parsed.JSON), result.Result.Diagnostic, parsed.JSON); result.Result.Free(mem.System); return 1 }
		if result.Result.Path == "" { result.Result.Free(mem.System); cliError(errOut, "NO_ARTIFACT", "target has no readable artifact"); return 1 }
		name := result.Result.Path
		if !path.IsAbs(name) { name = path.Join(mem.System, parsed.Directory, name) }
		data, readErr := os.ReadFile(mem.System, name)
		if name != result.Result.Path { mem.FreeString(mem.System, name) }
		result.Result.Free(mem.System)
		if readErr != nil { cliError(errOut, "FS_ERR", "cannot read artifact"); return 1 }
		out.Write(data); if len(data) != 0 { mem.FreeSlice(mem.System, data) }
		return 0
	}
}

type graphArguments struct { Build buildArguments; Depth int; Expand bool; OK bool }

func runGraph(args []string, out io.Writer, errOut io.Writer, kind string) int {
	graph := parseGraphArguments(args, errOut, kind == "span")
	if !graph.OK { return 2 }
	if len(graph.Build.Targets) != 1 { cliError(errOut, "OPT_VALUE_INVALID", kind+" requires exactly one target"); return 2 }
	session := openBuildSession(graph.Build, errOut, true)
	if session.Status != 0 { return session.Status }
	defer session.Free()
	e := json.NewEncoder(out)
	if kind == "inputs" || kind == "outputs" {
		traversal := graphValues(session.Program, graph.Build.Targets[0], graph.Depth, kind)
		if traversal.Diagnostic.Code != "" { annotateTargetDiagnostic(&traversal.Diagnostic, graph.Build.Targets[0]); emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), traversal.Diagnostic, graph.Build.JSON); traversal.Diagnostic.Free(mem.System); return 1 }
		e.BeginArray()
		for i := range traversal.Values { e.Str(traversal.Values[i]) }
		e.EndArray(); e.Flush(); io.WriteString(out, "\n"); program.FreeStrings(mem.System, traversal.Values)
		return 0
	}
	planResult := session.Program.Plan(graph.Build.Targets[0])
	if planResult.Diagnostic.Code != "" { annotateTargetDiagnostic(&planResult.Diagnostic, graph.Build.Targets[0]); emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), planResult.Diagnostic, graph.Build.JSON); planResult.Diagnostic.Free(mem.System); return 1 }
	defer planResult.Plan.Free(mem.System)
	e.BeginObject(); e.Str("schema"); e.Int(1); e.Str("type"); e.Str("span")
	e.Str("static"); e.BeginObject()
	if graph.Depth == 1 {
		e.Str("inputs"); stringArray(&e, planResult.Plan.Inputs); e.Str("outputs"); stringArray(&e, planResult.Plan.Outputs)
	} else {
		inTraversal := graphValues(session.Program, graph.Build.Targets[0], graph.Depth, "inputs")
		if inTraversal.Diagnostic.Code != "" { annotateTargetDiagnostic(&inTraversal.Diagnostic, graph.Build.Targets[0]); emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), inTraversal.Diagnostic, graph.Build.JSON); inTraversal.Diagnostic.Free(mem.System); return 1 }
		outTraversal := graphValues(session.Program, graph.Build.Targets[0], graph.Depth, "outputs")
		if outTraversal.Diagnostic.Code != "" { program.FreeStrings(mem.System, inTraversal.Values); annotateTargetDiagnostic(&outTraversal.Diagnostic, graph.Build.Targets[0]); emitDiagnostic(diagnosticWriter(out, errOut, graph.Build.JSON), outTraversal.Diagnostic, graph.Build.JSON); outTraversal.Diagnostic.Free(mem.System); return 1 }
		e.Str("inputs"); stringArray(&e, inTraversal.Values); e.Str("outputs"); stringArray(&e, outTraversal.Values)
		program.FreeStrings(mem.System, inTraversal.Values)
		program.FreeStrings(mem.System, outTraversal.Values)
	}
	e.EndObject()
	e.Str("dynamic"); e.BeginArray(); e.EndArray()
	e.Str("expanded"); e.Bool(graph.Expand)
	e.EndObject(); e.Flush(); io.WriteString(out, "\n")
	return 0
}

type graphNode struct { Target string; Depth int }
type graphResult struct { Values []string; Diagnostic diagnostic.Diagnostic }

// graphValues walks dependency plans breadth-first. Each discovered resource is
// emitted once in discovery order, which keeps graph output stable and makes
// cycles finite. A depth of one includes only the selected target's edges.
func graphValues(p *program.Program, target string, depth int, kind string) graphResult {
	if depth == 0 { return graphResult{} }
	var pending []graphNode
	pending = slices.Append(mem.System, pending, graphNode{Target: cloneCommandText(target)})
	var visited []string
	var values []string
	for cursor := 0; cursor < len(pending); cursor++ {
		node := pending[cursor]
		if graphContains(visited, node.Target) { mem.FreeString(mem.System, node.Target); continue }
		visited = slices.Append(mem.System, visited, node.Target)
		result := p.Plan(node.Target)
		if result.Diagnostic.Code != "" {
			if node.Depth == 0 {
				program.FreeStrings(mem.System, values)
				program.FreeStrings(mem.System, visited)
				slices.Free(mem.System, pending)
				return graphResult{Diagnostic: result.Diagnostic}
			}
			result.Diagnostic.Free(mem.System)
			continue // An external input is a graph leaf rather than an error.
		}
		plan := result.Plan
		if kind == "inputs" {
			for i := range plan.Inputs { graphAppend(&values, plan.Inputs[i]) }
		} else {
			for i := range plan.Outputs { graphAppend(&values, plan.Outputs[i]) }
		}
		if depth == -1 || node.Depth+1 < depth {
			for i := range plan.Inputs {
				if !p.HasTarget(plan.Inputs[i]) || graphContains(visited, plan.Inputs[i]) { continue }
				pending = slices.Append(mem.System, pending, graphNode{Target: cloneCommandText(plan.Inputs[i]), Depth: node.Depth + 1})
			}
		}
		plan.Free(mem.System)
	}
	if len(pending) != 0 { slices.Free(mem.System, pending) }
	program.FreeStrings(mem.System, visited)
	return graphResult{Values: values}
}

func graphContains(values []string, value string) bool { for i := range values { if values[i] == value { return true } }; return false }

func graphAppend(values *[]string, value string) {
	if graphContains(*values, value) { return }
	*values = slices.Append(mem.System, *values, cloneCommandText(value))
}

func parseGraphArguments(args []string, errOut io.Writer, allowExpand bool) graphArguments {
	result := graphArguments{Depth: 1}
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--expand" {
			if !allowExpand { cliError(errOut, "OPT_UNKNOWN", "unknown option: --expand"); return graphArguments{} }
			result.Expand = true; continue
		}
		if arg == "--depth" {
			if i+1 == len(args) { cliError(errOut, "OPT_NO_VALUE", "missing value for --depth"); return graphArguments{} }
			i++; depth, convertErr := strconv.Atoi(args[i]); if convertErr != nil || depth < -1 { cliError(errOut, "OPT_VALUE_INVALID", "depth must be -1 or a nonnegative integer"); return graphArguments{} }; result.Depth = depth; continue
		}
		if len(arg) > 8 && arg[:8] == "--depth=" { depth, convertErr := strconv.Atoi(arg[8:]); if convertErr != nil || depth < -1 { cliError(errOut, "OPT_VALUE_INVALID", "depth must be -1 or a nonnegative integer"); return graphArguments{} }; result.Depth = depth; continue }
		remaining = slices.Append(mem.System, remaining, arg)
	}
	result.Build = parseBuildArguments(remaining, errOut)
	if len(remaining) != 0 { slices.Free(mem.System, remaining) }
	if !result.Build.OK { return graphArguments{} }
	result.OK = true
	return result
}

func discardEvents(p *program.Program) { for { next := p.NextEvent(); if !next.OK { return }; next.Event.Free(mem.System) } }
