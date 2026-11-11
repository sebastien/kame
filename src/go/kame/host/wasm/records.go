package wasm

import (
	"kame/core"
	"kame/host"
	"kame/internal/mem"
)

type memoryRecordCursor struct {
	ID        int64
	Name      string
	Offset    int
	Delimiter int
}

func (h *MemoryHost) ReadRecord(a mem.Allocator, id int64, name string, delimiter int, inputFD int) host.RecordResult {
	_ = inputFD
	if name == "" {
		return host.RecordResult{Code: "FEATURE_UNSUP"}
	}
	if id <= 0 || name == "" || (delimiter != 0 && delimiter != 10) {
		return host.RecordResult{Code: "EXPR_INVALID"}
	}
	file := h.findFile(name)
	if file < 0 {
		return host.RecordResult{Code: "FS_ERR"}
	}
	index := -1
	for i := range h.Readers {
		if h.Readers[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		h.Readers = append(h.Readers, memoryRecordCursor{ID: id, Name: pureText(h.Alloc, name), Delimiter: delimiter})
		index = len(h.Readers) - 1
	}
	cursor := &h.Readers[index]
	if cursor.Name != name || cursor.Delimiter != delimiter {
		return host.RecordResult{Code: "EXPR_INVALID"}
	}
	data := h.Files[file].Data
	start := cursor.Offset
	if start >= len(data) {
		return host.RecordResult{}
	}
	end := start
	for end < len(data) && end-start <= host.RecordLimit {
		value := data[end]
		end++
		if int(value) == delimiter {
			break
		}
	}
	if end-start > host.RecordLimit {
		return host.RecordResult{Code: "KASH_RECORD_LIMIT"}
	}
	cursor.Offset = end
	return host.RecordResult{Value: core.NewString(a, string(data[start:end]))}
}

func (h *MemoryHost) CloseRecord(id int64) {
	for i := range h.Readers {
		if h.Readers[i].ID == id {
			mem.FreeString(h.Alloc, h.Readers[i].Name)
			copy(h.Readers[i:], h.Readers[i+1:])
			h.Readers = h.Readers[:len(h.Readers)-1]
			return
		}
	}
}
