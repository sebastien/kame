// Package posix runs shell scripts on hosted POSIX systems.
package posix

import (
	"kame/host"
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
	"solod.dev/so/time"
)

//so:embed posix.h
var posix_h string

//so:embed posix.c
var posix_c string

// These aliases preserve the POSIX package's public API while the portable
// process contract lives in host.
type Outcome = host.ProcessOutcome
type EventKind = host.ProcessEventKind
type DiagnosticCode = string
type Diagnostic = host.ProcessDiagnostic
type Request = host.ProcessRequest
type Event = host.ProcessEvent
type EventResult = host.ProcessEventResult

const (
	Exited                = host.ProcessExited
	TimedOut              = host.ProcessTimedOut
	Cancelled             = host.ProcessCancelled
	Failed                = host.ProcessFailed
	Started               = host.ProcessStarted
	Stdout                = host.ProcessStdout
	Stderr                = host.ProcessStderr
	Terminal              = host.ProcessTerminal
	DiagnosticHostFailure = "HOST_FAIL"
)

//so:extern km_host
type nativeHost struct{}

//so:extern km_event
type nativeEvent struct {
	kind              c.Int
	id                int64
	pid               int64
	pgid              int64
	outcome           c.Int
	status            c.Int
	signal            c.Int
	data              *byte
	dataLen           c.Int
	diagnosticCode    *byte
	diagnosticCodeLen c.Int
	diagnostic        *byte
	diagnosticLen     c.Int
	output            *byte
	stdoutLen         c.Int
	errorOutput       *byte
	stderrLen         c.Int
	stdoutTruncated   bool
	stderrTruncated   bool
	retainBytes       c.Int
	stageData         *c.Int
	stageCount        c.Int
}

//so:extern
func km_host_new() *nativeHost { return nil }

//so:extern nodecay
func km_file_modtime(name string, link bool) int64 { _, _ = name, link; return 0 }

//so:extern nodecay
func km_host_start(h *nativeHost, id int64, shell []string, script []byte, directory string, environment []string, timeoutMS int64, retainBytes int, direct bool) c.Int {
	_, _, _, _, _, _, _, _, _ = h, id, shell, script, directory, environment, timeoutMS, retainBytes, direct
	return 0
}

//so:extern nodecay
func km_host_start_graph(h *nativeHost, id int64, arguments []string, lengths []int, directories []string, environment []string, environmentLengths []int, timeouts []int64, timeoutMS int64, retainBytes int, input string, output string, appendOutput bool) c.Int {
	_, _, _, _, _, _, _, _, _, _, _, _, _ = h, id, arguments, lengths, directories, environment, environmentLengths, timeouts, timeoutMS, retainBytes, input, output, appendOutput
	return 0
}

//so:extern
func km_event_stage_field(event *nativeEvent, index c.Int, field c.Int) c.Int {
	_, _, _ = event, index, field
	return 0
}

//so:extern
func km_host_pump(h *nativeHost, waitMS c.Int) c.Int { _, _ = h, waitMS; return 0 }

//so:extern
func km_host_next(h *nativeHost, event *nativeEvent) bool { _, _ = h, event; return false }

//so:extern
func km_host_cancel(h *nativeHost, id int64, timeout bool) c.Int { _, _, _ = h, id, timeout; return 0 }

//so:extern
func km_host_stop(h *nativeHost, id int64, graceMS int64) c.Int { _, _, _ = h, id, graceMS; return 0 }

//so:extern
func km_host_cancel_all(h *nativeHost) { _ = h }

//so:extern
func km_host_active(h *nativeHost) c.Int { _ = h; return 0 }

//so:extern nodecay
func km_host_cache_lock(h *nativeHost, path string, stripe c.Int) c.Int {
	_, _, _ = h, path, stripe
	return -1
}

//so:extern
func km_host_cache_unlock(h *nativeHost, stripe c.Int) { _, _ = h, stripe }

//so:extern
func km_host_force_waitpid_failure(h *nativeHost) { _ = h }

//so:extern
func km_event_free(event *nativeEvent) { _ = event }

//so:extern
func km_host_free(h *nativeHost) { _ = h }

//so:extern
func km_cli_install_signals() c.Int { return 0 }

//so:extern
func km_cli_take_signal() c.Int { return 0 }

//so:extern
func km_cli_stderr_is_terminal() c.Int { return 0 }

//so:extern
func km_cli_stderr_width() c.Int { return 0 }

//so:extern
func km_cli_environment_size() c.Int { return 0 }

//so:extern nodecay
func km_cli_environment_copy(out []byte) c.Int { _ = out; return 0 }

type Host struct {
	Alloc  mem.Allocator
	native *nativeHost
	// start anchors the monotonic clock at host creation.
	start time.Time
}

func New(a mem.Allocator) *Host {
	h := mem.Alloc[Host](a)
	h.Alloc, h.native = a, km_host_new()
	h.start = time.Now()
	return h
}

// Start queues a failed terminal event when the host rejects the request. A
// nil host cannot queue anything and returns false without an event.
func (h *Host) Start(request host.ProcessRequest) bool {
	if request.Directory == "" {
		request.Directory = "."
	}
	if h == nil || h.native == nil {
		return false
	}
	if len(request.Stages) != 0 {
		var arguments []string
		var lengths []int
		var directories, environment []string
		var environmentLengths []int
		var timeouts []int64
		for i := range request.Stages {
			lengths = slices.Append(h.Alloc, lengths, len(request.Stages[i].Argv))
			for j := range request.Stages[i].Argv {
				arguments = slices.Append(h.Alloc, arguments, request.Stages[i].Argv[j])
			}
			directory := request.Stages[i].Directory
			if directory == "" {
				directory = request.Directory
			}
			directories = slices.Append(h.Alloc, directories, directory)
			timeouts = slices.Append(h.Alloc, timeouts, request.Stages[i].TimeoutMS)
			start := len(environment)
			for j := range request.Environment {
				entry := request.Environment[j]
				overridden := false
				for k := range request.Stages[i].Environment {
					replacement := request.Stages[i].Environment[k]
					at := strings.IndexByte(replacement, '=')
					if at > 0 && strings.HasPrefix(entry, replacement[:at+1]) {
						overridden = true
					}
				}
				if !overridden {
					environment = slices.Append(h.Alloc, environment, entry)
				}
			}
			for j := range request.Stages[i].Environment {
				environment = slices.Append(h.Alloc, environment, request.Stages[i].Environment[j])
			}
			environmentLengths = slices.Append(h.Alloc, environmentLengths, len(environment)-start)
		}
		ok := km_host_start_graph(h.native, request.ID, arguments, lengths, directories, environment, environmentLengths, timeouts, request.TimeoutMS, request.RetainBytes, request.Input, request.Output, request.Append) == 0
		slices.Free(h.Alloc, arguments)
		slices.Free(h.Alloc, lengths)
		slices.Free(h.Alloc, directories)
		slices.Free(h.Alloc, environment)
		slices.Free(h.Alloc, environmentLengths)
		slices.Free(h.Alloc, timeouts)
		return ok
	}
	argv, direct := request.Shell, false
	if len(request.Argv) != 0 {
		argv, direct = request.Argv, true
	}
	return km_host_start(h.native, request.ID, argv, request.Script, request.Directory, request.Environment, request.TimeoutMS, request.RetainBytes, direct) == 0
}

// Pump waits for at most waitMS milliseconds, reads available process output,
// and advances timeout and cancellation state. Negative waits are treated as 0.
func (h *Host) Pump(waitMS int) bool {
	if h == nil || h.native == nil {
		return false
	}
	if waitMS < 0 {
		waitMS = 0
	}
	return km_host_pump(h.native, c.Int(waitMS)) == 0
}

func cloneBytes(a mem.Allocator, ptr *byte, n c.Int) []byte {
	if ptr == nil || n <= 0 {
		return nil
	}
	return slices.Clone(a, c.Bytes(ptr, int(n)))
}

func cloneString(a mem.Allocator, ptr *byte, n c.Int) string {
	if ptr == nil || n <= 0 {
		return ""
	}
	// string wraps the clone backing (zero-copy in Solod): ownership
	// transfers to the string, so the intermediate slice is never freed.
	return string(cloneBytes(a, ptr, n))
}

func cloneDiagnostic(a mem.Allocator, code *byte, codeLen c.Int, message *byte, messageLen c.Int) host.ProcessDiagnostic {
	if code == nil || codeLen <= 0 {
		return Diagnostic{}
	}
	return Diagnostic{Code: cloneString(a, code, codeLen), Message: cloneString(a, message, messageLen)}
}

// Next returns the next owned event, if any.
func (h *Host) Next() host.ProcessEventResult {
	if h == nil || h.native == nil {
		return EventResult{}
	}
	var raw nativeEvent
	if !km_host_next(h.native, &raw) {
		return EventResult{}
	}
	e := host.ProcessEvent{Kind: host.ProcessEventKind(raw.kind), ID: raw.id, PID: raw.pid, PGID: raw.pgid, Outcome: host.ProcessOutcome(raw.outcome), Status: int(raw.status), Signal: int(raw.signal), Data: cloneBytes(h.Alloc, raw.data, raw.dataLen), Diagnostic: cloneDiagnostic(h.Alloc, raw.diagnosticCode, raw.diagnosticCodeLen, raw.diagnostic, raw.diagnosticLen), Stdout: cloneBytes(h.Alloc, raw.output, raw.stdoutLen), Stderr: cloneBytes(h.Alloc, raw.errorOutput, raw.stderrLen), StdoutTruncated: raw.stdoutTruncated, StderrTruncated: raw.stderrTruncated, RetainBytes: int(raw.retainBytes)}
	for i := 0; i < int(raw.stageCount); i++ {
		e.Stages = slices.Append(h.Alloc, e.Stages, host.ProcessStageResult{Status: int(km_event_stage_field(&raw, c.Int(i), 0)), Signal: int(km_event_stage_field(&raw, c.Int(i), 1)), Outcome: host.ProcessOutcome(km_event_stage_field(&raw, c.Int(i), 2))})
	}
	km_event_free(&raw)
	return host.ProcessEventResult{Event: e, OK: true}
}

// Cancel requests process-group termination. Repeating it is harmless.
func (h *Host) Cancel(id int64) bool {
	return h != nil && h.native != nil && km_host_cancel(h.native, id, false) == 0
}

// Stop sends SIGTERM and escalates to SIGKILL after graceMS milliseconds.
func (h *Host) Stop(id int64, graceMS int64) bool {
	return h != nil && h.native != nil && km_host_stop(h.native, id, graceMS) == 0
}

// CancelAll requests termination for every active process group.
func (h *Host) CancelAll() {
	if h != nil && h.native != nil {
		km_host_cancel_all(h.native)
	}
}

func (h *Host) Active() int {
	if h == nil || h.native == nil {
		return 0
	}
	return int(km_host_active(h.native))
}

// InstallSignals installs async-safe SIGINT and SIGTERM notifications for the
// command loop. The handler itself never touches Go/Solod state.
func InstallSignals() bool { return km_cli_install_signals() == 0 }

// TakeSignal returns zero when no signal arrived, a positive signal number for
// the first signal, or a negative number when a second signal arrived.
func TakeSignal() int { return int(km_cli_take_signal()) }

// StderrIsTerminal reports whether the diagnostic stream is attached to a TTY.
func StderrIsTerminal() bool { return km_cli_stderr_is_terminal() != 0 }

// StderrWidth returns terminal columns, or zero when the stream has no known
// width (including redirected output).
func StderrWidth() int { return int(km_cli_stderr_width()) }

// Environment returns a complete owned snapshot of the process environment.
func Environment(a mem.Allocator) []string {
	size := int(km_cli_environment_size())
	if size <= 0 {
		return nil
	}
	data := mem.AllocSlice[byte](a, size, size)
	written := int(km_cli_environment_copy(data))
	if written < 0 {
		mem.FreeSlice(a, data)
		return nil
	}
	var values []string
	start := 0
	for i := 0; i < written; i++ {
		if data[i] != 0 {
			continue
		}
		if i > start {
			values = slices.Append(a, values, string(slices.Clone(a, data[start:i])))
		}
		start = i + 1
	}
	mem.FreeSlice(a, data)
	return values
}

func FreeEnvironment(a mem.Allocator, values []string) {
	for i := range values {
		mem.FreeString(a, values[i])
	}
	slices.Free(a, values)
}

// ForceWaitpidFailureForTest makes the next process reaping attempt fail. It is
// exported because Solod tests are a separate package that can only reach the
// exported API; it stays the sole test-only seam in this package.
func (h *Host) ForceWaitpidFailureForTest() {
	if h != nil && h.native != nil {
		km_host_force_waitpid_failure(h.native)
	}
}

// Free terminates active process groups and releases the host. Call it exactly
// once: the handle's own memory is released, so it cannot be reused.
func (h *Host) Free() {
	if h == nil {
		return
	}
	if h.native != nil {
		km_host_free(h.native)
		h.native = nil
	}
	mem.Free(h.Alloc, h)
}
