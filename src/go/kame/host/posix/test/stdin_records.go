//go:build linux

package posixcheck

import (
	"kame/host/posix"
	"kame/internal/testing"
	"syscall"
)

func stdinRecordPipe(reader *int, writer *int) bool {
	var fds [2]int
	if syscall.Pipe(fds[:]) != nil {
		return false
	}
	*reader, *writer = fds[0], fds[1]
	return true
}
func stdinRecordClose(fd int) { syscall.Close(fd) }
func stdinRecordFlags(fd int) int {
	v, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if e != 0 {
		return -1
	}
	return int(v)
}
func stdinRecordWrite(fd int, data string) bool {
	n, e := syscall.Write(fd, []byte(data))
	return e == nil && n == len(data)
}
func stdinRecordRead(fd int, data []byte) int {
	var fds syscall.FdSet
	fds.Bits[fd/64] |= 1 << uint(fd%64)
	tv := syscall.Timeval{}
	n, e := syscall.Select(fd+1, &fds, nil, nil, &tv)
	if e != nil || n == 0 {
		return -1
	}
	n, e = syscall.Read(fd, data)
	if e != nil {
		return -1
	}
	return n
}

func TestStdinRecordClaimWaitAndRemainingBytes(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	defer h.Free()
	reader, writer := -1, -1
	if !stdinRecordPipe(&reader, &writer) {
		t.Fatal("create input pipe")
		return
	}
	defer stdinRecordClose(reader)
	defer stdinRecordClose(writer)
	flags := stdinRecordFlags(reader)
	first := h.ReadRecord(a, 7, "", 10, reader)
	if !first.Waiting || first.Code != "" || h.StdinRecord != 7 {
		t.Error("empty live input did not suspend with an exclusive claim")
	}
	first.Value.Free(a)
	second := h.ReadRecord(a, 8, "", 10, reader)
	if second.Code != "KASH_DOUBLE_CONSUME" {
		t.Error("competing record reader was accepted")
	}
	second.Value.Free(a)
	h.CloseRecord(8)
	if h.StdinRecord != 7 {
		t.Error("closing rejected reader released the live claim")
	}
	if !stdinRecordWrite(writer, "one\ntwo\n") {
		t.Fatal("write input")
		return
	}
	first = h.ReadRecord(a, 7, "", 10, reader)
	if first.Waiting || first.Code != "" || first.Value.Text != "one\n" {
		t.Error("reader lost a record across suspension")
	}
	first.Value.Free(a)
	h.CloseRecord(7)
	var data [4]byte
	if stdinRecordRead(reader, data[:]) != 4 || string(data[:]) != "two\n" {
		t.Error("reader consumed bytes beyond its delimiter or closed the caller input")
	}
	if stdinRecordFlags(reader) != flags || h.StdinRecord != 0 || len(h.Readers) != 0 {
		t.Error("reader changed borrowed descriptor flags or retained its claim")
	}
}

func TestStdinRecordCancellationDoesNotConsumeFutureBytes(t *testing.T) {
	a := t.Allocator()
	h := posix.New(a)
	defer h.Free()
	reader, writer := -1, -1
	if !stdinRecordPipe(&reader, &writer) {
		t.Fatal("create input pipe")
		return
	}
	defer stdinRecordClose(reader)
	defer stdinRecordClose(writer)
	if !stdinRecordWrite(writer, "partial") {
		t.Fatal("write input")
		return
	}
	r := h.ReadRecord(a, 9, "", 10, reader)
	if !r.Waiting || r.Code != "" {
		t.Error("unterminated live input did not suspend")
	}
	r.Value.Free(a)
	h.CloseRecord(9)
	if !stdinRecordWrite(writer, "rest\n") {
		t.Fatal("write future input")
		return
	}
	var data [5]byte
	if stdinRecordRead(reader, data[:]) != 5 || string(data[:]) != "rest\n" {
		t.Error("cancelled reader stole future input or closed the caller descriptor")
	}
}
