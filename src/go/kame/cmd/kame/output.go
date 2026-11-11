package main

import (
	"io"
	"kame/host/posix"
	"kame/program"
	"os"
)

// Like presentation state, output delivery is single-owner for one Run. Custom
// embedding writers remain untouched; only the actual public descriptors use
// this adapter. All their writes bypass stdio, so no deferred fflush can block.
type nativeOutputState struct {
	Program     *program.Program
	DeadlineNS  int64
	FailureCode string
	Bounded     bool
	Signal      int
}

type nativeOutputWriter struct {
	State    *nativeOutputState
	FD       int
	Failed   bool
	TimedOut bool
}

var activeNativeOutput *nativeOutputState

func nativeOutputFD(writer io.Writer) int {
	var stdout io.Writer = os.Stdout
	var stderr io.Writer = os.Stderr
	if writer == stdout {
		return 1
	}
	if writer == stderr {
		return 2
	}
	return 0
}

func restoreNativeOutput(previous *nativeOutputState) { activeNativeOutput = previous }

func restoreDiagnosticOut(previous io.Writer) { cliDiagnosticOut = previous }

func (w *nativeOutputWriter) Write(data []byte) (int, error) {
	if w.Failed {
		if w.TimedOut {
			return 0, posix.ErrOutputTimeout
		}
		return 0, posix.ErrOutputWrite
	}
	remaining := int64(0)
	if w.State.DeadlineNS != 0 && w.State.Program != nil {
		w.State.Bounded = true
		remaining = w.State.DeadlineNS - w.State.Program.Host.Monotonic()
		if remaining <= 0 {
			remaining = -1
		}
	}
	n, err := posix.WriteOutput(w.FD, data, remaining)
	if remaining != 0 {
		if signal := posix.TakeSignal(); signal != 0 {
			w.State.Signal = signal
			w.State.Program.Eval.FinalizerOutputCancelled(w.State.DeadlineNS)
		}
	}
	if err != nil && remaining != 0 {
		w.Failed, w.TimedOut = true, err == posix.ErrOutputTimeout
		if w.TimedOut {
			w.State.FailureCode = "FINALIZE_TIMEOUT"
		} else if w.State.FailureCode == "" {
			w.State.FailureCode = "HOST_FAIL"
		}
	}
	return n, err
}

func setNativeOutputDeadline(p *program.Program, deadline int64) nativeOutputState {
	if activeNativeOutput == nil {
		return nativeOutputState{}
	}
	previous := *activeNativeOutput
	activeNativeOutput.Program, activeNativeOutput.DeadlineNS = p, deadline
	return previous
}

func restoreNativeOutputDeadline(previous nativeOutputState) {
	if activeNativeOutput == nil {
		return
	}
	activeNativeOutput.Program, activeNativeOutput.DeadlineNS = previous.Program, previous.DeadlineNS
}

func nativeOutputFailure() string {
	if activeNativeOutput == nil {
		return ""
	}
	return activeNativeOutput.FailureCode
}

func nativeOutputSignal() int {
	if activeNativeOutput == nil {
		return 0
	}
	return activeNativeOutput.Signal
}
