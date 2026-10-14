package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/host/posix"
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
	Inputs      []cli.RunInput
	OK          bool
}

func (options *formatArguments) Free() {
	for i := range options.Files {
		mem.FreeString(mem.System, options.Files[i])
	}
	slices.Free(mem.System, options.Files)
	slices.Free(mem.System, options.Inputs)
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
		formatHasResults = parsed.Check
		var data []byte
		text, name := parsed.Inputs[0].Value, "<command:1>"
		if parsed.Inputs[0].Kind == "stdin" {
			name = "<stdin>"
			readData, readErr := io.ReadAll(mem.System, in)
			if readErr != nil {
				invocationCounts.Failed++
				cliError(errOut, "FS_ERR", "cannot read stdin")
				return 1
			}
			data, text = readData, string(readData)
		}
		formatted, ok := formatSource(parsed.Lang, name, text, parsed.Indent, parsed.IndentWidth, parsed.Comment, errOut)
		changed := text != formatted
		mem.FreeSlice(mem.System, data)
		if !ok {
			invocationCounts.Failed++
			return 1
		}
		writeFormattedResult(out, errOut, name, parsed, formatted, changed)
		invocationCounts.Completed++
		mem.FreeString(mem.System, formatted)
		if parsed.Check && changed {
			if !cliDiagnosticJSON {
				io.WriteString(out, name+"\n")
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
			// Stat follows source symlinks, as ReadFile does. Publication replaces
			// the source entry, not its target, retaining the target's permissions.
			info, statErr := os.Stat(parsed.Files[i])
			h := posix.Host{Alloc: mem.System}
			if statErr != nil || h.WriteFileAtomic(parsed.Files[i], []byte(formatted), uint32(info.Mode().Perm()), false) != nil {
				invocationCounts.Failed++
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
	for i := range inv.Files {
		inv.Files[i] = languageSourcePath(inv.Directory, inv.Files[i])
	}
	return formatArguments{Lang: inv.Lang, Indent: inv.Indent, IndentWidth: inv.IndentWidth, Comment: inv.Comment, InPlace: inv.InPlace, Check: inv.Check, Files: inv.Files, Inputs: inv.Inputs, OK: inv.OK}
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
