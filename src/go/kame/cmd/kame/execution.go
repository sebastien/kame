// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/bytes"
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

func terminalDashboardAvailable() bool {
	environment := posix.Environment(mem.System)
	dumb := environmentValue(environment, "TERM") == "dumb"
	posix.FreeEnvironment(mem.System, environment)
	return posix.StderrIsTerminal() && !dumb && posix.StderrWidth() >= 40 && posix.StderrHeight() >= 4
}

func dashboardDimensions() (int, int) { return posix.StderrWidth(), posix.StderrHeight() }

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
	if len(targets) != 0 {
		setDashboardSubject(targets[0])
	}
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
		tickCLIProgram(p, 10, errOut, json)
		drainEvents(p, out, errOut, json, &progress)
		for i := range handles {
			if handles[i] == nil {
				continue
			}
			// Definitions are reactive nodes: publishing their first value leaves
			// the node open for future invalidations. A CLI target request consumes
			// that first value rather than waiting for a terminal state.
			if handles[i].Definition && handles[i].Node.Current {
				if json {
					p.ObserveDefinition(handles[i])
				} else {
					writeValue(out, handles[i].Node.Latest)
				}
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
			} else if !json && polled.Result.Value.Kind != core.Nil {
				writeValue(out, polled.Result.Value)
			}
			polled.Result.Free(mem.System)
			handles[i].Free()
			handles[i] = nil
			remaining--
		}
	}
	// A completed target may have released its final service dependency. Keep
	// pumping the host until graceful service teardown has reaped its process
	// group so terminal lifecycle events are published before the CLI returns.
	for p.Host != nil && p.Host.Active() != 0 {
		tickCLIProgram(p, 10, errOut, json)
		drainEvents(p, out, errOut, json, &progress)
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
	if failed || cancelling {
		return 1
	}
	return 0
}

type buildProgress struct {
	Active    int
	Completed int
	Failed    int
	Cancelled int
}

func drainEvents(p *program.Program, out io.Writer, errOut io.Writer, json bool, progress *buildProgress) {
	var messages = bytes.NewBuffer(mem.System, nil)
	destination := errOut
	if !json && dashboardLive {
		errOut = &messages
	}
	for {
		next := p.NextEvent()
		if !next.OK {
			redrawDashboard(errOut, progress)
			publishDashboard(destination, messages.Bytes())
			messages.Free()
			// C stdio buffers redirected streams. Publish drained events while the
			// process or watch session is still alive, including JSON records.
			flushCLIOutput(out)
			flushCLIOutput(destination)
			return
		}
		event := next.Event
		observeWorker(event)
		observeOutcome(event, progress)
		invocationCounts = *progress
		// Routine activity belongs only in the ANSI footer, even in fallback mode.
		if !json && selectedOutput == "ansi" && (event.Kind == program.ProcessStarted || event.Kind == program.ProcessExited || event.Kind == program.TargetStarted || event.Kind == program.TargetReason || event.Kind == program.ServiceState) {
			event.Free(mem.System)
			continue
		}
		if json {
			writeJSONEvent(out, event)
		} else if event.Kind == program.Stdout {
			// Preserve event ordering before an independent terminal stream writes.
			publishDashboard(destination, messages.Bytes())
			messages.Reset()
			dashboardRaw(errOut, event.Data, posix.StdoutIsTerminal())
			publishDashboard(destination, messages.Bytes())
			messages.Reset()
			out.Write(event.Data)
		} else if event.Kind == program.Stderr {
			dashboardRaw(errOut, event.Data, posix.StderrIsTerminal())
			errOut.Write(event.Data)
		} else if event.Kind == program.ProcessStarted {
			clearDashboard(errOut)
			if dashboardCanDraw() {
				event.Free(mem.System)
				continue
			}
			cli.Style(errOut, "status.running", "process ", diagnosticColor == "always")
			writeProcessStarted(errOut, event)
		} else if event.Kind == program.ProcessExited {
			if event.HasRuntime {
				clearDashboard(errOut)
				fmt.Fprintf(errOut, "process [%s] finished in %dms\n", event.Target, event.RuntimeMS)
			}
		} else if event.Kind == program.TargetStarted {
			clearDashboard(errOut)
			if dashboardCanDraw() {
				event.Free(mem.System)
				continue
			}
			fmt.Fprintf(errOut, "started [%s]\n", event.Target)
		} else if event.Kind == program.TargetCompleted {
			writeTargetOutcome(errOut, event, "done", "status.success")
		} else if event.Kind == program.TargetFailed {
			writeTargetOutcome(errOut, event, "error", "status.failed")
		} else if event.Kind == program.TargetCancelled {
			writeTargetOutcome(errOut, event, "cancelled", "status.cancelled")
		} else if event.Kind == program.CacheWarning {
			emitDiagnostic(errOut, event.Diagnostic, false, p.Parsed.Source)
		} else if event.Kind == program.ServiceState {
			clearDashboard(errOut)
			fmt.Fprintf(errOut, "info [%s] service %s (generation %d, attempt %d)\n", event.Target, event.State, event.Generation, event.Attempt)
		} else if event.Kind == program.TargetReason {
			clearDashboard(errOut)
			cli.Style(errOut, "message.info", "info ", diagnosticColor == "always")
			fmt.Fprintf(errOut, "[%s] %s: %s", event.Target, event.Decision, event.Message)
			if event.DependencyKey.Name != "" {
				fmt.Fprintf(errOut, ": %s", event.DependencyKey.Name)
			}
			io.WriteString(errOut, "\n")
		}
		event.Free(mem.System)
	}
}

func writeTargetOutcome(out io.Writer, event program.Event, label string, token string) {
	clearDashboard(out)
	if selectedOutput == "ansi" {
		status := "✓"
		var timing = bytes.NewBuffer(mem.System, nil)
		if event.Kind == program.TargetFailed {
			status = "failed"
		} else if event.Kind == program.TargetCancelled {
			status = "cancelled"
		}
		for i := range outcomes {
			if outcomes[i].Node == event.NodeID && outcomes[i].Generation == event.Generation && outcomes[i].Kind == event.Key.Kind && outcomes[i].Name == event.Key.Name && outcomes[i].HasTiming {
				writeDuration(&timing, outcomes[i].ElapsedMS)
				io.WriteString(&timing, " - ")
				break
			}
		}
		io.WriteString(&timing, status)
		width := 0
		if posix.StderrIsTerminal() {
			width = posix.StderrWidth() - 2
		}
		writeOutcomeRow(out, event.Target, timing.String(), token, width)
		timing.Free()
		if event.Diagnostic.Code != "" {
			fmt.Fprintf(out, "  %s: %s", event.Diagnostic.Code, event.Diagnostic.Message)
			if event.Diagnostic.Cause.HasStatus {
				fmt.Fprintf(out, " (status %d)", event.Diagnostic.Cause.Status)
			}
			if event.Diagnostic.Cause.HasSignal {
				fmt.Fprintf(out, " (signal %d)", event.Diagnostic.Cause.Signal)
			}
			io.WriteString(out, "\n")
		}
		return
	}
	var line = bytes.NewBuffer(mem.System, nil)
	fmt.Fprintf(&line, "%s [%s]", label, event.Target)
	if event.Kind == program.TargetCompleted {
		io.WriteString(&line, " complete")
	} else if event.Kind == program.TargetFailed {
		io.WriteString(&line, " failed")
	} else {
		io.WriteString(&line, " cancelled")
	}
	if event.Diagnostic.Code != "" {
		fmt.Fprintf(&line, " %s: %s", event.Diagnostic.Code, event.Diagnostic.Message)
		if event.Diagnostic.Cause.HasStatus {
			fmt.Fprintf(&line, " (status %d)", event.Diagnostic.Cause.Status)
		}
		if event.Diagnostic.Cause.HasSignal {
			fmt.Fprintf(&line, " (signal %d)", event.Diagnostic.Cause.Signal)
		}
	}
	cli.Style(out, token, line.String(), diagnosticColor == "always")
	io.WriteString(out, "\n")
	line.Free()
}

func writeOutcomeRow(out io.Writer, target string, status string, token string, width int) {
	statusWidth := measureDashboardText(status, len(status)*2).Cells
	var subject = bytes.NewBuffer(mem.System, nil)
	io.WriteString(&subject, "[")
	if width >= 20 {
		writeTargetField(&subject, target, width-statusWidth-4)
	} else {
		io.WriteString(&subject, target)
	}
	io.WriteString(&subject, "]")
	writeStyledSubject(out, subject.String())
	cells := measureDashboardText(subject.String(), len(subject.String())*2).Cells
	padding := width - cells - statusWidth
	if padding < 1 {
		padding = 1
	}
	for i := 0; i < padding; i++ {
		io.WriteString(out, " ")
	}
	writeStatusField(out, status, token)
	io.WriteString(out, "\n")
	subject.Free()
}

func writeProcessStarted(out io.Writer, event program.Event) {
	if event.Program == "" {
		return
	}
	fmt.Fprintf(out, "[%s] process %s", event.Target, event.Program)
	for i := range event.Argv {
		fmt.Fprintf(out, " %s", event.Argv[i])
	}
	if event.DisplayTruncated {
		io.WriteString(out, " …")
	}
	io.WriteString(out, "\n")
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

// writeValue prints a value with the shared portable display so native and
// WASM output stay byte-for-byte identical.
func writeValue(out io.Writer, value core.Value) {
	text := eval.Display(mem.System, value)
	io.WriteString(out, text)
	mem.FreeString(mem.System, text)
}

func flushCLIOutput(out io.Writer) {
	if file, ok := out.(*os.File); ok {
		_ = file.Sync()
	}
}
