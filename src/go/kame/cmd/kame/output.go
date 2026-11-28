package main

import (
	"io"
	"kame/host/posix"
	"kame/program"
	"os"
	"syscall"
	"time"
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
	Batch       bool
	RunDeadline time.Time
	RunExpired  bool
}

type nativeOutputWriter struct {
	State       *nativeOutputState
	FD          int
	Failed      bool
	TimedOut    bool
	Interrupted bool
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

// invocationDescriptors reports whether out and errOut write straight to the
// process's stdout and stderr, so children may inherit those descriptors.
func invocationDescriptors(out, errOut io.Writer) bool {
	stdout, ok := out.(*nativeOutputWriter)
	if !ok || stdout.FD != 1 {
		return false
	}
	stderr, ok := errOut.(*nativeOutputWriter)
	return ok && stderr.FD == 2
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
	finalizing := w.State.DeadlineNS != 0 && w.State.Program != nil
	if w.Interrupted && !finalizing {
		return 0, posix.ErrOutputInterrupted
	}
	remaining := int64(0)
	if finalizing {
		w.State.Bounded = true
		remaining = w.State.DeadlineNS - w.State.Program.Host.Monotonic()
		if remaining <= 0 {
			remaining = -1
		}
	} else if w.State.Program != nil && !w.State.RunDeadline.IsZero() {
		remaining = int64(time.Until(w.State.RunDeadline))
		if remaining <= 0 {
			w.State.RunExpired = true
			return 0, posix.ErrOutputTimeout
		}
	}
	n, err := posix.WriteOutput(w.FD, data, remaining, !finalizing)
	if !finalizing {
		if err == syscall.EPIPE && w.State.Program != nil {
			// A closed relay must stop the producer as a direct pipe would.
			w.Failed = true
			w.State.Signal = int(syscall.SIGPIPE)
		}
		if w.State.Batch && (err == posix.ErrOutputInterrupted || err == posix.ErrOutputTimeout) {
			// Queued output from the cancelled body must not block cleanup.
			w.Interrupted = true
			w.State.RunExpired = w.State.RunExpired || err == posix.ErrOutputTimeout
		}
		return n, err
	}
	if signal := posix.TakeSignal(); signal != 0 {
		w.State.Signal = signal
		w.State.Program.Eval.FinalizerOutputCancelled(w.State.DeadlineNS)
	}
	if err != nil {
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
