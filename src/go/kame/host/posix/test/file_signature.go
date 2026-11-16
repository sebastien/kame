package posixcheck

import (
	"kame/core"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/internal/testing"
	"os"
)

func TestFileSignatureStreamsExactContentWithoutTrackedCopies(t *testing.T) {
	a := t.Allocator()
	tracker := a.(*mem.Tracker)
	h := posix.New(a)
	defer h.Free()

	directory, err := os.MkdirTemp("", "kame-signature-")
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

// Recently changed files are not memoized: a same-size rewrite that restores
// the modification time must still produce the new content's signature.
func TestFileSignatureDoesNotReuseRecentlyChangedContent(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	directory, err := os.MkdirTemp("", "kame-signature-")
	if err != nil {
		t.Fatal("create signature test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	name := directory + "/tool"
	if os.WriteFile(name, []byte("first"), 0o700) != nil {
		t.Fatal("write first content")
		return
	}
	info, err := os.Stat(name)
	if err != nil || !h.FileSignature(name).Equal(core.ContentSignature([]byte("first"))) {
		t.Fatal("first signature")
		return
	}
	if os.WriteFile(name, []byte("other"), 0o700) != nil || os.Chtimes(name, info.ModTime(), info.ModTime()) != nil {
		t.Fatal("rewrite content")
		return
	}
	if !h.FileSignature(name).Equal(core.ContentSignature([]byte("other"))) {
		t.Error("signature reused stale content after a same-size rewrite")
	}
}
