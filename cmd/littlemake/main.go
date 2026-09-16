// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/lang/expr"
	"littlemake/lang/rule"
	"littlemake/lang/script"
	"littlemake/lang/template"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
)

func main() { os.Exit(Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// Run executes a command with explicit streams so the command behavior remains
// independently testable from process startup.
func Run(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	if len(args) < 2 || args[0] != "do" {
		cliError(errOut, "CMD_UNKNOWN", "expected `do parse`")
		return 2
	}
	if args[1] != "parse" {
		cliError(errOut, "CMD_UNKNOWN", "unknown command: "+args[1])
		return 2
	}
	return runParse(args[2:], in, out, errOut)
}

func writeAST(out io.Writer, lang string, name string, text string) int {
	enc := newASTEncoder(out, lang, name)
	failed := false
	if lang == "expr" {
		result := expr.Parse(mem.System, name, text)
		enc.expr(result.Expr)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "template" {
		result := template.ParseString(mem.System, name, text)
		enc.template(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else if lang == "rule" {
		result := rule.ParseRule(mem.System, name, text)
		enc.rule(result.Rule)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	} else {
		result := script.Parse(mem.System, name, text)
		enc.script(result)
		failed = enc.diagnostics(result.Diagnostics)
		result.Free()
	}
	enc.finish()
	if enc.err() != nil {
		return 1
	}
	if failed {
		return 1
	}
	return 0
}
