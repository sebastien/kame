// Package posix runs shell scripts on hosted POSIX systems.
package posix

import (
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

//so:embed posix.h
var posix_h string

//so:embed posix.c
var posix_c string

// Outcome and EventKind values must match the lm_event enum in posix.c.
type Outcome int

const (
	Exited Outcome = iota
	TimedOut
	Cancelled
	Failed
)

type EventKind int

const (
	Started EventKind = iota
	Stdout
	Stderr
	Terminal
)

// DiagnosticCode is a stable code from docs/spec/011-diagnostics.md.
type DiagnosticCode string

const (
	DiagnosticHostFailure DiagnosticCode = "LM-HOSTF"
)

// Diagnostic carries a host failure code and its owned message.
type Diagnostic struct {
	Code    DiagnosticCode
	Message string
}

// Request remains owned by its caller until its terminal event. Shell contains
// the executable followed by its arguments; Script is appended as its final
// argument.
type Request struct {
	ID          int64
	Shell       []string
	Script      []byte
	Directory   string
	Environment []string // Complete KEY=VALUE entries; it is never inherited.
	TimeoutMS   int64
	RetainBytes int
}

// Event owns Data, Stdout, Stderr, and Diagnostic and must be released with Free.
type Event struct {
	Kind              EventKind
	ID                int64
	PID               int64
	PGID              int64
	Outcome           Outcome
	Status            int
	Signal            int
	Data              []byte
	Diagnostic        Diagnostic
	Stdout            []byte
	Stderr            []byte
	StdoutTruncated   bool
	StderrTruncated   bool
}

type EventResult struct {
	Event Event
	OK    bool
}

func (e *Event) Free(a mem.Allocator) {
	slices.Free(a, e.Data)
	slices.Free(a, e.Stdout)
	slices.Free(a, e.Stderr)
	mem.FreeString(a, string(e.Diagnostic.Code))
	mem.FreeString(a, e.Diagnostic.Message)
	*e = Event{}
}

//so:extern lm_host
type nativeHost struct{}

//so:extern lm_event
type nativeEvent struct {
	kind      c.Int
	id        int64
	pid       int64
	pgid      int64
	outcome   c.Int
	status    c.Int
	signal    c.Int
	data      *byte
	dataLen   c.Int
	diagnosticCode *byte
	diagnosticCodeLen c.Int
	diagnostic *byte
	diagnosticLen c.Int
	output    *byte
	stdoutLen c.Int
	errorOutput *byte
	stderrLen c.Int
	stdoutTruncated bool
	stderrTruncated bool
}

//so:extern
func lm_host_new() *nativeHost { return nil }

//so:extern nodecay
func lm_host_start(h *nativeHost, id int64, shell []string, script []byte, directory string, environment []string, timeoutMS int64, retainBytes int) c.Int {
	_, _, _, _, _, _, _, _ = h, id, shell, script, directory, environment, timeoutMS, retainBytes
	return 0
}

//so:extern
func lm_host_pump(h *nativeHost, waitMS c.Int) c.Int { _, _ = h, waitMS; return 0 }

//so:extern
func lm_host_next(h *nativeHost, event *nativeEvent) bool { _, _ = h, event; return false }

//so:extern
func lm_host_cancel(h *nativeHost, id int64, timeout bool) c.Int { _, _, _ = h, id, timeout; return 0 }

//so:extern
func lm_host_cancel_all(h *nativeHost) { _ = h }

//so:extern
func lm_host_active(h *nativeHost) c.Int { _ = h; return 0 }

//so:extern
func lm_host_force_waitpid_failure(h *nativeHost) { _ = h }

//so:extern
func lm_event_free(event *nativeEvent) { _ = event }

//so:extern
func lm_host_free(h *nativeHost) { _ = h }

type Host struct {
	Alloc mem.Allocator
	native *nativeHost
}

func New(a mem.Allocator) *Host {
	h := mem.Alloc[Host](a)
	h.Alloc, h.native = a, lm_host_new()
	return h
}

// Start queues a failed terminal event when the host rejects the request. A
// nil host cannot queue anything and returns false without an event.
func (h *Host) Start(request Request) bool {
	if h == nil || h.native == nil { return false }
	return lm_host_start(h.native, request.ID, request.Shell, request.Script, request.Directory, request.Environment, request.TimeoutMS, request.RetainBytes) == 0
}

// Pump waits for at most waitMS milliseconds, reads available process output,
// and advances timeout and cancellation state. Negative waits are treated as 0.
func (h *Host) Pump(waitMS int) bool {
	if h == nil || h.native == nil { return false }
	if waitMS < 0 { waitMS = 0 }
	return lm_host_pump(h.native, c.Int(waitMS)) == 0
}

func cloneBytes(a mem.Allocator, ptr *byte, n c.Int) []byte {
	if ptr == nil || n <= 0 { return nil }
	return slices.Clone(a, c.Bytes(ptr, int(n)))
}

func cloneString(a mem.Allocator, ptr *byte, n c.Int) string {
	if ptr == nil || n <= 0 { return "" }
	return string(cloneBytes(a, ptr, n))
}

func cloneDiagnostic(a mem.Allocator, code *byte, codeLen c.Int, message *byte, messageLen c.Int) Diagnostic {
	if code == nil || codeLen <= 0 { return Diagnostic{} }
	return Diagnostic{Code: DiagnosticCode(cloneString(a, code, codeLen)), Message: cloneString(a, message, messageLen)}
}

// Next returns the next owned event, if any.
func (h *Host) Next() EventResult {
	if h == nil || h.native == nil { return EventResult{} }
	var raw nativeEvent
	if !lm_host_next(h.native, &raw) { return EventResult{} }
	e := Event{Kind: EventKind(raw.kind), ID: raw.id, PID: raw.pid, PGID: raw.pgid, Outcome: Outcome(raw.outcome), Status: int(raw.status), Signal: int(raw.signal), Data: cloneBytes(h.Alloc, raw.data, raw.dataLen), Diagnostic: cloneDiagnostic(h.Alloc, raw.diagnosticCode, raw.diagnosticCodeLen, raw.diagnostic, raw.diagnosticLen), Stdout: cloneBytes(h.Alloc, raw.output, raw.stdoutLen), Stderr: cloneBytes(h.Alloc, raw.errorOutput, raw.stderrLen), StdoutTruncated: raw.stdoutTruncated, StderrTruncated: raw.stderrTruncated}
	lm_event_free(&raw)
	return EventResult{Event: e, OK: true}
}

// Cancel requests process-group termination. Repeating it is harmless.
func (h *Host) Cancel(id int64) bool {
	return h != nil && h.native != nil && lm_host_cancel(h.native, id, false) == 0
}

// CancelAll requests termination for every active process group.
func (h *Host) CancelAll() {
	if h != nil && h.native != nil { lm_host_cancel_all(h.native) }
}

func (h *Host) Active() int {
	if h == nil || h.native == nil { return 0 }
	return int(lm_host_active(h.native))
}

// ForceWaitpidFailureForTest makes the next process reaping attempt fail. It is
// exported because Solod tests are a separate package that can only reach the
// exported API; it stays the sole test-only seam in this package.
func (h *Host) ForceWaitpidFailureForTest() {
	if h != nil && h.native != nil { lm_host_force_waitpid_failure(h.native) }
}

// Free terminates active process groups and releases the host. Call it exactly
// once: the handle's own memory is released, so it cannot be reused.
func (h *Host) Free() {
	if h == nil { return }
	if h.native != nil { lm_host_free(h.native); h.native = nil }
	mem.Free(h.Alloc, h)
}
