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
	"solod.dev/so/strconv"
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
var diagnosticSources []*source.Source

func rememberDiagnosticSource(name string, text string) {
	for i := range diagnosticSources {
		if diagnosticSources[i].Name == name {
			diagnosticSources[i].Free(mem.System)
			diagnosticSources[i] = source.New(mem.System, name, text)
			return
		}
	}
	diagnosticSources = slices.Append(mem.System, diagnosticSources, source.New(mem.System, name, text))
}

func restoreDiagnosticSources(previous []*source.Source) {
	for i := range diagnosticSources {
		diagnosticSources[i].Free(mem.System)
	}
	slices.Free(mem.System, diagnosticSources)
	diagnosticSources = previous
}

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
	fmt.Fprintf(out, "error[%s]: %s\n", code, message)
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
	styled := diagnosticColor == "always" && diagnosticFormat == "human"
	token := "message.error"
	if d.Severity == diagnostic.Warning {
		token = "message.warning"
	} else if d.Severity == diagnostic.Fatal {
		token = "message.fatal"
	}
	writeDiagnosticMessage(out, d, token, styled)
	if d.Target != "" {
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
	writeDiagnosticSourceBlock(out, d.Source, d.Span, src, width, token)
	if len(d.Frames) != 0 {
		io.WriteString(out, "\n")
		cli.Style(out, "heading", "Trace (innermost first)", styled)
		io.WriteString(out, "\n")
	}
	for i := range d.Frames {
		writeDiagnosticFrame(out, d, i, src, width, styled)
	}
	for i := range d.Notes {
		io.WriteString(out, "note: ")
		io.WriteString(out, d.Notes[i])
		io.WriteString(out, "\n")
	}
	for i := range d.Related {
		io.WriteString(out, "note: ")
		io.WriteString(out, d.Related[i].Message)
		io.WriteString(out, "\n")
		writeDiagnosticSourceBlock(out, d.Related[i].Source, d.Related[i].Span, src, width, "location")
	}
	for i := range d.Tips {
		cli.Style(out, "message.tip", "help: ", styled)
		io.WriteString(out, d.Tips[i])
		io.WriteString(out, "\n")
	}
	if d.Cause.Kind != "" {
		io.WriteString(out, "caused by: ")
		message := d.Cause.Message
		if message == "" {
			message = d.Cause.Kind
			if d.Cause.Kind == "process" {
				message = "process exited"
			}
		}
		io.WriteString(out, message)
		if d.Cause.HasStatus {
			fmt.Fprintf(out, " (status %d)", d.Cause.Status)
		}
		if d.Cause.HasSignal {
			fmt.Fprintf(out, " (signal %d)", d.Cause.Signal)
		}
		io.WriteString(out, "\n")
	}
}

func writeDiagnosticMessage(out io.Writer, d diagnostic.Diagnostic, token string, styled bool) {
	cli.Style(out, token, diagnosticSeverity(d.Severity), styled)
	io.WriteString(out, "[")
	cli.Style(out, "diagnostic.code", d.Code, styled)
	io.WriteString(out, "]: ")
	if d.Target != "" {
		fmt.Fprintf(out, "[%s] ", d.Target)
	}
	io.WriteString(out, d.Message+"\n")
}

// diagnosticSource uses the already parsed source when possible. A different
// file-backed primary source is loaded only for rendering and is never exposed
// to JSON or stored in the diagnostic.
func diagnosticSource(name string, primary *source.Source) (*source.Source, bool) {
	for i := range diagnosticSources {
		if diagnosticSources[i].Name == name {
			return diagnosticSources[i], false
		}
	}
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

func writeDiagnosticSourceBlock(out io.Writer, name string, span diagnostic.Span, primary *source.Source, width int, token string) {
	if name == "" {
		return
	}
	src, loaded := diagnosticSource(name, primary)
	styled := diagnosticColor == "always" && diagnosticFormat == "human"
	arrow := "  --> "
	if diagnosticFormat == "human" {
		arrow = "  ╭─ "
	}
	cli.Style(out, "structure", arrow, styled)
	if src != nil && src.Name == name {
		start := span.Start
		if start < 0 {
			start = 0
		}
		if start > len(src.Text) {
			start = len(src.Text)
		}
		position := src.Position(start)
		writeStyledDiagnosticLocation(out, name, position.Line, position.Column, styled)
		io.WriteString(out, "\n")
		lineStart, lineEnd := src.LineBounds(start)
		writeDiagnosticExcerpt(out, src.Text, lineStart, lineEnd, start, span.End, width, position.Line, token)
	} else {
		cli.Style(out, "location", name, styled)
		io.WriteString(out, "\n")
	}
	if loaded {
		src.Free(mem.System)
	}
}

func writeStyledDiagnosticLocation(out io.Writer, name string, line int, column int, styled bool) {
	var lineBuffer, columnBuffer [strconv.MaxIntBase10Len]byte
	lineText := strconv.FormatInt(lineBuffer[:], int64(line), 10)
	columnText := strconv.FormatInt(columnBuffer[:], int64(column), 10)
	cli.Style(out, "location", name+":"+lineText+":"+columnText, styled)
}

func writeDiagnosticFrame(out io.Writer, d diagnostic.Diagnostic, index int, primary *source.Source, width int, styled bool) {
	f := d.Frames[index]
	branch, separator := "  ", " at "
	if diagnosticFormat == "human" {
		branch, separator = "  ├─ ", " · "
		if index == len(d.Frames)-1 {
			branch = "  ╰─ "
		}
	}
	cli.Style(out, "structure", branch, styled)
	var buffer [strconv.MaxIntBase10Len]byte
	cli.Style(out, "text.muted", "#"+strconv.FormatInt(buffer[:], int64(index), 10)+"  ", styled)
	if f.Kind != "" {
		cli.Style(out, "text.muted", f.Kind+" ", styled)
	}
	labelToken := "worker.tool"
	if f.Kind == "definition" {
		labelToken = "target.task"
	}
	if f.Kind == "include" {
		labelToken = "value.path"
	}
	cli.Style(out, labelToken, f.Label, styled)
	var src *source.Source
	loaded := false
	if f.Source != "" {
		src, loaded = diagnosticSource(f.Source, primary)
		cli.Style(out, "structure", separator, styled)
		if src != nil && src.Name == f.Source {
			position := src.Position(f.Span.Start)
			writeStyledDiagnosticLocation(out, f.Source, position.Line, position.Column, styled)
		} else {
			cli.Style(out, "location", f.Source, styled)
		}
	}
	io.WriteString(out, "\n")
	if src != nil && src.Name == f.Source {
		start := f.Span.Start
		if start < 0 {
			start = 0
		}
		if start > len(src.Text) {
			start = len(src.Text)
		}
		lineStart, lineEnd := src.LineBounds(start)
		if !diagnosticLineSeen(d, index, src, lineStart) {
			writeDiagnosticExcerpt(out, src.Text, lineStart, lineEnd, start, f.Span.End, width, 0, "")
		}
	}
	if loaded {
		src.Free(mem.System)
	}
}

// Share source lines, not frames: distinct contexts keep their labels/locations.
func diagnosticLineSeen(d diagnostic.Diagnostic, index int, src *source.Source, lineStart int) bool {
	for i := -1; i < index; i++ {
		name, at := d.Source, d.Span.Start
		if i >= 0 {
			name, at = d.Frames[i].Source, d.Frames[i].Span.Start
		}
		if name != src.Name {
			continue
		}
		if at < 0 {
			at = 0
		}
		if at > len(src.Text) {
			at = len(src.Text)
		}
		start, _ := src.LineBounds(at)
		if start == lineStart {
			return true
		}
	}
	return false
}

// Keep one visual line around the focus, rather than wrapping a tiny suffix onto
// another row. Positions remain authored; ellipses describe presentation only.
func writeDiagnosticExcerpt(out io.Writer, text string, lineStart int, lineEnd int, start int, end int, width int, line int, token string) {
	var buffer [strconv.MaxIntBase10Len]byte
	number := strconv.FormatInt(buffer[:], int64(line), 10)
	if line == 0 {
		number = ""
	}
	rich := diagnosticFormat == "human"
	styled := rich && diagnosticColor == "always"
	rail, ellipsis, ellipsisWidth := " | ", "...", 3
	if rich {
		rail, ellipsis, ellipsisWidth = " │ ", "…", 1
	}
	gutter := " " + number + rail
	if line == 0 {
		gutter = "    " + rail
	}
	width -= len(number) + 4
	if line == 0 {
		width -= 3
	}
	if width < 8 {
		width = 8
	}
	windowStart := lineStart
	windowEnd := excerptSegmentEnd(text, windowStart, lineEnd, width)
	focusLimit := excerptSegmentEnd(text, lineStart, lineEnd, width-ellipsisWidth)
	if (start >= focusLimit || end > focusLimit) && windowEnd < lineEnd {
		// Count display cells before the focus, using the original tab stops.
		column := 0
		for i := lineStart; i < start; {
			if text[i] == '\t' {
				column += 8 - column%8
				i++
				continue
			}
			r, size := utf8.DecodeRuneInString(text[i:])
			if size == 0 {
				break
			}
			column += source.DisplayWidth(r)
			i += size
		}
		context := width / 3
		if context > width-2*ellipsisWidth-2 {
			context = width - 2*ellipsisWidth - 2
		}
		if context < 0 {
			context = 0
		}
		if column > context {
			windowStart = excerptSegmentEnd(text, lineStart, start, column-context)
		}
	}
	left := windowStart > lineStart
	if left {
		width -= ellipsisWidth
	}
	windowEnd = excerptSegmentEnd(text, windowStart, lineEnd, width)
	right := windowEnd < lineEnd
	if right {
		windowEnd = excerptSegmentEnd(text, windowStart, lineEnd, width-ellipsisWidth)
	}
	cli.Style(out, "structure", gutter, styled)
	if left {
		cli.Style(out, "text.muted", ellipsis, styled)
	}
	writeExcerptText(out, text[windowStart:windowEnd])
	if right {
		cli.Style(out, "text.muted", ellipsis, styled)
	}
	io.WriteString(out, "\n")
	if line != 0 {
		writeDiagnosticSpaces(out, len(number)+1)
		cli.Style(out, "structure", rail, styled)
		if left {
			writeDiagnosticSpaces(out, ellipsisWidth)
		}
		writeMarker(out, text, windowStart, start, end, windowEnd, token, styled)
	}
}

func writeDiagnosticSpaces(out io.Writer, count int) {
	for i := 0; i < count; i++ {
		io.WriteString(out, " ")
	}
}

// Expand tabs before adding gutters so terminal tab stops cannot move markers.
func writeExcerptText(out io.Writer, text string) {
	column := 1
	for i := 0; i < len(text); {
		if text[i] == '\t' {
			cells := 8 - (column-1)%8
			for n := 0; n < cells; n++ {
				io.WriteString(out, " ")
			}
			column += cells
			i++
		} else {
			r, size := utf8.DecodeRuneInString(text[i:])
			if size == 0 {
				break
			}
			io.WriteString(out, text[i:i+size])
			column += source.DisplayWidth(r)
			i += size
		}
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

func writeMarker(out io.Writer, text string, lineStart int, start int, end int, lineEnd int, token string, styled bool) {
	column := 1
	for i := lineStart; i < start; {
		if text[i] == '\t' {
			for n := 0; n < 8-(column-1)%8; n++ {
				io.WriteString(out, " ")
			}
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
	head, unit, tail := "^", "~", "~"
	if diagnosticFormat == "human" {
		head, unit, tail = "╰", "─", "╯"
		if width == 1 {
			head = "╵"
		}
	}
	length := len(head) + (width-1)*len(unit)
	marker := mem.AllocSlice[byte](mem.System, length, length)
	copy(marker, []byte(head))
	for i := len(head); i < len(marker); i += len(unit) {
		copy(marker[i:], []byte(unit))
	}
	if width > 1 {
		copy(marker[len(marker)-len(tail):], []byte(tail))
	}
	cli.Style(out, token, string(marker), styled)
	mem.FreeSlice(mem.System, marker)
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
