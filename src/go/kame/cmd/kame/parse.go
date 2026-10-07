package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"solod.dev/so/bytes"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
)

var cliDiagnosticOut io.Writer
var cliDiagnosticJSON bool

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

type parseArgumentResult struct {
	Lang string
	File string
	OK   bool
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

func cliError(out io.Writer, code string, message string) {
	invocationHadDiagnostic = true
	clearDashboard(out)
	if code == "NO_MEMORY" {
		writeEmergencyDiagnostic(out, cliDiagnosticJSON)
		return
	}
	if cliDiagnosticJSON && cliDiagnosticOut != nil {
		writeJSONDiagnostic(cliDiagnosticOut, diagnostic.Diagnostic{Code: code, Severity: diagnostic.Error, Message: message})
		return
	}
	fmt.Fprintf(out, "error %s: %s\n", code, message)
}

// writeEmergencyDiagnostic allocates no diagnostic text, allowing the CLI to
// report allocator exhaustion instead of failing silently while formatting it.
func writeEmergencyDiagnostic(out io.Writer, json bool) {
	if json && cliDiagnosticOut != nil {
		io.WriteString(cliDiagnosticOut, "{\"schema\":1,\"type\":\"diagnostic\",\"diagnostic\":{\"code\":\"NO_MEMORY\",\"severity\":\"fatal\",\"message\":\"memory exhausted\"}}\n")
		return
	}
	io.WriteString(out, "fatal NO_MEMORY: memory exhausted\n")
}
