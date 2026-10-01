// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/time"
	"solod.dev/so/unicode/utf8"
)

// Presentation state is initialized once per Run. It affects rendering only;
// diagnostic data and JSON output remain independent of terminal capabilities.
var diagnosticColor = "never"
var requestedDiagnosticColor = "auto"
var requestedDiagnosticFormat = "plain"
var diagnosticFormat = "plain"
var diagnosticWidth = 80

func configureDiagnosticPresentation(options buildArguments) {
	color, format := requestedDiagnosticColor, requestedDiagnosticFormat
	if options.Color != "" {
		color = options.Color
	}
	if options.DiagnosticFormat != "" {
		format = options.DiagnosticFormat
	}
	diagnosticFormat = format
	environment := posix.Environment(mem.System)
	diagnosticColor = resolveDiagnosticColor(color, format, posix.StderrIsTerminal(), environment)
	posix.FreeEnvironment(mem.System, environment)
	diagnosticWidth = posix.StderrWidth()
	if diagnosticWidth < 20 {
		diagnosticWidth = 80
	}
}

func setDiagnosticColor(color string) {
	requestedDiagnosticColor = color
}

func setDiagnosticFormat(format string) { requestedDiagnosticFormat = format }

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

func materializeTargets(p *program.Program, targets []string, out io.Writer, errOut io.Writer, json bool) int {
	startedAt := time.Now().UnixNano()
	var handles []*program.Handle
	failed := false
	progress := buildProgress{}
	for i := range targets {
		started := p.Start(targets[i])
		if started.Diagnostic.Code != "" {
			annotateTargetDiagnostic(&started.Diagnostic, targets[i])
			emitDiagnostic(diagnosticWriter(out, errOut, json), started.Diagnostic, json, p.Parsed.Source)
			started.Diagnostic.Free(mem.System)
			failed = true
			continue
		}
		handles = slices.Append(mem.System, handles, started.Handle)
	}
	remaining := len(handles)
	cancelling := false
	for remaining != 0 {
		signal := posix.TakeSignal()
		if signal < 0 {
			for i := range handles {
				if handles[i] != nil {
					handles[i].Free()
				}
			}
			slices.Free(mem.System, handles)
			return 128 - signal
		}
		if signal > 0 && !cancelling {
			cancelling = true
			for i := range handles {
				if handles[i] != nil {
					handles[i].Cancel()
				}
			}
		}
		p.Tick(10)
		drainEvents(p, out, errOut, json, &progress)
		for i := range handles {
			if handles[i] == nil {
				continue
			}
			// Definitions are reactive nodes: publishing their first value leaves
			// the node open for future invalidations. A CLI target request consumes
			// that first value rather than waiting for a terminal state.
			if handles[i].Definition && handles[i].Node.Current {
				writeValue(out, handles[i].Node.Latest)
				handles[i].Free()
				handles[i] = nil
				remaining--
				continue
			}
			polled := handles[i].Poll()
			if !polled.Done {
				continue
			}
			if polled.Result.Diagnostic.Code != "" {
				annotateTargetDiagnostic(&polled.Result.Diagnostic, targets[i])
				emitDiagnostic(diagnosticWriter(out, errOut, json), polled.Result.Diagnostic, json, p.Parsed.Source)
				failed = true
			} else if polled.Result.Value.Kind != core.Nil {
				writeValue(out, polled.Result.Value)
			}
			polled.Result.Free(mem.System)
			handles[i].Free()
			handles[i] = nil
			remaining--
		}
	}
	drainEvents(p, out, errOut, json, &progress)
	slices.Free(mem.System, handles)
	// A second signal may arrive while the first cancellation reaps the final
	// process. Consume it before returning the ordinary cancellation status.
	if cancelling {
		signal := posix.TakeSignal()
		if signal < 0 {
			return 128 - signal
		}
	}
	if !json && progress.Completed+progress.Failed != 0 {
		elapsedMS := (time.Now().UnixNano() - startedAt) / 1000000
		if progress.Failed == 0 {
			fmt.Fprintf(errOut, "Summary: %d %s complete in %d.%03ds\n", progress.Completed, targetWord(progress.Completed), elapsedMS/1000, elapsedMS%1000)
		} else {
			fmt.Fprintf(errOut, "Summary: %d complete, %d failed in %d.%03ds\n", progress.Completed, progress.Failed, elapsedMS/1000, elapsedMS%1000)
		}
	}
	if failed || cancelling {
		return 1
	}
	return 0
}

func targetWord(count int) string {
	if count == 1 {
		return "target"
	}
	return "targets"
}

type buildProgress struct {
	Active    int
	Completed int
	Failed    int
}

func drainEvents(p *program.Program, out io.Writer, errOut io.Writer, json bool, progress *buildProgress) {
	for {
		next := p.NextEvent()
		if !next.OK {
			return
		}
		event := next.Event
		if json {
			writeJSONEvent(out, event)
		} else if event.Kind == program.Stdout {
			out.Write(event.Data)
		} else if event.Kind == program.Stderr {
			errOut.Write(event.Data)
		} else if event.Kind == program.TargetStarted {
			progress.Active++
			fmt.Fprintf(errOut, "[%s] started (%d active, %d complete)\n", event.Target, progress.Active, progress.Completed)
		} else if event.Kind == program.TargetCompleted {
			if progress.Active != 0 {
				progress.Active--
			}
			progress.Completed++
			fmt.Fprintf(errOut, "[%s] complete (%d active, %d complete)\n", event.Target, progress.Active, progress.Completed)
		} else if event.Kind == program.TargetFailed || event.Kind == program.TargetCancelled {
			if progress.Active != 0 {
				progress.Active--
			}
			progress.Failed++
			fmt.Fprintf(errOut, "[%s] failed (%d active, %d complete)\n", event.Target, progress.Active, progress.Completed)
		} else if event.Kind == program.CacheWarning {
			fmt.Fprintf(errOut, "warning %s: %s\n", event.Diagnostic.Code, event.Diagnostic.Message)
		}
		event.Free(mem.System)
	}
}

func emitDiagnostic(out io.Writer, d diagnostic.Diagnostic, json bool, src *source.Source) {
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
	if diagnosticColor == "always" {
		if d.Severity == diagnostic.Warning {
			io.WriteString(out, "\x1b[33m")
		} else {
			io.WriteString(out, "\x1b[31m")
		}
	}
	if d.Target != "" {
		if diagnosticFormat == "human" {
			fmt.Fprintf(out, "✗ %s failed · %s\n", d.Target, d.Code)
		} else {
			fmt.Fprintf(out, "%s failed: %s\n", d.Target, d.Code)
		}
		if len(d.TargetStack) > 1 {
			io.WriteString(out, "  required by ")
			for i := range d.TargetStack {
				if i != 0 {
					if diagnosticFormat == "human" {
						io.WriteString(out, " → ")
					} else {
						io.WriteString(out, " -> ")
					}
				}
				io.WriteString(out, d.TargetStack[i])
			}
			io.WriteString(out, "\n")
		}
	}
	if d.Source != "" && renderSource != nil && d.Source == renderSource.Name {
		position := renderSource.Position(d.Span.Start)
		fmt.Fprintf(out, "%s:%d:%d: %s %s: %s\n", d.Source, position.Line, position.Column, diagnosticSeverity(d.Severity), d.Code, d.Message)
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
		fmt.Fprintf(out, "%s: %s %s: %s\n", d.Source, diagnosticSeverity(d.Severity), d.Code, d.Message)
	} else {
		fmt.Fprintf(out, "%s %s: %s\n", diagnosticSeverity(d.Severity), d.Code, d.Message)
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
	if diagnosticColor == "always" {
		io.WriteString(out, "\x1b[0m")
	}
	if loaded {
		renderSource.Free(mem.System)
	}
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

func writeValue(out io.Writer, value core.Value) {
	if value.Kind == core.String || value.Kind == core.Pattern {
		io.WriteString(out, value.Text)
		return
	}
	if value.Kind == core.Bytes {
		out.Write(value.Bytes)
		return
	}
	if value.Kind == core.Bool {
		if value.Bool {
			io.WriteString(out, "true")
		} else {
			io.WriteString(out, "false")
		}
		return
	}
	if value.Kind == core.Int {
		fmt.Fprintf(out, "%d", value.Int)
		return
	}
	if value.Kind == core.Float {
		fmt.Fprintf(out, "%g", value.Float)
		return
	}
	if value.Kind == core.Nil {
		io.WriteString(out, "nil")
		return
	}
	if value.Kind == core.Resource {
		io.WriteString(out, value.Resource.Name)
		return
	}
	if value.Kind == core.List {
		io.WriteString(out, "[")
		for i := range value.List {
			if i != 0 {
				io.WriteString(out, " ")
			}
			writeValue(out, value.List[i])
		}
		io.WriteString(out, "]")
		return
	}
	if value.Kind == core.Record {
		io.WriteString(out, "[")
		for i := range value.Record {
			if i != 0 {
				io.WriteString(out, " ")
			}
			io.WriteString(out, value.Record[i].Key)
			io.WriteString(out, ": ")
			writeValue(out, value.Record[i].Value)
		}
		io.WriteString(out, "]")
	}
}
