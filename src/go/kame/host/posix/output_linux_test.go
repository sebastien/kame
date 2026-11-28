package posix

import (
	"bytes"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWriteOutputFullPipeKeepsBorrowedFlags(t *testing.T) {
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipe[0])
	defer unix.Close(pipe[1])
	size, err := unix.FcntlInt(uintptr(pipe[1]), unix.F_GETPIPE_SZ, 0)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := unix.Write(pipe[1], bytes.Repeat([]byte{'x'}, size)); err != nil || count != size {
		t.Fatalf("fill pipe: wrote %d of %d: %v", count, size, err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := WriteOutput(pipe[1], []byte{'x'}, int64(150*time.Millisecond), true)
		done <- err
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err != ErrOutputTimeout {
				t.Fatalf("blocked output error=%v, want timeout", err)
			}
			return
		case <-ticker.C:
			flags, err := unix.FcntlInt(uintptr(pipe[1]), unix.F_GETFL, 0)
			if err != nil || flags&unix.O_NONBLOCK != 0 {
				<-done
				t.Fatalf("host changed the caller's flags during a blocked write: flags=%#x err=%v", flags, err)
			}
		}
	}
}
