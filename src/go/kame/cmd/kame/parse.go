package main

import (
	"kame/cli"
	"kame/diagnostic"
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
	status := writeAST(out, lang, name, text)
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
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		inv.Free()
		return parseArgumentResult{}
	}
	return parseArgumentResult{Lang: inv.Lang, File: inv.File, OK: inv.OK}
}

func cliError(out io.Writer, code string, message string) {
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
