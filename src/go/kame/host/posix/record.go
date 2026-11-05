package posix

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/c"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

type recordCursor struct {
	ID        int64
	FD        int
	Offset    int64
	Name      string
	Delimiter int
	Stdin     bool
	InputFD   int
	Data      []byte
	Size      int
	Ended     bool
}

//so:extern nodecay
func km_record_open(name string) c.Int { _ = name; return -1 }

//so:extern nodecay
func km_record_next(fd int, offset *int64, delimiter int, buffer []byte) c.Int {
	_, _, _, _ = fd, offset, delimiter, buffer
	return -1
}

//so:extern
func km_record_close(fd int) { _ = fd }

//so:extern
func km_record_borrow(fd int) c.Int { _ = fd; return -1 }

//so:extern
func km_record_byte(fd int) c.Int { _ = fd; return -2 }

func (h *Host) ReadRecord(a mem.Allocator, id int64, name string, delimiter int, inputFD int) host.RecordResult {
	if id <= 0 || (delimiter != 0 && delimiter != 10) {
		return host.RecordResult{Code: "EXPR_INVALID"}
	}
	stdin := name == ""
	if stdin && (inputFD < 0 || inputFD > 2147483647) {
		return host.RecordResult{Code: "FEATURE_UNSUP"}
	}
	if stdin && h.StdinRecord != 0 && h.StdinRecord != id {
		return host.RecordResult{Code: "KASH_DOUBLE_CONSUME"}
	}
	index := -1
	for i := range h.Readers {
		if h.Readers[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		fd := -1
		if stdin {
			fd = int(km_record_borrow(inputFD))
		} else {
			path := h.hostPath(name)
			if path.Error != nil {
				h.freeHostPath(path)
				return host.RecordResult{Code: "FS_ERR"}
			}
			fd = int(km_record_open(path.Path))
			h.freeHostPath(path)
		}
		if fd < 0 {
			return host.RecordResult{Code: "FS_ERR"}
		}
		cursor := recordCursor{ID: id, FD: fd, Name: cloneText(h.Alloc, name), Delimiter: delimiter, Stdin: stdin, InputFD: inputFD}
		if stdin {
			cursor.Data = slices.Make[byte](h.Alloc, host.RecordLimit)
			h.StdinRecord = id
		}
		h.Readers = slices.Append(h.Alloc, h.Readers, cursor)
		index = len(h.Readers) - 1
	}
	cursor := &h.Readers[index]
	if cursor.Name != name || cursor.Delimiter != delimiter || (stdin && cursor.InputFD != inputFD) {
		return host.RecordResult{Code: "EXPR_INVALID"}
	}
	if stdin {
		return h.readStdinRecord(a, cursor)
	}
	buffer := slices.Make[byte](a, host.RecordLimit+1)
	defer slices.Free(a, buffer)
	size := int(km_record_next(cursor.FD, &cursor.Offset, delimiter, buffer))
	if size < 0 {
		code := "FS_ERR"
		if size == -2 {
			code = "KASH_RECORD_LIMIT"
		}
		return host.RecordResult{Code: code}
	}
	if size == 0 {
		return host.RecordResult{}
	}
	return host.RecordResult{Value: core.NewString(a, string(buffer[:size]))}
}

func (h *Host) readStdinRecord(a mem.Allocator, cursor *recordCursor) host.RecordResult {
	_ = h
	if cursor.Ended {
		return host.RecordResult{}
	}
	// ponytail: one-byte reads avoid consuming bytes past a record delimiter.
	// Yield after 4 KiB; add a shared peek/read transport only if profiling needs it.
	for i := 0; i < 4096; i++ {
		value := int(km_record_byte(cursor.FD))
		if value == -1 {
			return host.RecordResult{Waiting: true}
		}
		if value == -2 {
			return host.RecordResult{Code: "FS_ERR"}
		}
		if value == 0 {
			cursor.Ended = true
			if cursor.Size == 0 {
				return host.RecordResult{}
			}
		} else {
			if cursor.Size == host.RecordLimit {
				return host.RecordResult{Code: "KASH_RECORD_LIMIT"}
			}
			cursor.Data[cursor.Size] = byte(value - 1)
			cursor.Size++
			if value-1 != cursor.Delimiter {
				continue
			}
		}
		r := host.RecordResult{Value: core.NewString(a, string(cursor.Data[:cursor.Size]))}
		cursor.Size = 0
		return r
	}
	return host.RecordResult{Waiting: true}
}

func (h *Host) CloseRecord(id int64) {
	for i := range h.Readers {
		if h.Readers[i].ID == id {
			km_record_close(h.Readers[i].FD)
			mem.FreeString(h.Alloc, h.Readers[i].Name)
			slices.Free(h.Alloc, h.Readers[i].Data)
			if h.StdinRecord == id {
				h.StdinRecord = 0
			}
			copy(h.Readers[i:], h.Readers[i+1:])
			h.Readers = h.Readers[:len(h.Readers)-1]
			return
		}
	}
}
