package posix

import (
	"kame/core"
	"strconv"
	"testing"
)

func TestDigestMemoBoundedAcrossPaths(t *testing.T) {
	var m digestMemo
	key := ""
	for i := range maxDigestEntries + 100 {
		key = strconv.Itoa(i)
		m.remember(key, fileIdentity{}, core.Signature{Mode: core.SignatureContent})
	}
	if len(m.entries) != maxDigestEntries || m.entries[key].Signature.Mode != core.SignatureContent {
		t.Fatal("digest memo failed to bound old paths and retain the latest digest")
	}
}
