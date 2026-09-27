package main

import (
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
	lang, file := "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+2 != len(args) || file != "" {
				cliError(errOut, "OPT_VALUE_INVALID", "parse accepts at most one file")
				return parseArgumentResult{}
			}
			file = args[i+1]
			break
		}
		if arg == "--lang" {
			if i+1 == len(args) {
				cliError(errOut, "OPT_NO_VALUE", "missing value for --lang")
				return parseArgumentResult{}
			}
			i++
			lang = args[i]
			continue
		}
		if len(arg) > 7 && arg[:7] == "--lang=" {
			lang = arg[7:]
			continue
		}
		if len(arg) != 0 && arg[0] == '-' {
			cliError(errOut, "OPT_UNKNOWN", "unknown option: "+arg)
			return parseArgumentResult{}
		}
		if file != "" {
			cliError(errOut, "OPT_VALUE_INVALID", "parse accepts at most one file")
			return parseArgumentResult{}
		}
		file = arg
	}
	if lang == "" {
		cliError(errOut, "OPT_NO_VALUE", "missing required --lang")
		return parseArgumentResult{}
	}
	if lang != "expr" && lang != "template" && lang != "rule" && lang != "script" {
		cliError(errOut, "OPT_VALUE_INVALID", "invalid language: "+lang)
		return parseArgumentResult{}
	}
	return parseArgumentResult{Lang: lang, File: file, OK: true}
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
