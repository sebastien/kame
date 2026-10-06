package main

import (
	"kame/cli"
	"kame/lang/format"
	"kame/lang/source"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/strconv"
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
	defer parsed.Free()
	if !parsed.OK {
		return 2
	}
	if len(parsed.Files) == 0 {
		data, readErr := io.ReadAll(mem.System, in)
		if readErr != nil {
			cliError(errOut, "FS_ERR", "cannot read stdin")
			return 1
		}
		formatted, ok := formatSource(parsed.Lang, "<stdin>", string(data), parsed.Indent, parsed.IndentWidth, parsed.Comment, errOut)
		if len(data) != 0 {
			mem.FreeSlice(mem.System, data)
		}
		if !ok {
			return 1
		}
		io.WriteString(out, formatted)
		mem.FreeString(mem.System, formatted)
		return 0
	}
	different := false
	for i := range parsed.Files {
		data, readErr := os.ReadFile(mem.System, parsed.Files[i])
		if readErr != nil {
			cliError(errOut, "FS_ERR", "cannot read source: "+parsed.Files[i])
			return 1
		}
		formatted, ok := formatSource(parsed.Lang, parsed.Files[i], string(data), parsed.Indent, parsed.IndentWidth, parsed.Comment, errOut)
		changed := ok && string(data) != formatted
		if len(data) != 0 {
			mem.FreeSlice(mem.System, data)
		}
		if !ok {
			return 1
		}
		if parsed.Check && changed {
			io.WriteString(out, parsed.Files[i])
			io.WriteString(out, "\n")
			different = true
		}
		if parsed.InPlace && changed {
			temporary := parsed.Files[i] + ".kame-fmt.tmp"
			if os.WriteFile(temporary, []byte(formatted), 0o644) != nil || os.Rename(temporary, parsed.Files[i]) != nil {
				os.Remove(temporary)
				mem.FreeString(mem.System, formatted)
				cliError(errOut, "FS_ERR", "cannot replace source: "+parsed.Files[i])
				return 1
			}
		}
		if !parsed.InPlace && !parsed.Check {
			io.WriteString(out, formatted)
		}
		mem.FreeString(mem.System, formatted)
	}
	if different {
		return 1
	}
	return 0
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
		src := mem.Alloc[source.Source](mem.System)
		src.Name, src.Text = name, text
		position := src.Position(result.Span.Start)
		io.WriteString(errOut, name)
		io.WriteString(errOut, ":")
		var positionText [strconv.MaxIntBase10Len]byte
		io.WriteString(errOut, strconv.Itoa(positionText[:], position.Line))
		io.WriteString(errOut, ":")
		io.WriteString(errOut, strconv.Itoa(positionText[:], position.Column))
		io.WriteString(errOut, ": error ")
		io.WriteString(errOut, result.Code)
		io.WriteString(errOut, ": ")
		io.WriteString(errOut, result.Message)
		io.WriteString(errOut, "\n")
		mem.Free(mem.System, src)
		mem.FreeString(mem.System, result.Code)
		mem.FreeString(mem.System, result.Message)
		return "", false
	}
	return result.Text, true
}
