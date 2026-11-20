package posix

import (
	"bytes"
	"errors"
	"io"
	"kame/host"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type callableStage struct {
	stdin        io.Reader
	stdout       io.Writer
	outputPipe   *os.File
	stderr       io.Writer
	result       chan host.ProcessStageResult
	ports        sync.Mutex
	busy         atomic.Bool
	ended        atomic.Bool
	outputClosed atomic.Bool
	watching     atomic.Bool
	accept       [256]bool
}

func (s *callableStage) inputFile() *os.File {
	switch input := s.stdin.(type) {
	case *os.File:
		return input
	case *meteredReader:
		return input.file
	default:
		return nil
	}
}

type inheritedStageWriter struct {
	stage  *callableStage
	port   io.Writer
	stdout bool
}

func (w inheritedStageWriter) Write(data []byte) (int, error) {
	w.stage.ports.Lock()
	defer w.stage.ports.Unlock()
	if w.stage.ended.Load() {
		return 0, syscall.EPIPE
	}
	count, err := w.port.Write(data)
	if w.stdout && errors.Is(err, syscall.EPIPE) {
		w.stage.outputClosed.Store(true)
	}
	return count, err
}

func (r *running) followStageParent(parent *running) {
	if parent == nil {
		return
	}
	go func() {
		select {
		case <-parent.stopped:
			r.terminate(host.ProcessCancelled, 100)
		case <-r.done:
		}
	}()
}

func (s *callableStage) wait(run *running, timeoutMS int64) host.ProcessStageResult {
	if timeoutMS > 0 {
		timer := run.newTimer(timeoutMS, func() {
			if !s.ended.Load() {
				run.terminate(host.ProcessTimedOut, 0)
			}
		})
		defer timer.Stop()
	}
	select {
	case result := <-s.result:
		return result
	case <-run.stopped:
		s.ended.Store(true)
		// Graph teardown closes its descriptors before joining the stage.
		// Wait for any exchange to leave the ports before snapshots read them.
		s.ports.Lock()
		s.ports.Unlock()
		return host.ProcessStageResult{Outcome: host.ProcessCancelled, HasEffectiveStatus: true}
	}
}

func (h *Host) StartStageIO(request host.Request, exchange host.StageIO) bool {
	if request.ID <= 0 || !exchange.Valid() {
		return false
	}
	event := host.ProcessEvent{ID: request.ID, Kind: host.ProcessStageIOCompleted, StageIO: host.StageIOCompletion{
		NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, Operation: exchange.Operation,
	}}
	run := h.running(exchange.Graph)
	if run == nil {
		event.StageIO.Closed = true
		h.pending = append(h.pending, event)
		return true
	}
	stage := run.callables[exchange.Index]
	if stage == nil {
		return false
	}
	if exchange.Operation == host.StageWatch {
		if !stage.watching.CompareAndSwap(false, true) {
			return false
		}
		go func() {
			stage.watchOutput(run, &event)
			stage.watching.Store(false)
			h.emit(event)
		}()
		return true
	}
	if !stage.busy.CompareAndSwap(false, true) {
		return false
	}
	exchange.Bytes = bytes.Clone(exchange.Bytes)
	go func() {
		stage.exchange(run, exchange, &event)
		stage.busy.Store(false)
		h.emit(event)
	}()
	return true
}

func (s *callableStage) exchange(run *running, exchange host.StageIO, event *host.ProcessEvent) {
	s.ports.Lock()
	defer s.ports.Unlock()
	if s.ended.Load() {
		event.StageIO.Closed = true
		return
	}
	select {
	case <-run.stopped:
		event.StageIO.Closed = true
		return
	default:
	}
	var err error
	switch exchange.Operation {
	case host.StageRead:
		if s.outputClosed.Load() {
			event.Diagnostic = host.ProcessDiagnostic{Code: "EXEC_CANCELLED", Message: "callable output consumer closed"}
			return
		}
		if s.stdin == nil {
			err = io.EOF
		} else {
			buffer := make([]byte, host.StageChunkLimit)
			count, readErr := s.readInput(run, buffer, exchange.Frame)
			if s.outputClosed.Load() {
				event.Diagnostic = host.ProcessDiagnostic{Code: "EXEC_CANCELLED", Message: "callable output consumer closed"}
				return
			}
			err = readErr
			if count > 0 {
				event.Data, err = buffer[:count], nil
			}
		}
	case host.StageStdout, host.StageStderr:
		out := s.stdout
		if exchange.Operation == host.StageStderr {
			out = s.stderr
		}
		count, writeErr := out.Write(exchange.Bytes)
		err = writeErr
		if err == nil && count != len(exchange.Bytes) {
			err = io.ErrShortWrite
		}
	case host.StageFinish:
		result := host.ProcessStageResult{Status: exchange.Status, Signal: exchange.Signal, EffectiveStatus: exchange.Status, HasEffectiveStatus: true}
		if exchange.Cancelled || s.outputClosed.Load() {
			result = host.ProcessStageResult{Outcome: host.ProcessCancelled, HasEffectiveStatus: true}
		} else if exchange.Signal == 0 && s.accept[exchange.Status] {
			result.EffectiveStatus = 0
		}
		s.ended.Store(true)
		s.result <- result
	}
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.EPIPE) || errors.Is(err, os.ErrClosed) {
		event.StageIO.Closed = true
	} else if err != nil {
		event.Diagnostic = host.ProcessDiagnostic{Code: "HOST_FAIL", Message: err.Error()}
	}
}

func (s *callableStage) watchOutput(run *running, event *host.ProcessEvent) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !s.ended.Load() {
		closed, err := s.probeOutput()
		if closed {
			s.outputClosed.Store(true)
			event.StageIO.Closed = true
			return
		}
		if err != nil && !errors.Is(err, os.ErrClosed) {
			event.Diagnostic = host.ProcessDiagnostic{Code: "HOST_FAIL", Message: err.Error()}
			return
		}
		select {
		case <-run.stopped:
			event.StageIO.Closed = true
			return
		case <-ticker.C:
		}
	}
}

func (s *callableStage) probeOutput() (bool, error) {
	if s.outputClosed.Load() {
		return true, nil
	}
	if s.outputPipe == nil {
		return false, nil
	}
	descriptor, err := s.outputPipe.SyscallConn()
	if err != nil {
		return false, err
	}
	closed := false
	controlErr := descriptor.Control(func(fd uintptr) {
		poll := [1]unix.PollFd{{Fd: int32(fd)}}
		for {
			_, err = unix.Poll(poll[:], 0)
			if err != unix.EINTR {
				break
			}
		}
		closed = poll[0].Revents&(unix.POLLHUP|unix.POLLERR) != 0
	})
	if controlErr != nil {
		return false, controlErr
	}
	return closed, err
}

func (s *callableStage) readInput(run *running, buffer []byte, frame string) (count int, err error) {
	if reader, ok := s.stdin.(*meteredReader); ok {
		reader.node.reading.Add(1)
		defer func() {
			reader.node.reading.Add(-1)
			if reader.meter.measurement == 2 && !reader.meter.framed {
				reader.meter.add(&reader.meter.bytes, int64(count))
			}
		}()
	}
	file := s.inputFile()
	if file == nil {
		return 0, syscall.EBADF
	}
	descriptor, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var delimiter byte
	if frame == ":lines" {
		delimiter = '\n'
	}
	// exec may put the shared input descriptor in blocking mode. Poll with a
	// bounded wait before every byte and keep the descriptor live until the
	// read returns, so graph cancellation can close even a stalled FIFO.
	for count < len(buffer) {
		if s.outputClosed.Load() {
			return count, os.ErrClosed
		}
		select {
		case <-run.stopped:
			return count, os.ErrClosed
		default:
		}
		var consumed int
		controlErr := descriptor.Control(func(fd uintptr) {
			poll := [1]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			ready, pollErr := unix.Poll(poll[:], 10)
			err = pollErr
			if err == nil && ready != 0 {
				destination := buffer[count:]
				if frame != "" {
					destination = destination[:1]
				}
				consumed, err = unix.Read(int(fd), destination)
				if err == nil && consumed == 0 {
					err = io.EOF
				}
			}
		})
		if controlErr != nil {
			return count, controlErr
		}
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return count, err
		}
		count += consumed
		if consumed != 0 && (frame == "" || buffer[count-1] == delimiter) {
			break
		}
	}
	return count, nil
}
