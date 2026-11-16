package posix

import (
	"kame/core"
	"os"
	"sync"
	"time"
)

// fileIdentity is the stat metadata that changes whenever file content can
// change: replacing a file changes its inode, and writing updates mtime and
// ctime. ctime cannot be set from user space.
type fileIdentity struct {
	Device, Inode, Size   uint64
	ModifiedNs, ChangedNs int64
}

// digestRacyWindow excludes files changed shortly before hashing: a write
// within the same timestamp tick could otherwise keep identical metadata.
const digestRacyWindow = 2 * time.Second

type digestMemo struct {
	mu      sync.Mutex
	entries map[string]digestMemoEntry
}

type digestMemoEntry struct {
	Identity  fileIdentity
	Signature core.Signature
}

// contentDigests is process-wide so that repeated sessions (REPL entries, watch
// iterations) do not re-read unchanged executables and inputs.
var contentDigests digestMemo

func openFileIdentity(file *os.File) (fileIdentity, bool) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fileIdentity{}, false
	}
	return statIdentity(info)
}

func (m *digestMemo) lookup(path string, identity fileIdentity) (core.Signature, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[path]
	if !ok || entry.Identity != identity {
		return core.Signature{}, false
	}
	return entry.Signature, true
}

func (m *digestMemo) store(path string, identity fileIdentity, signature core.Signature, started time.Time) {
	limit := started.Add(-digestRacyWindow).UnixNano()
	if identity.ModifiedNs >= limit || identity.ChangedNs >= limit {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[string]digestMemoEntry{}
	}
	m.entries[path] = digestMemoEntry{Identity: identity, Signature: signature}
}
