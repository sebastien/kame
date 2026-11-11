package posix

import (
	"bytes"
	"io"
	"kame/host"
	"os/exec"
	"time"
)

// Start and register under the same lock used by cancellation. A stopped graph
// may never advance to the next record, and retired workers leave no history.
func (r *running) startWorker(cmd *exec.Cmd) (*ownedProcess, error) {
	r.commandMu.Lock()
	defer r.commandMu.Unlock()
	select {
	case <-r.stopped:
		return nil, io.ErrClosedPipe
	default:
	}
	if e := cmd.Start(); e != nil {
		return nil, e
	}
	if r.owned == nil {
		r.owned = make(map[*exec.Cmd]*ownedProcess)
	}
	p := &ownedProcess{cmd: cmd}
	r.owned[cmd] = p
	return p, nil
}

func runEach(request host.ProcessRequest, stage host.ProcessStage, template *exec.Cmd, run *running, input io.Reader, output io.Writer) host.ProcessStageResult {
	result := host.ProcessStageResult{HasEffectiveStatus: true}
	for {
		select {
		case <-run.stopped:
			return operatorResult(io.ErrClosedPipe, run)
		default:
		}
		frame, e := readFrame(input, 0)
		if e == io.EOF {
			return result
		}
		if e != nil {
			return operatorResult(e, run)
		}
		cmd := stageCommand(request, stage)
		cmd.Path, cmd.Dir, cmd.Env, cmd.Err = template.Path, template.Dir, template.Env, nil
		cmd.Stdin = bytes.NewReader(frame[4:])
		cmd.Stdout, cmd.Stderr = output, template.Stderr
		if stage.ErrorMode == host.StderrStdout {
			cmd.Stderr = cmd.Stdout
		}
		owner, e := run.startWorker(cmd)
		if e != nil {
			return operatorResult(e, run)
		}
		var timer *time.Timer
		if stage.TimeoutMS > 0 {
			timer = time.AfterFunc(time.Duration(stage.TimeoutMS)*time.Millisecond, func() { run.terminate(host.ProcessTimedOut, 100) })
		}
		e = owner.wait()
		if timer != nil {
			timer.Stop()
		}
		run.commandMu.Lock()
		delete(run.owned, cmd)
		run.commandMu.Unlock()
		worker := processExit(e, cmd, stage)
		if outcome := host.ProcessOutcome(run.outcome.Load()); outcome != host.ProcessExited {
			worker.Outcome = outcome
			return worker
		}
		if worker.Status != 0 {
			if worker.EffectiveStatus == 0 {
				worker.EffectiveStatus = result.EffectiveStatus
			}
			result = worker
		}
		if worker.Signal != 0 || (worker.EffectiveStatus != 0 && !request.CollectFailures) {
			return worker
		}
	}
}
