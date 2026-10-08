package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/lang/format"
	"kame/lang/source"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

type formatArguments struct {
	Lang        string
	Indent      string
	IndentWidth int
	Comment     string
	InPlace     bool
	Check       bool
	Files       []string
	OK          bool
}

func (options *formatArguments) Free() {
	slices.Free(mem.System, options.Files)
	*options = formatArguments{}
}

func runFormat(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	parsed := parseFormatArguments(args, errOut)
	formatHasResults = parsed.Check || parsed.InPlace
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	if len(parsed.Files) == 0 {
		// Stdin has no file to replace; in-place retains ordinary stdout output.
		parsed.InPlace = false
		formatHasResults = parsed.Check
		data, readErr := io.ReadAll(mem.System, in)
		if readErr != nil {
			invocationCounts.Failed++
			cliError(errOut, "FS_ERR", "cannot read stdin")
			return 1
		}
		formatted, ok := formatSource(parsed.Lang, "<stdin>", string(data), parsed.Indent, parsed.IndentWidth, parsed.Comment, errOut)
		changed := string(data) != formatted
		mem.FreeSlice(mem.System, data)
		if !ok {
			invocationCounts.Failed++
			return 1
		}
		writeFormattedResult(out, errOut, "<stdin>", parsed, formatted, changed)
		invocationCounts.Completed++
		mem.FreeString(mem.System, formatted)
		if parsed.Check && changed {
			if !cliDiagnosticJSON {
				io.WriteString(out, "<stdin>\n")
			}
			return 1
		}
		return 0
	}
	different := false
	for i := range parsed.Files {
		data, readErr := os.ReadFile(mem.System, parsed.Files[i])
		if readErr != nil {
			invocationCounts.Failed++
			cliError(errOut, "FS_ERR", "cannot read source: "+parsed.Files[i])
			return 1
		}
		formatted, ok := formatSource(parsed.Lang, parsed.Files[i], string(data), parsed.Indent, parsed.IndentWidth, parsed.Comment, errOut)
		changed := ok && string(data) != formatted
		mem.FreeSlice(mem.System, data)
		if !ok {
			invocationCounts.Failed++
			return 1
		}
		if parsed.Check && changed && !cliDiagnosticJSON {
			io.WriteString(out, parsed.Files[i])
			io.WriteString(out, "\n")
			different = true
		}
		if parsed.InPlace && changed {
			temporary := parsed.Files[i] + ".kame-fmt.tmp"
			if os.WriteFile(temporary, []byte(formatted), 0o644) != nil || os.Rename(temporary, parsed.Files[i]) != nil {
				invocationCounts.Failed++
				os.Remove(temporary)
				mem.FreeString(mem.System, formatted)
				cliError(errOut, "FS_ERR", "cannot replace source: "+parsed.Files[i])
				return 1
			}
		}
		if changed && parsed.Check {
			different = true
		}
		writeFormattedResult(out, errOut, parsed.Files[i], parsed, formatted, changed)
		invocationCounts.Completed++
		mem.FreeString(mem.System, formatted)
	}
	if different {
		return 1
	}
	return 0
}

func writeFormattedResult(out io.Writer, errOut io.Writer, sourceName string, options formatArguments, text string, changed bool) {
	action := "format"
	if options.Check {
		action = "check"
	}
	if options.InPlace {
		action = "in-place"
	}
	if cliDiagnosticJSON {
		cli.WriteDataResult(out, "format-result", sourceName, "", action, changed, []byte(text), action == "format")
		return
	}
	if action == "format" {
		io.WriteString(out, text)
		return
	}
	cli.Style(errOut, "status.success", "done ", diagnosticColor == "always")
	io.WriteString(errOut, "fmt "+sourceName+" · ")
	if !changed {
		io.WriteString(errOut, "unchanged\n")
	} else if options.Check {
		io.WriteString(errOut, "would change\n")
	} else {
		io.WriteString(errOut, "formatted\n")
	}
}

func parseFormatArguments(args []string, errOut io.Writer) formatArguments {
	inv := cli.Parse("fmt", args)
	if !inv.OK {
		cliError(errOut, inv.Error.Code, inv.Error.Message)
		inv.Free()
		return formatArguments{}
	}
	return formatArguments{Lang: inv.Lang, Indent: inv.Indent, IndentWidth: inv.IndentWidth, Comment: inv.Comment, InPlace: inv.InPlace, Check: inv.Check, Files: inv.Files, OK: inv.OK}
}

func formatSource(lang string, name string, text string, indentStyle string, indentWidth int, comment string, errOut io.Writer) (string, bool) {
	result := format.SourceWithComment(mem.System, lang, name, text, indentStyle, indentWidth, comment)
	if !result.OK {
		src := source.New(mem.System, name, text)
		emitDiagnostic(diagnosticWriter(cliDiagnosticOut, errOut, cliDiagnosticJSON), diagnostic.Diagnostic{Code: result.Code, Severity: diagnostic.Error, Message: result.Message, Source: name, Span: diagnostic.Span{Start: result.Span.Start, End: result.Span.End}}, cliDiagnosticJSON, src)
		src.Free(mem.System)
		mem.FreeString(mem.System, result.Code)
		mem.FreeString(mem.System, result.Message)
		return "", false
	}
	return result.Text, true
}
