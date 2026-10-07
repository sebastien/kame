package program

// The task cache is deliberately local to the runtime.  It stores only a
// accepted signature record and replayable process output. Resource observations
// are validated by the portable engine, including reads made during execution.

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

const cacheFormat byte = 1
const cacheLogDefault = 64 * 1024
const cacheManifestMax = 16 * 1024 * 1024
const cacheRecordsPerBackend = 1024

type retainedCacheFile struct {
	Name string
	Time int64
}

// pruneCacheDirectory bounds persistent records by removing the least recently
// published regular files. Directory entries are sorted as a stable tie-break
// for hosts whose filesystem timestamps have coarse resolution.
func (p *Program) pruneCacheDirectory(directory string) {
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		return
	}
	var records []retainedCacheFile
	for i := range entries {
		if entries[i].IsDir || strings.HasPrefix(entries[i].Name, ".kame-write-") {
			continue
		}
		name := path.Join(p.Alloc, directory, entries[i].Name)
		info := p.Host.Lstat(name)
		mem.FreeString(p.Alloc, name)
		if info.Exists && info.Info.Regular {
			records = slices.Append(p.Alloc, records, retainedCacheFile{Name: entries[i].Name, Time: info.Info.ModTime})
		}
	}
	for i := 1; i < len(records); i++ {
		value := records[i]
		j := i
		for j > 0 && (records[j-1].Time > value.Time || (records[j-1].Time == value.Time && records[j-1].Name > value.Name)) {
			records[j] = records[j-1]
			j--
		}
		records[j] = value
	}
	remove := len(records) - cacheRecordsPerBackend
	for i := 0; i < remove; i++ {
		name := path.Join(p.Alloc, directory, records[i].Name)
		_ = p.Host.Remove(name)
		mem.FreeString(p.Alloc, name)
	}
	slices.Free(p.Alloc, records)
	for i := range entries {
		mem.FreeString(p.Alloc, entries[i].Name)
	}
	slices.Free(p.Alloc, entries)
}

type cacheRecord struct {
	Identity        string
	Fingerprint     [32]byte
	Schema          byte
	StartedAt       int64
	CompletedAt     int64
	Duration        int64
	ExitStatus      int64
	Manifest        []byte
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

func (r *cacheRecord) Free(a mem.Allocator) {
	mem.FreeString(a, r.Identity)
	slices.Free(a, r.Manifest)
	slices.Free(a, r.Stdout)
	slices.Free(a, r.Stderr)
	*r = cacheRecord{}
}

// Compatibility wrapper around the engine's shared digest primitive.
type sha256State struct {
	digest core.Digest
}

func newSHA256() sha256State {
	return sha256State{digest: core.NewDigest()}
}

func (s *sha256State) Write(data []byte) {
	s.digest.Write(data)
}

func (s *sha256State) Sum(out []byte) {
	s.digest.Sum(out)
}

func cacheHash(data []byte, out []byte) { s := newSHA256(); s.Write(data); s.Sum(out) }

// FingerprintSHA256 computes the cache's streaming SHA-256 digest. It returns
// false when output is too short; callers need not allocate or retain input.
func FingerprintSHA256(data []byte, output []byte) bool {
	if len(output) < 32 {
		return false
	}
	cacheHash(data, output[:32])
	return true
}

func appendU64(out []byte, n uint64) []byte {
	for i := 0; i < 8; i++ {
		out = slices.Append(mem.System, out, byte(n>>uint(i*8)))
	}
	return out
}

func resourceKindName(kind core.ResourceKind) string {
	if kind == core.ResourceDefinition {
		return "definition"
	}
	if kind == core.ResourceTarget {
		return "target"
	}
	if kind == core.ResourceFile {
		return "file"
	}
	if kind == core.ResourceTask {
		return "task"
	}
	if kind == core.ResourceService {
		return "service"
	}
	if kind == core.ResourceGlob {
		return "glob"
	}
	if kind == core.ResourceTool {
		return "tool"
	}
	return "environment"
}

// configuredEnvironment resolves a named read from the child snapshot.
func (p *Program) configuredEnvironment(name string) (string, bool) {
	for i := len(p.Options.Environment) - 1; i >= 0; i-- {
		entry := p.Options.Environment[i]
		if len(entry) > len(name) && entry[:len(name)] == name && entry[len(name)] == '=' {
			return entry[len(name)+1:], true
		}
	}
	return "", false
}

func cacheHex(a mem.Allocator, digest []byte) string {
	const hex = "0123456789abcdef"
	b := strings.NewBuilder(a)
	for i := 0; i < 32; i++ {
		b.WriteByte(hex[digest[i]>>4])
		b.WriteByte(hex[digest[i]&15])
	}
	out := cloneText(a, b.String())
	b.Free()
	return out
}
func FingerprintHex(digest []byte) string {
	const hex = "0123456789abcdef"
	b := strings.NewBuilder(mem.System)
	for i := range digest {
		b.WriteByte(hex[digest[i]>>4])
		b.WriteByte(hex[digest[i]&15])
	}
	result := cloneText(mem.System, b.String())
	b.Free()
	return result
}

func recordBytes(r *cacheRecord) []byte {
	var out []byte
	out = slices.Append(mem.System, out, 'L')
	out = slices.Append(mem.System, out, 'M')
	out = slices.Append(mem.System, out, 'K')
	out = slices.Append(mem.System, out, 'R')
	out = slices.Append(mem.System, out, cacheFormat)
	out = appendU64(out, uint64(len(r.Identity)))
	for i := range r.Identity {
		out = slices.Append(mem.System, out, r.Identity[i])
	}
	for i := range r.Fingerprint {
		out = slices.Append(mem.System, out, r.Fingerprint[i])
	}
	out = slices.Append(mem.System, out, r.Schema)
	out = appendU64(out, uint64(r.StartedAt))
	out = appendU64(out, uint64(r.CompletedAt))
	out = appendU64(out, uint64(r.Duration))
	out = appendU64(out, uint64(r.ExitStatus))
	out = appendU64(out, uint64(len(r.Manifest)))
	for i := range r.Manifest {
		out = slices.Append(mem.System, out, r.Manifest[i])
	}
	flags := byte(0)
	if r.StdoutTruncated {
		flags |= 1
	}
	if r.StderrTruncated {
		flags |= 2
	}
	out = slices.Append(mem.System, out, flags)
	out = appendU64(out, uint64(len(r.Stdout)))
	for i := range r.Stdout {
		out = slices.Append(mem.System, out, r.Stdout[i])
	}
	out = appendU64(out, uint64(len(r.Stderr)))
	for i := range r.Stderr {
		out = slices.Append(mem.System, out, r.Stderr[i])
	}
	return out
}
func takeU64(data []byte, at *int) uint64 {
	if *at+8 > len(data) {
		return 0
	}
	var n uint64
	for i := 0; i < 8; i++ {
		n |= uint64(data[*at+i]) << uint(i*8)
	}
	*at += 8
	return n
}
func parseRecord(a mem.Allocator, data []byte) cacheRecord {
	if len(data) < 5 || string(data[:4]) != "LMKR" || data[4] != cacheFormat {
		return cacheRecord{}
	}
	at := 5
	if at+8 > len(data) {
		return cacheRecord{}
	}
	n := takeU64(data, &at)
	if n == 0 || n > 1024*1024 || n > uint64(len(data)-at) {
		return cacheRecord{}
	}
	r := cacheRecord{Identity: cloneText(a, string(data[at:at+int(n)]))}
	at += int(n)
	if at+32+1+32+8 > len(data) {
		r.Free(a)
		return cacheRecord{}
	}
	for i := range r.Fingerprint {
		r.Fingerprint[i] = data[at+i]
	}
	at += 32
	r.Schema = data[at]
	if r.Schema != 1 {
		r.Free(a)
		return cacheRecord{}
	}
	at++
	r.StartedAt = int64(takeU64(data, &at))
	r.CompletedAt = int64(takeU64(data, &at))
	r.Duration = int64(takeU64(data, &at))
	r.ExitStatus = int64(takeU64(data, &at))
	// A forwarding host may not expose a synchronous clock, so a record can
	// carry zero timing. Ordering and a completed status are still required.
	if r.StartedAt < 0 || r.CompletedAt < r.StartedAt || r.Duration < 0 || r.ExitStatus != 0 {
		r.Free(a)
		return cacheRecord{}
	}
	n = takeU64(data, &at)
	if n == 0 || n > cacheManifestMax || n > uint64(len(data)-at) {
		r.Free(a)
		return cacheRecord{}
	}
	r.Manifest = slices.Clone(a, data[at:at+int(n)])
	at += int(n)
	if at >= len(data) {
		r.Free(a)
		return cacheRecord{}
	}
	flags := data[at]
	if flags&^byte(3) != 0 {
		r.Free(a)
		return cacheRecord{}
	}
	at++
	if at+8 > len(data) {
		r.Free(a)
		return cacheRecord{}
	}
	n = takeU64(data, &at)
	if n > uint64(len(data)-at) {
		r.Free(a)
		return cacheRecord{}
	}
	r.Stdout = slices.Clone(a, data[at:at+int(n)])
	r.StdoutTruncated = flags&1 != 0
	at += int(n)
	if at+8 > len(data) {
		r.Free(a)
		return cacheRecord{}
	}
	n = takeU64(data, &at)
	if n != uint64(len(data)-at) {
		r.Free(a)
		return cacheRecord{}
	}
	r.Stderr = slices.Clone(a, data[at:])
	r.StderrTruncated = flags&2 != 0
	return r
}

func (p *Program) cacheLoad(entry *instance, fingerprint []byte) cacheLookupResult {
	name := p.cachePath(entry)
	info := p.Host.Stat(name)
	maxLog := p.Options.CacheRetainBytes
	if maxLog < cacheLogDefault {
		maxLog = cacheLogDefault
	}
	maxRecord := int64(cacheManifestMax) + int64(maxLog)*2 + 1024*1024
	if !info.Exists {
		mem.FreeString(p.Alloc, name)
		if info.Failed {
			return cacheLookupResult{MissReason: "proof-unverifiable"}
		}
		return cacheLookupResult{MissReason: "record-missing"}
	}
	if info.Info.Size < 0 || info.Info.Size > maxRecord {
		p.cacheWarning(entry, "CACHE_RECORD", "cache record exceeds the supported size")
		mem.FreeString(p.Alloc, name)
		return cacheLookupResult{MissReason: "record-invalid"}
	}
	data, err := p.Host.ReadFile(p.Alloc, name)
	mem.FreeString(p.Alloc, name)
	if err != nil {
		return cacheLookupResult{MissReason: "proof-unverifiable"}
	}
	r := p.validateRecord(entry, fingerprint, data)
	mem.FreeSlice(p.Alloc, data)
	return r
}

// validateRecord parses and validates one encoded cache record. A malformed,
// stale, or mismatched record is freed, preserving the established miss reason.
func (p *Program) validateRecord(entry *instance, fingerprint []byte, data []byte) cacheLookupResult {
	r := parseRecord(p.Alloc, data)
	if r.Identity == "" {
		p.cacheWarning(entry, "CACHE_RECORD", "malformed or unsupported cache record")
		return cacheLookupResult{MissReason: "record-invalid"}
	}
	identity := p.cacheIdentity(entry)
	var digest [32]byte
	if len(r.Manifest) != 0 {
		cacheHash(r.Manifest, digest[:])
	}
	_ = fingerprint
	if r.Identity == "" || r.Identity != identity || string(r.Fingerprint[:]) != string(digest[:]) {
		r.Free(p.Alloc)
		mem.FreeString(p.Alloc, identity)
		return cacheLookupResult{MissReason: "record-invalid"}
	}
	mem.FreeString(p.Alloc, identity)
	return cacheLookupResult{Record: r, Hit: true}
}

// cacheLookupResult reports a cached record, a hit, or a pending forwarded
// lookup the producer must resume.
type cacheLookupResult struct {
	Record  cacheRecord
	Hit     bool
	Waiting bool
	// MissReason borrows a stable code; classification adds no host observations.
	MissReason string
}

// cacheLookup returns the cached record for a task, or reports that a forwarded
// lookup was submitted and the producer must resume. The file host answers
// synchronously; a forwarding host owns persistence and answers through the
// cache request kinds.
func (p *Program) cacheLookup(c *core.EngineContext, entry *instance) cacheLookupResult {
	if !p.Forwarding {
		key := p.cacheKey(entry)
		stripe := int(key[0])
		lockPath := p.cacheLockPath(key)
		mem.FreeSlice(p.Alloc, key)
		locked := p.mkdirParent(lockPath) && p.Host.LockCache(lockPath, stripe)
		mem.FreeString(p.Alloc, lockPath)
		if !locked {
			entry.CacheReady = false
			p.cacheWarning(entry, "CACHE_LOCK", "cache miss lock unavailable; running without cache coordination")
			return cacheLookupResult{MissReason: "proof-unverifiable"}
		}
		entry.cacheLockHeld, entry.cacheLockStripe = true, stripe
		return p.cacheLoad(entry, entry.CacheFingerprint[:])
	}
	if entry.cachePending {
		entry.cachePending = false
		completion := c.Completion()
		if completion.RequestID == 0 {
			return cacheLookupResult{Waiting: true}
		}
		if entry.cacheWaitingLock {
			entry.cacheWaitingLock = false
			if completion.Diagnostic.Code != "" {
				completion.Diagnostic.Free(p.Alloc)
				completion.Value.Free(p.Alloc)
				p.releaseCacheLock(entry)
				entry.CacheReady = false
				return cacheLookupResult{MissReason: "proof-unverifiable"}
			}
			completion.Diagnostic.Free(p.Alloc)
			completion.Value.Free(p.Alloc)
			return p.cacheLookupForwarded(c, entry)
		}
		if completion.Diagnostic.Code != "" {
			completion.Diagnostic.Free(p.Alloc)
			completion.Value.Free(p.Alloc)
			p.releaseCacheLock(entry)
			entry.CacheReady = false
			return cacheLookupResult{MissReason: "proof-unverifiable"}
		}
		if completion.Value.Kind == core.Nil {
			completion.Value.Free(p.Alloc)
			return cacheLookupResult{MissReason: "record-missing"}
		}
		if completion.Value.Kind != core.Bytes {
			completion.Value.Free(p.Alloc)
			p.releaseCacheLock(entry)
			entry.CacheReady = false
			return cacheLookupResult{MissReason: "proof-unverifiable"}
		}
		result := p.validateRecord(entry, entry.CacheFingerprint[:], completion.Value.Bytes)
		completion.Value.Free(p.Alloc)
		return result
	}
	key := p.cacheKey(entry)
	p.nextRequest++
	entry.cacheLockStripe = int(key[0])
	entry.cacheLockHeld, entry.cachePending, entry.cacheWaitingLock = true, true, true
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestCacheLock, Payload: host.CacheGetPayload(p.Alloc, key)})
	mem.FreeSlice(p.Alloc, key)
	c.Submit(p.nextRequest)
	return cacheLookupResult{Waiting: true}
}

func (p *Program) cacheLookupForwarded(c *core.EngineContext, entry *instance) cacheLookupResult {
	key := p.cacheKey(entry)
	payload := host.CacheGetPayload(p.Alloc, key)
	mem.FreeSlice(p.Alloc, key)
	p.nextRequest++
	p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, NodeID: c.NodeID(), Generation: c.Generation(), Attempt: c.Attempt(), Kind: host.RequestCacheGet, Payload: payload})
	c.Submit(p.nextRequest)
	entry.cachePending = true
	return cacheLookupResult{Waiting: true}
}

func (p *Program) releaseCacheLock(entry *instance) {
	if entry == nil || !entry.cacheLockHeld {
		return
	}
	if p.Forwarding {
		key := p.cacheKey(entry)
		payload := host.CacheGetPayload(p.Alloc, key)
		mem.FreeSlice(p.Alloc, key)
		p.nextRequest++
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestCacheUnlock, Payload: payload})
	} else {
		p.Host.UnlockCache(entry.cacheLockStripe)
	}
	entry.cacheLockHeld = false
}

func (p *Program) cacheWarning(entry *instance, code string, message string) {
	if !p.Options.Verbose {
		return
	}
	target := ""
	if entry != nil {
		target = entry.Plan.Target
	}
	p.emit(Event{Kind: CacheWarning, Target: target, Diagnostic: diagnostic.Diagnostic{Code: code, Severity: diagnostic.Warning, Message: message}})
}
func (p *Program) cacheSave(entry *instance, r *cacheRecord) bool {
	if p.Forwarding {
		key := p.cacheKey(entry)
		data := recordBytes(r)
		payload := host.CachePutPayload(p.Alloc, key, data)
		mem.FreeSlice(p.Alloc, key)
		mem.FreeSlice(mem.System, data)
		p.nextRequest++
		p.Outbound = slices.Append(p.Alloc, p.Outbound, host.Request{ID: p.nextRequest, Kind: host.RequestCachePut, Payload: payload})
		return true
	}
	name := p.cachePath(entry)
	if !p.mkdirParent(name) {
		mem.FreeString(p.Alloc, name)
		return false
	}
	data := recordBytes(r)
	ok := p.Host.WriteFileAtomic(name, data, 0o644, true) == nil
	mem.FreeSlice(mem.System, data)
	mem.FreeString(p.Alloc, name)
	if ok {
		directory := path.Join(p.Alloc, p.Options.Directory, ".kame/cache/tasks")
		p.pruneCacheDirectory(directory)
		mem.FreeString(p.Alloc, directory)
	}
	return ok
}
