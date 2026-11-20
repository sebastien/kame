package posix

import (
	"bytes"
	"errors"
	"io"
	"kame/host"
	"os/exec"
)

func runEach(request host.ProcessRequest, stage host.ProcessStage, template *exec.Cmd, run *running, input io.Reader, output io.Writer) (host.ProcessStageResult, bool) {
	result := host.ProcessStageResult{HasEffectiveStatus: true}
	if stage.TimeoutMS > 0 {
		timer := run.newTimer(stage.TimeoutMS, func() { run.terminate(host.ProcessTimedOut, 100) })
		defer timer.Stop()
	}
	for {
		select {
		case <-run.stopped:
			return operatorResult(io.ErrClosedPipe, run), false
		default:
		}
		frame, e := readFrame(input, 0)
		if e == io.EOF {
			return result, true
		}
		if e != nil {
			return operatorResult(e, run), false
		}
		cmd := stageCommand(request, stage)
		cmd.Path, cmd.Dir, cmd.Env, cmd.Err = template.Path, template.Dir, template.Env, nil
		cmd.Stdin = bytes.NewReader(frame[4:])
		cmd.Stdout, cmd.Stderr = output, template.Stderr
		if stage.ErrorMode == host.StderrStdout {
			cmd.Stderr = cmd.Stdout
		}
		owner, e := run.startOwnedCommand(cmd, false)
		if e != nil {
			return operatorResult(e, run), false
		}
		e = owner.wait(run)
		run.commandMu.Lock()
		delete(run.owned, cmd)
		run.commandMu.Unlock()
		worker := processExit(e, cmd, stage)
		if outcome := host.ProcessOutcome(run.outcome.Load()); outcome != host.ProcessExited {
			worker.Outcome = outcome
			return worker, false
		}
		var exit *exec.ExitError
		if e != nil && !errors.As(e, &exit) {
			return operatorResult(e, run), false
		}
		if worker.Status != 0 {
			if worker.EffectiveStatus == 0 {
				worker.EffectiveStatus = result.EffectiveStatus
			}
			result = worker
		}
		if worker.Signal != 0 || (worker.EffectiveStatus != 0 && !request.CollectFailures) {
			return worker, false
		}
	}
}
