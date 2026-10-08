package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"solod.dev/so/bytes"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
)

type parseArgumentResult struct {
	Lang string
	File string
	OK   bool
}

func runParse(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	parsed := parseArguments(args, errOut)
	if !parsed.OK {
		return 2
	}
	lang, file := parsed.Lang, parsed.File
	name, text := "<stdin>", ""
	var data []byte
	if file == "" {
		readData, readErr := io.ReadAll(mem.System, in)
		if readErr != nil {
			cliError(errOut, "FS_ERR", "cannot read stdin")
			return 1
		}
		data, text = readData, string(readData)
	} else {
		readData, readErr := os.ReadFile(mem.System, file)
		if readErr != nil {
			cliError(errOut, "FS_ERR", "cannot read source: "+file)
			return 1
		}
		name, data, text = file, readData, string(readData)
	}
	var buffer = bytes.NewBuffer(mem.System, nil)
	status := writeAST(&buffer, lang, name, text)
	if cliDiagnosticJSON && status == 0 {
		io.WriteString(out, buffer.String())
	} else if !cliDiagnosticJSON && status == 0 {
		cli.WriteReport(out, "parse "+name+" · "+lang, buffer.String(), stdoutColor)
	} else {
		var document core.Value
		if core.ParseJSON(mem.System, []byte(buffer.String()), &document) {
			diagnostics := documentField(document, "diagnostics")
			src := source.New(mem.System, name, text)
			for i := range diagnostics.List {
				item := diagnostics.List[i]
				code := documentField(item, "code")
				message := documentField(item, "message")
				span := documentField(item, "span")
				start := documentField(span, "start")
				end := documentField(span, "end")
				emitDiagnostic(diagnosticWriter(out, errOut, cliDiagnosticJSON), diagnostic.Diagnostic{Code: code.Text, Severity: diagnostic.Error, Message: message.Text, Source: name, Span: diagnostic.Span{Start: int(start.Int), End: int(end.Int)}}, cliDiagnosticJSON, src)
			}
			src.Free(mem.System)
			document.Free(mem.System)
		}
	}
	buffer.Free()
	mem.FreeSlice(mem.System, data)
	return status
}

func parseArguments(args []string, errOut io.Writer) parseArgumentResult {
	inv := cli.Parse("parse", args)
	defer inv.Free()
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		return parseArgumentResult{}
	}
	return parseArgumentResult{Lang: inv.Lang, File: inv.File, OK: inv.OK}
}

// documentField borrows a JSON record field; it must not be freed independently.
func documentField(value core.Value, name string) core.Value {
	for i := range value.Record {
		if value.Record[i].Key == name {
			return value.Record[i].Value
		}
	}
	return core.Value{}
}
