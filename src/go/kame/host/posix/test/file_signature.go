package posix_test

import (
	"kame/core"
	"kame/host/posix"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/testing"
)

func TestFileSignatureStreamsExactContentWithoutTrackedCopies(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	h := posix.New(a)
	defer h.Free()
	var pathBuffer [os.MaxPathLen]byte
	directory, err := os.MkdirTemp(pathBuffer[:], "", "kame-signature-")
	if err != nil {
		t.Fatal("create signature test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	data := mem.AllocSlice[byte](a, 3*65536+17, 3*65536+17)
	defer mem.FreeSlice(a, data)
	for i := range data {
		data[i] = byte(i % 251)
	}
	name := directory + "/input"
	if os.WriteFile(name, data, 0o600) != nil {
		t.Fatal("write signature input")
		return
	}
	before := tracker.Stats().TotalAlloc
	actual := h.FileSignature(name)
	if !actual.Equal(core.ContentSignature(data)) {
		t.Error("streamed signature differs from whole-file SHA-256")
	}
	if tracker.Stats().TotalAlloc-before >= uint64(len(data)) {
		t.Error("fingerprint allocated a full-file copy")
	}
	if os.Symlink(name, directory+"/link") != nil || !h.FileSignature(directory+"/link").Equal(actual) {
		t.Error("symlink signature did not follow content")
	}
	if h.FileSignature(directory+"/missing").Mode != core.SignatureUnavailable {
		t.Error("unreadable file became a reusable signature")
	}
	if os.WriteFile(name, nil, 0o600) != nil || !h.FileSignature(name).Equal(core.ContentSignature(nil)) {
		t.Error("empty content has the wrong signature")
	}
}
