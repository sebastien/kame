package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNativeKashNonblockingPipesKeepChildIOBlocking(t *testing.T) {
	binary := nativeCLIBinary(t)
	payload := bytes.Repeat([]byte("ab"), 512*1024)
	requireBlocking := func(fd int, command string) string {
		return fmt.Sprintf(`sh -c "while read key flags; do if [ \"\$key\" = flags: ] && [ \$((flags & 04000)) -ne 0 ]; then exit 91; fi; done < /proc/self/fdinfo/%d; %s"`, fd, command)
	}
	for _, test := range []struct {
		name, source string
		want         []byte
	}{
		{"linear handoff", requireBlocking(0, "printf R >&2; exec dd bs=1 count=2 status=none") +
			"\ncat | " + requireBlocking(1, "exec cat"), payload},
		{"fan-out", requireBlocking(0, "printf R >&2; exec cat") +
			" |(broadcast) (" + requireBlocking(1, "exec wc -c") + ", wc -c)", []byte("1048576\n1048576\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			in, producer := nonblockingTestPipe(t)
			consumer, out := nonblockingTestPipe(t)
			ready, childErr, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer ready.Close()
			defer childErr.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "do", "run", "--lang", "kash", "-c", test.source)
			command.Stdin, command.Stdout, command.Stderr = in, out, childErr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					cancel()
					command.Wait()
				}
			}()
			childErr.Close()
			deadline, _ := ctx.Deadline()
			if err := ready.SetReadDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			var marker [1]byte
			if _, err := io.ReadFull(ready, marker[:]); err != nil || marker[0] != 'R' {
				t.Fatalf("reader never became ready: marker=%q err=%v", marker, err)
			}
			for _, endpoint := range []*os.File{in, out} {
				flags, err := unix.FcntlInt(endpoint.Fd(), unix.F_GETFL, 0)
				if err != nil || flags&unix.O_NONBLOCK == 0 {
					t.Fatalf("caller lost nonblocking status: fd=%s flags=%#x err=%v", endpoint.Name(), flags, err)
				}
			}
			in.Close()
			out.Close()
			if err := producer.SetWriteDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			produced := make(chan error, 1)
			go func() {
				_, err := producer.Write(payload)
				producer.Close()
				produced <- err
			}()
			if err := consumer.SetReadDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			data, readErr := io.ReadAll(consumer)
			status := command.Wait()
			diagnostics, _ := io.ReadAll(ready)
			if err := <-produced; err != nil || readErr != nil || status != nil || !bytes.Equal(data, test.want) || len(diagnostics) != 0 {
				t.Fatalf("read %d of %d expected bytes (equal=%v), producer=%v consumer=%v status=%v stderr=%q",
					len(data), len(test.want), bytes.Equal(data, test.want), err, readErr, status, diagnostics)
			}
		})
	}
}

func nonblockingTestPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	var fd [2]int
	if err := unix.Pipe2(fd[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	read, write := os.NewFile(uintptr(fd[0]), "pipe reader"), os.NewFile(uintptr(fd[1]), "pipe writer")
	t.Cleanup(func() { read.Close(); write.Close() })
	return read, write
}

func TestNativeKashClosedOutputRelayStopsProducer(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(pair[0])
	output := os.NewFile(uintptr(pair[1]), "closed socket")
	defer output.Close()
	var stderr bytes.Buffer
	status := runNativeCLI(t, []string{"do", "run", "--lang", "kash", "--timeout", "2000", "-c", "yes"}, nil, output, &stderr)
	if status != 128+int(syscall.SIGPIPE) {
		t.Fatalf("status=%d stderr=%q", status, stderr.String())
	}
}

func TestNativeKashFullOutputRelayHonorsInvocationTimeout(t *testing.T) {
	binary := nativeCLIBinary(t)
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pair[0])
	output := os.NewFile(uintptr(pair[1]), "full socket")
	defer output.Close()
	var chunk [4096]byte
	for {
		_, err := unix.Write(pair[1], chunk[:])
		if err == unix.EAGAIN {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "do", "run", "--lang", "kash", "--timeout", "250", "-c", `defer (err! "cleaned")
yes`)
	var stderr bytes.Buffer
	command.Stdout, command.Stderr = output, &stderr
	err = command.Run()
	if ctx.Err() != nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 1 ||
		!strings.Contains(stderr.String(), "RECIPE_TIMEOUT") || !strings.Contains(stderr.String(), "cleaned") {
		t.Fatalf("timeout=%v status=%v stderr=%q", ctx.Err(), err, stderr.String())
	}
}
