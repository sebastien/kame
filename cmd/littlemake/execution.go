// Command littlemake provides the native LittleMake command-line interface.
package main

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/host/posix"
	"littlemake/lang/source"
	"littlemake/program"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func materializeTargets(p *program.Program, targets []string, out io.Writer, errOut io.Writer, json bool) int {
	var handles []*program.Handle
	failed := false
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
		drainEvents(p, out, errOut, json)
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
	drainEvents(p, out, errOut, json)
	slices.Free(mem.System, handles)
	// A second signal may arrive while the first cancellation reaps the final
	// process. Consume it before returning the ordinary cancellation status.
	if cancelling { signal := posix.TakeSignal(); if signal < 0 { return 128 - signal } }
	if failed || cancelling {
		return 1
	}
	return 0
}

func drainEvents(p *program.Program, out io.Writer, errOut io.Writer, json bool) {
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
			fmt.Fprintf(errOut, "[%s] started\n", event.Target)
		} else if event.Kind == program.TargetCompleted {
			fmt.Fprintf(errOut, "[%s] complete\n", event.Target)
		} else if event.Kind == program.TargetFailed || event.Kind == program.TargetCancelled {
			fmt.Fprintf(errOut, "[%s] failed\n", event.Target)
		} else if event.Kind == program.CacheWarning {
			fmt.Fprintf(errOut, "warning %s: %s\n", event.Diagnostic.Code, event.Diagnostic.Message)
		}
		event.Free(mem.System)
	}
}

func emitDiagnostic(out io.Writer, d diagnostic.Diagnostic, json bool, src *source.Source) {
	if json {
		writeJSONDiagnostic(out, d)
		return
	}
	cliDiagnosticWithSource(out, d, src)
}

// diagnosticWriter picks the stream for command diagnostics: JSON events are
// stdout-only by specification; human diagnostics stay on stderr.
func diagnosticWriter(out io.Writer, errOut io.Writer, json bool) io.Writer {
	if json {
		return out
	}
	return errOut
}

func cliDiagnosticWithSource(out io.Writer, d diagnostic.Diagnostic, src *source.Source) {
	hasSourceSpan := d.Span.End > d.Span.Start || d.Code == "PARSE_ERR"
	name := d.Source
	if name == "" { name = d.Target }
	if name == "" && src != nil && hasSourceSpan { name = src.Name }
	if name != "" && src != nil && hasSourceSpan {
		position := src.Position(d.Span.Start)
		fmt.Fprintf(out, "%s:%d:%d: %s %s: %s\n", name, position.Line, position.Column, diagnosticSeverity(d.Severity), d.Code, d.Message)
		start := d.Span.Start; if start < 0 { start = 0 }; if start > len(src.Text) { start = len(src.Text) }
		lineStart := start; for lineStart > 0 && src.Text[lineStart-1] != '\n' { lineStart-- }
		lineEnd := start; for lineEnd < len(src.Text) && src.Text[lineEnd] != '\n' && src.Text[lineEnd] != '\r' { lineEnd++ }
		io.WriteString(out, src.Text[lineStart:lineEnd]); io.WriteString(out, "\n")
		for i := lineStart; i < start; i++ { if src.Text[i] == '\t' { io.WriteString(out, "\t") } else { io.WriteString(out, " ") } }
		io.WriteString(out, "^\n")
		for i := range d.Notes { io.WriteString(out, "note: "); io.WriteString(out, d.Notes[i]); io.WriteString(out, "\n") }
		return
	}
	fmt.Fprintf(out, "<command>:1:1: %s %s: %s\n", diagnosticSeverity(d.Severity), d.Code, d.Message)
	for i := range d.Notes { io.WriteString(out, "note: "); io.WriteString(out, d.Notes[i]); io.WriteString(out, "\n") }
}

func diagnosticSeverity(severity diagnostic.Severity) string { if severity == diagnostic.Warning { return "warning" }; if severity == diagnostic.Fatal { return "fatal" }; return "error" }

// annotateTargetDiagnostic appends deterministic suggestions to unknown-target
// diagnostics. Path-like targets written without an explicit ./ prefix suggest
// the explicit form that file rules require.
func annotateTargetDiagnostic(d *diagnostic.Diagnostic, target string) {
	if d.Code != "TGT_NO_RULE" || len(target) == 0 {
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
