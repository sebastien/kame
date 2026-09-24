package main

import (
	"littlemake/lang/expr"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

type formatArguments struct { Lang string; InPlace bool; Check bool; Files []string; OK bool }

func runFormat(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	parsed := parseFormatArguments(args, errOut)
	if !parsed.OK { return 2 }
	if len(parsed.Files) == 0 {
		data, readErr := io.ReadAll(mem.System, in)
		if readErr != nil { cliError(errOut, "FS_ERR", "cannot read stdin"); return 1 }
		formatted, ok := formatSource(parsed.Lang, "<stdin>", string(data), errOut)
		if len(data) != 0 { mem.FreeSlice(mem.System, data) }
		if !ok { return 1 }
		io.WriteString(out, formatted); mem.FreeString(mem.System, formatted)
		return 0
	}
	different := false
	for i := range parsed.Files {
		data, readErr := os.ReadFile(mem.System, parsed.Files[i])
		if readErr != nil { cliError(errOut, "FS_ERR", "cannot read source: "+parsed.Files[i]); return 1 }
		formatted, ok := formatSource(parsed.Lang, parsed.Files[i], string(data), errOut)
		changed := ok && string(data) != formatted
		if len(data) != 0 { mem.FreeSlice(mem.System, data) }
		if !ok { return 1 }
		if parsed.Check && changed { io.WriteString(out, parsed.Files[i]); io.WriteString(out, "\n"); different = true }
		if parsed.InPlace && changed {
			temporary := parsed.Files[i] + ".littlemake-fmt.tmp"
			if os.WriteFile(temporary, []byte(formatted), 0o644) != nil || os.Rename(temporary, parsed.Files[i]) != nil { os.Remove(temporary); mem.FreeString(mem.System, formatted); cliError(errOut, "FS_ERR", "cannot replace source: "+parsed.Files[i]); return 1 }
		}
		if !parsed.InPlace && !parsed.Check { io.WriteString(out, formatted) }
		mem.FreeString(mem.System, formatted)
	}
	if different { return 1 }
	return 0
}

func parseFormatArguments(args []string, errOut io.Writer) formatArguments {
	result := formatArguments{Lang: "script"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-i" { result.InPlace = true; continue }
		if arg == "-n" { result.Check = true; continue }
		if arg == "--lang" {
			if i+1 == len(args) { cliError(errOut, "OPT_NO_VALUE", "missing value for --lang"); return formatArguments{} }
			i++; result.Lang = args[i]; continue
		}
		if len(arg) > 7 && arg[:7] == "--lang=" { result.Lang = arg[7:]; continue }
		if len(arg) != 0 && arg[0] == '-' { cliError(errOut, "OPT_UNKNOWN", "unknown option: "+arg); return formatArguments{} }
		result.Files = slices.Append(mem.System, result.Files, arg)
	}
	if result.InPlace && result.Check { cliError(errOut, "OPT_CONFLICT", "-i and -n cannot be used together"); return formatArguments{} }
	if result.Lang != "expr" && result.Lang != "template" && result.Lang != "rule" && result.Lang != "script" { cliError(errOut, "OPT_VALUE_INVALID", "invalid language: "+result.Lang); return formatArguments{} }
	result.OK = true
	return result
}

func formatSource(lang string, name string, text string, errOut io.Writer) (string, bool) {
	if lang == "expr" {
		result := expr.Parse(mem.System, name, text)
		if len(result.Diagnostics) != 0 { cliError(errOut, result.Diagnostics[0].Code, result.Diagnostics[0].Message); result.Free(); return "", false }
		formatted := expr.Format(mem.System, result.Expr); result.Free(); return formatted, true
	}
	if lang == "template" {
		result := template.ParseString(mem.System, name, text)
		if len(result.Diagnostics) != 0 { cliError(errOut, result.Diagnostics[0].Code, result.Diagnostics[0].Message); result.Free(); return "", false }
		formatted := template.FormatString(mem.System, result); result.Free(); return formatted, true
	}
	if lang == "rule" {
		result := rule.ParseRule(mem.System, name, text)
		if len(result.Diagnostics) != 0 { cliError(errOut, result.Diagnostics[0].Code, result.Diagnostics[0].Message); result.Free(); return "", false }
		formatted := rule.FormatRule(mem.System, result.Rule); result.Free(); return formatted, true
	}
	result := script.Parse(mem.System, name, text)
	if len(result.Diagnostics) != 0 { cliError(errOut, result.Diagnostics[0].Code, result.Diagnostics[0].Message); result.Free(); return "", false }
	formatted := script.Format(mem.System, result); result.Free(); return formatted, true
}
