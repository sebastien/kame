// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/cli"
	"kame/core"
	"kame/host/posix"
	"kame/lang/eval"
	"kame/program"
	"solod.dev/so/bytes"
	"solod.dev/so/fmt"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
)

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
