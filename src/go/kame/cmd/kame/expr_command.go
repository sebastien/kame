package main

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

type exprArguments struct { Text string; File string; Directory string; Args []string; Grants []eval.Grant; OK bool }

func runExpr(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	parsed := parseExprArguments(args, errOut)
	if !parsed.OK { return 2 }
	text := parsed.Text
	var data []byte
	if text == "" && parsed.File != "" { readData, readErr := os.ReadFile(mem.System, parsed.File); if readErr != nil { cliError(errOut, "FS_ERR", "cannot read expression: "+parsed.File); return 1 }; data, text = readData, string(readData) }
	if text == "" && parsed.File == "" { readData, readErr := io.ReadAll(mem.System, in); if readErr != nil { cliError(errOut, "FS_ERR", "cannot read stdin"); return 1 }; data, text = readData, string(readData) }
	defer freeCommandBytes(data)
	// nop is the identity operation. Wrapping the text keeps atoms such as
	// :nil or 0x10 expressions instead of definition right-hand-side string
	// templates, without changing the meaning of any expression form.
	options := buildArguments{Command: "result = (nop " + text + ")", Directory: parsed.Directory, Jobs: 1, Grants: parsed.Grants, NoDefaultGrants: true, OK: true}
	session := openBuildSession(options, errOut, true)
	if session.Status != 0 { return session.Status }
	defer session.Free()
	// The args frame must exist even when empty, so selectors yield empty
	// results instead of SEL_NO_CONTEXT.
	values := mem.AllocSlice[core.Value](mem.System, len(parsed.Args), len(parsed.Args)+1)
	for i := range parsed.Args { values[i] = core.NewString(mem.System, parsed.Args[i]) }
	session.Program.Eval.SetDefinitionArgs(values)
	for i := range values { values[i].Free(mem.System) }
	slices.Free(mem.System, values)
	started := session.Program.Start("result")
	if started.Diagnostic.Code != "" { emitDiagnostic(errOut, started.Diagnostic, false, session.Parsed.Source); started.Diagnostic.Free(mem.System); return 1 }
	handle := started.Handle
	defer handle.Free()
	for {
		session.Program.Tick(10)
		discardEvents(session.Program)
		if handle.Definition && handle.Node.Current { writeValue(out, handle.Node.Latest); return 0 }
		result := handle.Poll()
		if !result.Done { continue }
		if result.Result.Diagnostic.Code != "" { emitDiagnostic(errOut, result.Result.Diagnostic, false, session.Parsed.Source); result.Result.Free(mem.System); return 1 }
		writeValue(out, result.Result.Value); result.Result.Free(mem.System); return 0
	}
}

func freeCommandBytes(data []byte) { if len(data) != 0 { mem.FreeSlice(mem.System, data) } }

func parseExprArguments(args []string, errOut io.Writer) exprArguments {
	result := exprArguments{Directory: "."}
	positional := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" { positional = true; continue }
		if positional { result.Args = slices.Append(mem.System, result.Args, arg); continue }
		if arg == "-C" || arg == "--directory" { if i+1 == len(args) { cliError(errOut, "OPT_NO_VALUE", "missing value for "+arg); return exprArguments{} }; i++; result.Directory = args[i]; continue }
		if len(arg) > 12 && arg[:12] == "--directory=" { result.Directory = arg[12:]; continue }
		if !positional && arg == "-c" { if i+1 == len(args) { cliError(errOut, "OPT_NO_VALUE", "missing value for -c"); return exprArguments{} }; i++; result.Text = args[i]; continue }
		if !positional && (arg == "--allow-read" || arg == "--allow-write" || arg == "--allow-env" || arg == "--allow-run") { result.Grants = appendExprGrant(result.Grants, arg, ""); continue }
		if !positional && len(arg) >= 13 && arg[:13] == "--allow-read=" { if len(arg) == 13 { cliError(errOut, "OPT_VALUE_INVALID", "empty value for --allow-read"); return exprArguments{} }; result.Grants = appendExprGrant(result.Grants, "--allow-read", arg[13:]); continue }
		if !positional && len(arg) >= 14 && arg[:14] == "--allow-write=" { if len(arg) == 14 { cliError(errOut, "OPT_VALUE_INVALID", "empty value for --allow-write"); return exprArguments{} }; result.Grants = appendExprGrant(result.Grants, "--allow-write", arg[14:]); continue }
		if !positional && len(arg) >= 12 && arg[:12] == "--allow-env=" { if len(arg) == 12 { cliError(errOut, "OPT_VALUE_INVALID", "empty value for --allow-env"); return exprArguments{} }; result.Grants = appendExprGrant(result.Grants, "--allow-env", arg[12:]); continue }
		if !positional && len(arg) != 0 && arg[0] == '-' { cliError(errOut, "OPT_UNKNOWN", "unknown option: "+arg); return exprArguments{} }
		if !positional && result.File == "" { result.File = arg; continue }
	}
	if result.Text != "" && result.File != "" { cliError(errOut, "OPT_CONFLICT", "-c and expression file cannot be used together"); return exprArguments{} }
	result.OK = true
	return result
}

func appendExprGrant(grants []eval.Grant, option string, name string) []eval.Grant {
	capability := eval.Read
	if option == "--allow-write" { capability = eval.Write }
	if option == "--allow-run" { capability = eval.Run }
	if option == "--allow-env" { capability = eval.Env }
	grant := eval.Grant{Capability: capability}
	// Allocate the name list: a slice literal would not outlive this function.
	if name != "" { grant.Names = slices.Append(mem.System, grant.Names, cloneCommandText(name)) }
	return slices.Append(mem.System, grants, grant)
}
