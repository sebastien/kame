package posix_test

import (
	"kame/host/posix"
	"solod.dev/so/c"
	"solod.dev/so/testing"
)

//so:include <unistd.h>
//so:include <fcntl.h>
//so:include <poll.h>

func stdinRecordPipe(reader *int, writer *int) bool {
	c.Raw("int descriptors[2]; if (pipe(descriptors)) return false; *reader = descriptors[0]; *writer = descriptors[1]; return true;")
	return false
}

func stdinRecordClose(fd int) { c.Raw("if (fd >= 0) close((int)fd);") }

func stdinRecordFlags(fd int) int { c.Raw("return fcntl((int)fd, F_GETFL);"); return -1 }

func stdinRecordWrite(fd int, data string) bool {
	c.Raw("return write((int)fd, data.ptr, data.len) == data.len;")
	return false
}

func stdinRecordRead(fd int, data []byte) int {
	c.Raw("struct pollfd ready = {.fd = (int)fd, .events = POLLIN}; if (poll(&ready, 1, 0) <= 0) return -1; return read((int)fd, data.ptr, data.len);")
	return -1
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
