// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/source"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/unicode/utf8"
)

// Presentation state is initialized once per Run. It affects rendering only;
// diagnostic data and JSON output remain independent of terminal capabilities.
var diagnosticColor = "never"
var requestedDiagnosticColor = "auto"
var requestedDiagnosticFormat = "plain"
var diagnosticFormat = "plain"
var diagnosticWidth = 80
var cliDiagnosticOut io.Writer
var cliDiagnosticJSON bool

func configureDiagnosticPresentation(options buildArguments) {
	color, format := requestedDiagnosticColor, requestedDiagnosticFormat
	if options.Color != "" {
		color = options.Color
	}
	if options.DiagnosticFormat != "" {
		format = options.DiagnosticFormat
	}
	if format == "" {
		format = "plain"
		if selectedOutput == "ansi" {
			format = "human"
		}
	}
	diagnosticFormat = format
	environment := posix.Environment(mem.System)
	diagnosticColor = resolveDiagnosticColor(color, "human", posix.StderrIsTerminal(), environment)
	stdoutColor = resolveDiagnosticColor(color, "human", posix.StdoutIsTerminal(), environment) == "always"
	posix.FreeEnvironment(mem.System, environment)
	diagnosticWidth = posix.StderrWidth()
	if diagnosticFormat == "plain" {
		diagnosticWidth = 80
	}
	if diagnosticWidth < 20 {
		diagnosticWidth = 80
	}
}

func resolveDiagnosticColor(color string, format string, terminal bool, environment []string) string {
	if format != "human" || color == "never" {
		return "never"
	}
	if color == "always" {
		return "always"
	}
	if environmentValue(environment, "NO_COLOR") != "" || environmentValue(environment, "CLICOLOR") == "0" || environmentValue(environment, "TERM") == "dumb" {
		return "never"
	}
	if environmentValue(environment, "CLICOLOR_FORCE") != "" && environmentValue(environment, "CLICOLOR_FORCE") != "0" {
		return "always"
	}
	if terminal {
		return "always"
	}
	return "never"
}

func environmentValue(values []string, name string) string {
	for i := range values {
		value := values[i]
		if len(value) <= len(name) || value[:len(name)] != name || value[len(name)] != '=' {
			continue
		}
		return value[len(name)+1:]
	}
	return ""
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

func emitDiagnostic(out io.Writer, d diagnostic.Diagnostic, json bool, src *source.Source) {
	invocationHadDiagnostic = true
	clearDashboard(out)
	if d.Code == "NO_MEMORY" {
		writeEmergencyDiagnostic(out, json)
		return
	}
	if json {
		writeJSONDiagnostic(out, d)
		return
	}
	cliDiagnosticWithSource(out, d, src)
}

// diagnosticWriter picks the stream for command diagnostics: JSON events are
// stdout-only by specification; human diagnostics stay on stderr.
func diagnosticWriter(out io.Writer, errOut io.Writer, json bool) io.Writer {
	if json && out != nil {
		return out
	}
	return errOut
}

func cliDiagnosticWithSource(out io.Writer, d diagnostic.Diagnostic, src *source.Source) {
	cliDiagnosticWithSourceWidth(out, d, src, diagnosticWidth)
}

// cliDiagnosticWithSourceWidth makes source rendering reproducible for a
// supplied terminal width. The production renderer uses a stable 80-column
// fallback when stderr is redirected or the platform cannot report its width.
func cliDiagnosticWithSourceWidth(out io.Writer, d diagnostic.Diagnostic, src *source.Source, width int) {
	if width < 20 {
		width = 80
	}
	renderSource, loaded := diagnosticSource(d.Source, src)
	styled := diagnosticColor == "always" && diagnosticFormat == "human"
	token := "message.error"
	if d.Severity == diagnostic.Warning {
		token = "message.warning"
	} else if d.Severity == diagnostic.Fatal {
		token = "message.fatal"
	}
	if d.Target != "" {
		if diagnosticFormat == "human" {
			cli.Style(out, token, diagnosticSeverity(d.Severity), styled)
			fmt.Fprintf(out, " [%s] %s\n", d.Target, d.Code)
		} else {
			fmt.Fprintf(out, "%s failed: %s\n", d.Target, d.Code)
		}
		if len(d.TargetStack) > 1 {
			io.WriteString(out, "  required by ")
			for i := range d.TargetStack {
				if i != 0 {
					io.WriteString(out, " -> ")
				}
				io.WriteString(out, d.TargetStack[i])
			}
			io.WriteString(out, "\n")
		}
	}
	if d.Source != "" && renderSource != nil && d.Source == renderSource.Name {
		position := renderSource.Position(d.Span.Start)
		fmt.Fprintf(out, "%s:%d:%d: ", d.Source, position.Line, position.Column)
		writeDiagnosticMessage(out, d, token, styled)
		start := d.Span.Start
		if start < 0 {
			start = 0
		}
		if start > len(renderSource.Text) {
			start = len(renderSource.Text)
		}
		lineStart, lineEnd := renderSource.LineBounds(start)
		writeWrappedExcerpt(out, renderSource.Text, lineStart, lineEnd, start, d.Span.End, width)
	} else if d.Source != "" {
		io.WriteString(out, d.Source+": ")
		writeDiagnosticMessage(out, d, token, styled)
	} else {
		writeDiagnosticMessage(out, d, token, styled)
	}
	for i := range d.Notes {
		io.WriteString(out, "note: ")
		io.WriteString(out, d.Notes[i])
		io.WriteString(out, "\n")
	}
	for i := range d.Related {
		io.WriteString(out, "note: ")
		if d.Related[i].Source != "" {
			writeDiagnosticLocation(out, d.Related[i].Source, d.Related[i].Span.Start, renderSource)
			io.WriteString(out, ": ")
		}
		io.WriteString(out, d.Related[i].Message)
		io.WriteString(out, "\n")
	}
	for i := range d.Frames {
		io.WriteString(out, "while ")
		if d.Frames[i].Kind != "" {
			io.WriteString(out, d.Frames[i].Kind)
			io.WriteString(out, " ")
		}
		io.WriteString(out, d.Frames[i].Label)
		if d.Frames[i].Source != "" {
			io.WriteString(out, " at ")
			writeDiagnosticLocation(out, d.Frames[i].Source, d.Frames[i].Span.Start, renderSource)
		}
		io.WriteString(out, "\n")
	}
	for i := range d.Tips {
		io.WriteString(out, "help: ")
		io.WriteString(out, d.Tips[i])
		io.WriteString(out, "\n")
	}
	if d.Cause.Kind != "" {
		io.WriteString(out, "caused by: ")
		io.WriteString(out, d.Cause.Message)
		if d.Cause.HasStatus {
			fmt.Fprintf(out, " (status %d)", d.Cause.Status)
		}
		if d.Cause.HasSignal {
			fmt.Fprintf(out, " (signal %d)", d.Cause.Signal)
		}
		io.WriteString(out, "\n")
	}
	if loaded {
		renderSource.Free(mem.System)
	}
}

func writeDiagnosticMessage(out io.Writer, d diagnostic.Diagnostic, token string, styled bool) {
	cli.Style(out, token, diagnosticSeverity(d.Severity), styled)
	io.WriteString(out, " ")
	cli.Style(out, "diagnostic.code", d.Code, styled)
	io.WriteString(out, ": "+d.Message+"\n")
}

// diagnosticSource uses the already parsed source when possible. A different
// file-backed primary source is loaded only for rendering and is never exposed
// to JSON or stored in the diagnostic.
func diagnosticSource(name string, primary *source.Source) (*source.Source, bool) {
	if name == "" || (primary != nil && primary.Name == name && !primary.Expanded) {
		return primary, false
	}
	data, readErr := os.ReadFile(mem.System, name)
	if readErr != nil {
		return nil, false
	}
	loaded := source.New(mem.System, name, string(data))
	mem.FreeSlice(mem.System, data)
	return loaded, true
}

func writeDiagnosticLocation(out io.Writer, name string, offset int, primary *source.Source) {
	src, loaded := diagnosticSource(name, primary)
	if src != nil && src.Name == name {
		position := src.Position(offset)
		fmt.Fprintf(out, "%s:%d:%d", name, position.Line, position.Column)
	} else {
		io.WriteString(out, name)
	}
	if loaded {
		src.Free(mem.System)
	}
}

func writeWrappedExcerpt(out io.Writer, text string, lineStart int, lineEnd int, start int, end int, width int) {
	if lineStart == lineEnd {
		io.WriteString(out, "\n")
		writeMarker(out, text, lineStart, start, end, lineEnd)
		return
	}
	marked, segmentStart := false, lineStart
	for segmentStart < lineEnd {
		segmentEnd := excerptSegmentEnd(text, segmentStart, lineEnd, width)
		io.WriteString(out, text[segmentStart:segmentEnd])
		io.WriteString(out, "\n")
		if !marked && start >= segmentStart && (start < segmentEnd || (segmentEnd == lineEnd && start == segmentEnd)) {
			writeMarker(out, text, segmentStart, start, end, segmentEnd)
			marked = true
		}
		segmentStart = segmentEnd
	}
}

func excerptSegmentEnd(text string, start int, limit int, width int) int {
	column, i := 1, start
	for i < limit {
		cells, size := 1, 1
		if text[i] == '\t' {
			cells = 8 - (column-1)%8
		} else {
			r, decoded := utf8.DecodeRuneInString(text[i:])
			if decoded == 0 {
				break
			}
			cells, size = source.DisplayWidth(r), decoded
		}
		if i != start && column-1+cells > width {
			break
		}
		column, i = column+cells, i+size
	}
	return i
}

func writeMarker(out io.Writer, text string, lineStart int, start int, end int, lineEnd int) {
	column := 1
	for i := lineStart; i < start; {
		if text[i] == '\t' {
			io.WriteString(out, "\t")
			column += 8 - (column-1)%8
			i++
			continue
		}
		r, width := utf8.DecodeRuneInString(text[i:])
		if width == 0 {
			break
		}
		for n := 0; n < source.DisplayWidth(r); n++ {
			io.WriteString(out, " ")
		}
		column += source.DisplayWidth(r)
		i += width
	}
	if end < start {
		end = start
	}
	if end > lineEnd {
		end = lineEnd
	}
	width := 0
	for i := start; i < end; {
		if text[i] == '\t' {
			cells := 8 - (column-1)%8
			width, column = width+cells, column+cells
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if size == 0 {
			break
		}
		cells := source.DisplayWidth(r)
		width, column = width+cells, column+cells
		i += size
	}
	if width < 1 {
		width = 1
	}
	io.WriteString(out, "^")
	for i := 1; i < width; i++ {
		io.WriteString(out, "~")
	}
	io.WriteString(out, "\n")
}

func diagnosticSeverity(severity diagnostic.Severity) string {
	if severity == diagnostic.Warning {
		return "warning"
	}
	if severity == diagnostic.Fatal {
		return "fatal"
	}
	return "error"
}

// annotateTargetDiagnostic appends deterministic suggestions to unknown-target
// diagnostics. Path-like targets written without an explicit ./ prefix suggest
// the explicit form that file rules require.
func annotateTargetDiagnostic(d *diagnostic.Diagnostic, target string) {
	if d == nil || len(target) == 0 {
		return
	}
	if d.Target == "" {
		d.Target = cloneCommandText(target)
	}
	if len(d.TargetStack) == 0 {
		d.TargetStack = slices.Append(mem.System, d.TargetStack, cloneCommandText(target))
	} else if d.TargetStack[0] != target {
		stack := slices.Make[string](mem.System, len(d.TargetStack)+1)
		stack[0] = cloneCommandText(target)
		for i := range d.TargetStack {
			stack[i+1] = d.TargetStack[i]
		}
		slices.Free(mem.System, d.TargetStack)
		d.TargetStack = stack
	}
	if d.Code != "TGT_NO_RULE" {
		return
	}
	if target[0] == '/' {
		return
	}
	if len(target) >= 2 && target[0] == '.' && target[1] == '/' {
		return
	}
	pathLike := false
	for i := 0; i < len(target); i++ {
		if target[i] == '.' || target[i] == '/' {
			pathLike = true
			break
		}
	}
	if !pathLike {
		return
	}
	note := "did you mean ./" + target + "?"
	for i := range d.Notes {
		if d.Notes[i] == note {
			return
		}
	}
	d.Notes = slices.Append(mem.System, d.Notes, cloneCommandText(note))
}
