package posix

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"time"

	"golang.org/x/sys/unix"
)

const commandPipeIdle = 100 * time.Millisecond

// commandOutput owns the reader of a child output pipe. Reaping the child
// must not close this reader while its destination is applying backpressure.
type commandOutput struct {
	reader   *os.File
	writer   *os.File
	sink     io.Writer
	done     chan error
	draining bool
}

func commandOutputs(cmd *exec.Cmd) ([]commandOutput, error) {
	var outputs []commandOutput
	merged := cmd.Stdout != nil && reflect.ValueOf(cmd.Stdout).Comparable() && cmd.Stdout == cmd.Stderr
	for _, target := range []*io.Writer{&cmd.Stdout, &cmd.Stderr} {
		sink := *target
		if _, file := sink.(*os.File); file || sink == nil {
			continue
		}
		if target == &cmd.Stderr && merged {
			*target = cmd.Stdout
			continue
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			for i := range outputs {
				outputs[i].reader.Close()
				outputs[i].writer.Close()
			}
			return nil, err
		}
		outputs = append(outputs, commandOutput{reader: reader, writer: writer, sink: sink, done: make(chan error, 1)})
		*target = writer
	}
	return outputs, nil
}

func (o *commandOutput) copy() {
	buffer := make([]byte, 16*1024)
	_, err := io.CopyBuffer(struct{ io.Writer }{o.sink}, o, buffer)
	o.reader.Close()
	o.done <- err
}

func (o *commandOutput) Read(buffer []byte) (int, error) {
	if !o.draining {
		count, err := o.reader.Read(buffer)
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return count, err
		}
		o.draining = true
		if err := o.reader.SetReadDeadline(time.Time{}); err != nil {
			return count, err
		}
		if count != 0 {
			return count, nil
		}
	}
	// After the group has been stopped, any idle writer belongs to a process
	// that kept the descriptor outside the group. Wait for it only while
	// reading. Time spent writing a chunk to the sink does not count.
	conn, err := o.reader.SyscallConn()
	if err != nil {
		return 0, err
	}
	var ready int
	if controlErr := conn.Control(func(fd uintptr) {
		poll := [1]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		for {
			ready, err = unix.Poll(poll[:], int(commandPipeIdle/time.Millisecond))
			if err != unix.EINTR {
				break
			}
		}
	}); controlErr != nil {
		return 0, controlErr
	}
	if err != nil {
		return 0, err
	}
	if ready == 0 {
		return 0, io.EOF
	}
	return o.reader.Read(buffer)
}
