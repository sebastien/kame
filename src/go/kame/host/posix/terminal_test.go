package posix

import (
	"errors"
	"kame/host"
	"testing"
)

func TestTerminalRestoreFailure(t *testing.T) {
	err := errors.New("terminal disconnected")
	success := host.ProcessEvent{Outcome: host.ProcessExited}
	terminalRestoreFailure(&success, err)
	if success.Outcome != host.ProcessFailed || success.Diagnostic.Code != DiagnosticHostFailure {
		t.Fatalf("restore failure reported success: %+v", success)
	}
	failure := host.ProcessEvent{Outcome: host.ProcessExited, Status: 42, EffectiveStatus: 42}
	terminalRestoreFailure(&failure, err)
	if failure.Status != 42 || failure.Diagnostic.Code != "" {
		t.Fatalf("restore failure replaced the child failure: %+v", failure)
	}
}
