//go:build linux

package posixcheck

import (
	"kame/core"
	"kame/host/posix"
	"kame/internal/testing"
	"os"
	"time"
)

// Large stable files persist their digest under the user cache directory;
// a later content change (new change time) must not reuse that record.
func TestFileSignaturePersistsLargeStableDigests(t *testing.T) {
	h := posix.New(t.Allocator())
	defer h.Free()
	directory, err := os.MkdirTemp("", "kame-signature-")
	if err != nil {
		t.Fatal("create signature test directory")
		return
	}
	defer removeAtomicTestDirectory(directory)
	cacheHome := directory + "/cache"
	previous, had := os.LookupEnv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", cacheHome)
	defer func() {
		if had {
			os.Setenv("XDG_CACHE_HOME", previous)
		} else {
			os.Unsetenv("XDG_CACHE_HOME")
		}
	}()
	data := make([]byte, 300<<10)
	name := directory + "/large-tool"
	if os.WriteFile(name, data, 0o700) != nil {
		t.Fatal("write large tool")
		return
	}
	// Memoization skips files changed within the racy window.
	time.Sleep(2100 * time.Millisecond)
	if !h.FileSignature(name).Equal(core.ContentSignature(data)) {
		t.Fatal("large file signature")
		return
	}
	records, _ := os.ReadDir(cacheHome + "/kame/digests")
	if len(records) != 1 {
		t.Errorf("expected one persisted digest record, found %d", len(records))
	}
	data[0] = 1
	if os.WriteFile(name, data, 0o700) != nil {
		t.Fatal("rewrite large tool")
		return
	}
	if !h.FileSignature(name).Equal(core.ContentSignature(data)) {
		t.Error("persisted digest was reused after the content changed")
	}
}
