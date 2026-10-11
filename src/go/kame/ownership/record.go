package ownership

import (
	"kame/core"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// Records have a bounded binary envelope and a SHA-256 checksum. Paths and
// provenance are length-prefixed, so newlines and spaces never delimit members.
type record struct {
	Root     [16]byte
	Context  [32]byte
	Stamp    [48]byte
	Digest   [32]byte
	Accepted bool
	Path     string
	Producer string
	Batch    string
}

func (r *record) free(a mem.Allocator) {
	mem.FreeString(a, r.Path)
	mem.FreeString(a, r.Producer)
	mem.FreeString(a, r.Batch)
}

func putText(a mem.Allocator, data []byte, text string) []byte {
	for i := 0; i < 8; i++ {
		data = slices.Append(a, data, byte(uint64(len(text))>>uint(i*8)))
	}
	for i := range text {
		data = slices.Append(a, data, text[i])
	}
	return data
}

func (s *Store) encode(r *record) []byte {
	data := slices.Make[byte](s.Alloc, 133)
	copy(data, "KO01")
	copy(data[4:20], r.Root[:])
	copy(data[20:52], r.Context[:])
	copy(data[52:100], r.Stamp[:])
	copy(data[100:132], r.Digest[:])
	if r.Accepted {
		data[132] = 1
	}
	data = putText(s.Alloc, data, r.Path)
	data = putText(s.Alloc, data, r.Producer)
	data = putText(s.Alloc, data, r.Batch)
	checksum := hash(data)
	for i := range checksum.Digest {
		data = slices.Append(s.Alloc, data, checksum.Digest[i])
	}
	return data
}

func takeText(a mem.Allocator, data []byte, offset *int) string {
	if *offset < 0 || *offset+8 > len(data) {
		*offset = -1
		return ""
	}
	var n uint64
	for i := 0; i < 8; i++ {
		n |= uint64(data[*offset+i]) << uint(i*8)
	}
	*offset += 8
	if n > uint64(len(data)-*offset) {
		*offset = -1
		return ""
	}
	text := clone(a, string(data[*offset:*offset+int(n)]))
	*offset += int(n)
	return text
}

func (s *Store) decode(data []byte) record {
	result := record{}
	s.Status = -3
	if len(data) < 189 || string(data[:4]) != "KO01" || data[132] > 1 {
		return result
	}
	checksum := hash(data[:len(data)-32])
	for i := range checksum.Digest {
		if checksum.Digest[i] != data[len(data)-32+i] {
			return result
		}
	}
	copy(result.Root[:], data[4:20])
	copy(result.Context[:], data[20:52])
	copy(result.Stamp[:], data[52:100])
	copy(result.Digest[:], data[100:132])
	result.Accepted = data[132] == 1
	offset := 133
	result.Path = takeText(s.Alloc, data[:len(data)-32], &offset)
	result.Producer = takeText(s.Alloc, data[:len(data)-32], &offset)
	result.Batch = takeText(s.Alloc, data[:len(data)-32], &offset)
	if offset != len(data)-32 || !safeRecordPath(result.Path) || !safeKey(result.Batch) {
		return result
	}
	s.Status = 0
	return result
}

func safeKey(text string) bool {
	if len(text) != 64 {
		return false
	}
	for i := range text {
		if !(text[i] >= '0' && text[i] <= '9') && !(text[i] >= 'a' && text[i] <= 'f') {
			return false
		}
	}
	return true
}

func safeMember(text string) bool {
	if text == "" || text[0] == '/' || strings.HasPrefix(text, ".kame/") || text == ".kame" {
		return false
	}
	start := 0
	for i := 0; i <= len(text); i++ {
		if i < len(text) && text[i] != '/' {
			if text[i] == 0 || text[i] == '\\' {
				return false
			}
			continue
		}
		part := text[start:i]
		if part == "" || part == "." || part == ".." {
			return false
		}
		start = i + 1
	}
	return true
}

func safeRecordPath(text string) bool {
	if safeMember(text) {
		return true
	}
	if text == "" || strings.Contains(text, "\x00") || strings.Contains(text, "\\") {
		return false
	}
	// Absolute/external resource identities are inventory-only. They never reach
	// an artifact lookup or removal operation, regardless of their record state.
	return path.IsAbs(text) || core.IsResourceURIName(text)
}

func (s *Store) recordPath(name string) string {
	member := s.Member(name)
	if member != "" {
		return member
	}
	if core.IsResourceURIName(name) {
		parsed := core.ParseResourceURI(s.Alloc, name)
		if parsed.Error != "" {
			parsed.URI.Free()
			return ""
		}
		if parsed.URI.Scheme == "file" {
			physical := clone(s.Alloc, parsed.URI.Path)
			parsed.URI.Free()
			result := s.recordPath(physical)
			mem.FreeString(s.Alloc, physical)
			return result
		}
		result := parsed.URI.Canonical(s.Alloc)
		parsed.URI.Free()
		return result
	}
	if path.IsAbs(name) {
		return path.Clean(s.Alloc, name)
	}
	physical := path.Join(s.Alloc, s.Root, name)
	if !safeRecordPath(physical) {
		mem.FreeString(s.Alloc, physical)
		return ""
	}
	return physical
}

// Member converts a physical absolute/relative path to a checked root-relative
// name. URI decoding belongs to the caller; resource URI text is never a path.
func (s *Store) Member(name string) string {
	if strings.HasPrefix(name, "file://") {
		parsed := core.ParseResourceURI(s.Alloc, name)
		if parsed.Error != "" {
			parsed.URI.Free()
			return ""
		}
		physical := clone(s.Alloc, parsed.URI.Path)
		parsed.URI.Free()
		result := s.Member(physical)
		mem.FreeString(s.Alloc, physical)
		return result
	}
	if strings.Contains(name, "://") {
		return ""
	}
	var cleaned string
	if path.IsAbs(name) {
		cleaned = path.Clean(s.Alloc, name)
	} else {
		cleaned = path.Join(s.Alloc, s.Root, name)
	}
	defer mem.FreeString(s.Alloc, cleaned)
	if len(cleaned) <= len(s.Root)+1 || !strings.HasPrefix(cleaned, s.Root) || cleaned[len(s.Root)] != '/' {
		return ""
	}
	member := cleaned[len(s.Root)+1:]
	if !safeMember(member) {
		return ""
	}
	return clone(s.Alloc, member)
}

func (s *Store) writeRecord(r *record) bool {
	data := s.encode(r)
	name := key(s.Alloc, r.Path)
	s.Status = int(km_own_write(s.native, name+".record", data))
	mem.FreeString(s.Alloc, name)
	slices.Free(s.Alloc, data)
	return s.Status == 0
}

func (s *Store) Begin(paths []string, producer string, context [32]byte) bool {
	if !s.Acquire() {
		return false
	}
	joined := strings.Join(s.Alloc, paths, "\x00")
	batch := key(s.Alloc, joined)
	mem.FreeString(s.Alloc, joined)
	defer mem.FreeString(s.Alloc, batch)
	// The batch marker keeps a partially committed grouped publication uncertain.
	if km_own_write(s.native, batch+".batch", []byte("pending")) != 0 {
		s.Status = -1
		return false
	}
	for i := range paths {
		member := s.recordPath(paths[i])
		if member == "" {
			continue
		}
		name := key(s.Alloc, member)
		pending := s.read(name + ".recovery")
		slices.Free(s.Alloc, pending)
		mem.FreeString(s.Alloc, name)
		if s.Status != 1 {
			mem.FreeString(s.Alloc, member)
			s.Status = -3
			return false
		}
		r := record{Path: member, Producer: producer, Batch: batch}
		r.Root, r.Context = s.ID, context
		ok := s.writeRecord(&r)
		mem.FreeString(s.Alloc, member)
		if !ok {
			return false
		}
	}
	return true
}

func (s *Store) Commit(paths []string, producer string, context [32]byte) bool {
	joined := strings.Join(s.Alloc, paths, "\x00")
	batch := key(s.Alloc, joined)
	mem.FreeString(s.Alloc, joined)
	defer mem.FreeString(s.Alloc, batch)
	var observations []observation
	for i := range paths {
		member := s.Member(paths[i])
		item := observation{Status: 1}
		if member != "" {
			item = s.observe(member, true)
		}
		mem.FreeString(s.Alloc, member)
		observations = slices.Append(s.Alloc, observations, item)
		if item.Status != 0 && item.Status != 1 {
			slices.Free(s.Alloc, observations)
			// A symlink/non-regular output can satisfy ordinary publication, but
			// cannot establish cleanup ownership. Leave its attempt uncertain.
			s.Status = item.Status
			if s.Status == -3 {
				s.Status = 2
			}
			return false
		}
		// Missing tracked writes are not accepted production. Out-of-root paths
		// likewise have no automatic cleanup authority.
		if item.Status == 1 && paths[i] != "" {
			member = s.Member(paths[i])
			missing := member != ""
			mem.FreeString(s.Alloc, member)
			if missing {
				slices.Free(s.Alloc, observations)
				s.Status = -1
				return false
			}
		}
	}
	for i := range paths {
		member := s.recordPath(paths[i])
		if member == "" {
			continue
		}
		r := record{Path: member, Producer: producer, Batch: batch, Accepted: true}
		if !safeMember(member) {
			r.Accepted = false
		}
		r.Root, r.Context, r.Stamp, r.Digest = s.ID, context, observations[i].Stamp, observations[i].Digest
		ok := s.writeRecord(&r)
		mem.FreeString(s.Alloc, member)
		if !ok {
			slices.Free(s.Alloc, observations)
			return false
		}
	}
	slices.Free(s.Alloc, observations)
	s.Status = int(km_own_remove(s.native, batch+".batch"))
	return s.Status == 0
}
