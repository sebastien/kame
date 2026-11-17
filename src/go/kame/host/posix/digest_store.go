package posix

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"kame/core"
	"os"
	"path/filepath"
	"sync"
)

// Large executables (compilers, runtimes) are hashed by every new process that
// launches them. The persistent store keeps their content digests keyed by
// path and stat identity in the user cache directory, so a one-shot script
// does not re-read a 100 MB tool on each run. It is an optimization only:
// every failure falls back to hashing, and an entry is used only when the
// file's device, inode, size, modification and change times all match.
const persistentDigestMinSize = 256 << 10

var persistentDigestMagic = [4]byte{'K', 'D', 'M', '1'}

const persistentDigestRecordSize = 4 + 5*8 + 32

var persistentDigestDirectory = sync.OnceValue(func() string {
	if os.Getenv("KAME_DIGEST_CACHE") == "0" {
		return ""
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "kame", "digests")
})

func persistentDigestPath(path string) string {
	directory := persistentDigestDirectory()
	if directory == "" || !filepath.IsAbs(path) {
		return ""
	}
	key := sha256.Sum256([]byte(path))
	return filepath.Join(directory, hex.EncodeToString(key[:]))
}

func encodeDigestRecord(identity fileIdentity, signature core.Signature) []byte {
	record := make([]byte, 0, persistentDigestRecordSize)
	record = append(record, persistentDigestMagic[:]...)
	for _, field := range []uint64{identity.Device, identity.Inode, identity.Size, uint64(identity.ModifiedNs), uint64(identity.ChangedNs)} {
		record = binary.LittleEndian.AppendUint64(record, field)
	}
	return append(record, signature.Digest[:]...)
}

func loadPersistentDigest(path string, identity fileIdentity) (core.Signature, bool) {
	if identity.Size < persistentDigestMinSize {
		return core.Signature{}, false
	}
	name := persistentDigestPath(path)
	if name == "" {
		return core.Signature{}, false
	}
	record, err := os.ReadFile(name)
	if err != nil || len(record) != persistentDigestRecordSize {
		return core.Signature{}, false
	}
	expected := encodeDigestRecord(identity, core.Signature{})
	if string(record[:len(record)-32]) != string(expected[:len(expected)-32]) {
		return core.Signature{}, false
	}
	signature := core.Signature{Mode: core.SignatureContent}
	copy(signature.Digest[:], record[len(record)-32:])
	return signature, true
}

func storePersistentDigest(path string, identity fileIdentity, signature core.Signature) {
	if identity.Size < persistentDigestMinSize {
		return
	}
	name := persistentDigestPath(path)
	if name == "" {
		return
	}
	directory := filepath.Dir(name)
	if os.MkdirAll(directory, 0o700) != nil {
		return
	}
	staged, err := os.CreateTemp(directory, ".digest-*")
	if err != nil {
		return
	}
	_, writeErr := staged.Write(encodeDigestRecord(identity, signature))
	closeErr := staged.Close()
	if writeErr != nil || closeErr != nil || os.Rename(staged.Name(), name) != nil {
		os.Remove(staged.Name())
	}
}
