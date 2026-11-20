package posix

import (
	"bytes"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

type gatedOutput struct {
	data    bytes.Buffer
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (o *gatedOutput) Write(data []byte) (int, error) {
	o.once.Do(func() {
		close(o.started)
		<-o.resume
	})
	return o.data.Write(data)
}

func TestChildOutputDrainsAfterExitUnderBackpressure(t *testing.T) {
	input, writeInput, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writeInput.Close()
	sink := &gatedOutput{started: make(chan struct{}), resume: make(chan struct{})}
	release := sync.OnceFunc(func() { close(sink.resume) })
	defer release()
	cmd := exec.Command("/bin/sh", "-c", "printf before; read -r ready; printf after >&2")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, sink, sink
	cmd.WaitDelay = commandPipeIdle
	run := &running{stopped: make(chan struct{})}
	child, err := run.startOwnedCommand(cmd, false)
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- child.wait(run) }()
	select {
	case <-sink.started:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not write to its output pipe")
	}
	if _, err := writeInput.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		child.mu.Lock()
		retired := child.retired
		child.mu.Unlock()
		if retired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not exit while publication was blocked")
		}
		time.Sleep(time.Millisecond)
	}
	// Keep publication blocked longer than the os/exec pipe-close delay.
	time.Sleep(2 * commandPipeIdle)
	release()
	select {
	case err := <-waited:
		if err != nil || sink.data.String() != "beforeafter" {
			t.Fatalf("process wait error = %v, merged output = %q", err, sink.data.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child output did not drain")
	}
}
