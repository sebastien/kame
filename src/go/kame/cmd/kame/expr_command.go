package main

import (
	"kame/cli"
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

type exprArguments struct { Text string; File string; Directory string; Args []string; Grants []eval.Grant; OK bool }
type exprEffectOutput struct { Out io.Writer; Err io.Writer }

func writeExprEffect(value any, effect eval.Effect) {
	output := value.(*exprEffectOutput)
	if effect.Kind == eval.EffectErr { output.Err.Write(effect.Data); return }
	if effect.Kind == eval.EffectOut || effect.Kind == eval.EffectYield { output.Out.Write(effect.Data) }
}

func (options *exprArguments) Free() {
	slices.Free(mem.System, options.Args)
	for i := range options.Grants {
		for j := range options.Grants[i].Names { mem.FreeString(mem.System, options.Grants[i].Names[j]) }
		slices.Free(mem.System, options.Grants[i].Names)
	}
	slices.Free(mem.System, options.Grants)
	*options = exprArguments{}
}

func runExpr(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	parsed := parseExprArguments(args, errOut)
	defer parsed.Free()
	if !parsed.OK { return 2 }
	text := parsed.Text
	var data []byte
	if text == "" && parsed.File != "" { readData, readErr := os.ReadFile(mem.System, parsed.File); if readErr != nil { cliError(errOut, "FS_ERR", "cannot read expression: "+parsed.File); return 1 }; data, text = readData, string(readData) }
	if text == "" && parsed.File == "" { readData, readErr := io.ReadAll(mem.System, in); if readErr != nil { cliError(errOut, "FS_ERR", "cannot read stdin"); return 1 }; data, text = readData, string(readData) }
	defer freeCommandBytes(data)
	// nop is the identity operation. Wrapping the text keeps atoms such as
	// :nil or 0x10 expressions instead of definition right-hand-side string
	// templates, without changing the meaning of any expression form.
	command := "result = (nop " + text + ")"
	options := buildArguments{Command: command, Directory: parsed.Directory, Jobs: 1, Grants: parsed.Grants, NoDefaultGrants: true, OK: true}
	session := openBuildSession(options, errOut, true)
	defer session.Free()
	if session.Status != 0 { return session.Status }
	// The args frame must exist even when empty, so selectors yield empty
	// results instead of SEL_NO_CONTEXT.
	values := mem.AllocSlice[core.Value](mem.System, len(parsed.Args), len(parsed.Args)+1)
	for i := range parsed.Args { values[i] = core.NewString(mem.System, parsed.Args[i]) }
	session.Program.Eval.SetDefinitionArgs(values)
	session.Program.Eval.SetDefinitionEffectSink(writeExprEffect, &exprEffectOutput{Out: out, Err: errOut})
	for i := range values { values[i].Free(mem.System) }
	slices.Free(mem.System, values)
	started := session.Program.Start("result")
	if started.Diagnostic.Code != "" { emitDiagnostic(errOut, started.Diagnostic, false, session.Parsed.Source); started.Diagnostic.Free(mem.System); return 1 }
	handle := started.Handle
	defer handle.Free()
	for {
		session.Program.Tick(10)
		if handle.Definition && handle.Node.Current { writeValue(out, handle.Node.Latest); return 0 }
		result := handle.Poll()
		if !result.Done { continue }
		if result.Result.Diagnostic.Code != "" { emitDiagnostic(errOut, result.Result.Diagnostic, false, session.Parsed.Source); result.Result.Free(mem.System); return 1 }
		writeValue(out, result.Result.Value); result.Result.Free(mem.System); return 0
	}
}

func freeCommandBytes(data []byte) { if len(data) != 0 { mem.FreeSlice(mem.System, data) } }

func parseExprArguments(args []string, errOut io.Writer) exprArguments {
	inv := cli.Parse("expr", args)
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		inv.Free()
		return exprArguments{}
	}
	return exprArguments{Text: inv.Command, File: inv.File, Directory: inv.Directory, Args: inv.Args, Grants: inv.Grants, OK: inv.OK}
}
