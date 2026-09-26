package host

import (
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// ProcessOutcome describes a terminal process result.
type ProcessOutcome int

const (
	ProcessExited ProcessOutcome = iota
	ProcessTimedOut
	ProcessCancelled
	ProcessFailed
)

// ProcessEventKind identifies an event emitted by a ProcessHost.
type ProcessEventKind int

const (
	ProcessStarted ProcessEventKind = iota
	ProcessStdout
	ProcessStderr
	ProcessTerminal
)

// ProcessDiagnostic carries a stable host diagnostic code and its owned message.
type ProcessDiagnostic struct {
	Code    string
	Message string
}

// ProcessRequest remains owned by its caller until the matching terminal event.
// Shell contains the executable followed by its arguments; Script is appended as
// its final argument.
type ProcessRequest struct {
	ID          int64
	Shell       []string
	Script      []byte
	Directory   string
	Environment []string
	TimeoutMS   int64
	RetainBytes int
}

// ProcessEvent owns Data, Stdout, Stderr, and Diagnostic and must be released
// with Free after its receiver has consumed or transferred them.
type ProcessEvent struct {
	Kind            ProcessEventKind
	ID              int64
	PID             int64
	PGID            int64
	Outcome         ProcessOutcome
	Status          int
	Signal          int
	Data            []byte
	Diagnostic      ProcessDiagnostic
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

func (e *ProcessEvent) Free(a mem.Allocator) {
	if len(e.Data) != 0 {
		slices.Free(a, e.Data)
	}
	if len(e.Stdout) != 0 {
		slices.Free(a, e.Stdout)
	}
	if len(e.Stderr) != 0 {
		slices.Free(a, e.Stderr)
	}
	if e.Diagnostic.Code != "" {
		mem.FreeString(a, e.Diagnostic.Code)
	}
	if e.Diagnostic.Message != "" {
		mem.FreeString(a, e.Diagnostic.Message)
	}
	*e = ProcessEvent{}
}

type ProcessEventResult struct {
	Event ProcessEvent
	OK    bool
}

// ProcessHost is the portable runtime contract for executing complete recipe
// scripts. Submitted request storage remains caller-owned until a terminal
// event is received.
type ProcessHost interface {
	Start(ProcessRequest) bool
	Pump(waitMS int) bool
	Next() ProcessEventResult
	Cancel(id int64) bool
	CancelAll()
	Active() int
	Free()
}
