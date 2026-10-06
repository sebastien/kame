package program

// Manifest encoding is one budgeted buffer. Identity, rule text, nested
// definition values, and section wrapping are charged as they are written, and
// the backing store never grows past the cap. Overflow frees that buffer.

import (
	"kame/core"
	"kame/lang/rule"
	"solod.dev/so/math"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

// manifestSink is the shared writer for identity bytes. The manifest encoder
// charges them against the cap; the path hasher consumes them without retaining
// the text.
type manifestSink interface {
	appendByte(byte) bool
	appendU64(uint64) bool
	appendText(string) bool
	writeRaw(string) bool
}

type cacheEncoder struct {
	alloc    mem.Allocator
	buf      []byte
	limit    int
	exceeded bool
	unusable bool
}

func newCacheEncoder(a mem.Allocator, limit int) cacheEncoder {
	if limit <= 0 {
		limit = cacheManifestMax
	}
	return cacheEncoder{alloc: a, limit: limit}
}

func (e *cacheEncoder) free() {
	if cap(e.buf) != 0 {
		mem.FreeSlice(e.alloc, e.buf)
	}
	e.buf = nil
}

func (e *cacheEncoder) reserve(n int) bool {
	if e.exceeded || n < 0 {
		e.exceeded = true
		return false
	}
	need := len(e.buf) + n
	if need < len(e.buf) || need > e.limit {
		e.exceeded = true
		return false
	}
	if cap(e.buf) >= need {
		return true
	}
	newCap := need
	if cap(e.buf) > 0 {
		doubled := cap(e.buf) + cap(e.buf)
		if doubled > newCap && doubled > 0 {
			newCap = doubled
		}
	}
	if newCap > e.limit || newCap < need {
		newCap = e.limit
	}
	if newCap < need {
		e.exceeded = true
		return false
	}
	next, err := mem.TryReallocSlice(e.alloc, e.buf, len(e.buf), newCap)
	if err != nil {
		e.exceeded = true
		return false
	}
	e.buf = next
	return true
}

func (e *cacheEncoder) writeByte(value byte) {
	n := len(e.buf)
	e.buf = e.buf[:n+1]
	e.buf[n] = value
}

func (e *cacheEncoder) writeU64(value uint64) {
	for i := 0; i < 8; i++ {
		e.writeByte(byte(value >> uint(i*8)))
	}
}

func (e *cacheEncoder) appendByte(value byte) bool {
	if !e.reserve(1) {
		return false
	}
	e.writeByte(value)
	return true
}

func (e *cacheEncoder) appendU64(value uint64) bool {
	if !e.reserve(8) {
		return false
	}
	e.writeU64(value)
	return true
}

func (e *cacheEncoder) appendText(value string) bool {
	if !e.reserve(9 + len(value)) {
		return false
	}
	e.writeByte(5)
	e.writeU64(uint64(len(value)))
	for i := range value {
		e.writeByte(value[i])
	}
	return true
}

func (e *cacheEncoder) appendBytes(value []byte) bool {
	if !e.reserve(9 + len(value)) {
		return false
	}
	e.writeByte(6)
	e.writeU64(uint64(len(value)))
	for i := range value {
		e.writeByte(value[i])
	}
	return true
}

func (e *cacheEncoder) writeRaw(value string) bool {
	if !e.reserve(len(value)) {
		return false
	}
	for i := range value {
		e.writeByte(value[i])
	}
	return true
}

type hashSink struct{ state sha256State }

func (h *hashSink) appendByte(value byte) bool {
	h.state.Write([]byte{value})
	return true
}

func (h *hashSink) appendU64(value uint64) bool {
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(value >> uint(i*8))
	}
	h.state.Write(buf[:])
	return true
}

func (h *hashSink) appendText(value string) bool {
	if !h.appendByte(5) || !h.appendU64(uint64(len(value))) {
		return false
	}
	h.state.Write([]byte(value))
	return true
}

func (h *hashSink) writeRaw(value string) bool {
	h.state.Write([]byte(value))
	return true
}

func (p *Program) manifestLimit() int {
	if p.Options.CacheManifestMax > 0 && p.Options.CacheManifestMax < cacheManifestMax {
		return p.Options.CacheManifestMax
	}
	return cacheManifestMax
}

func (p *Program) identityTarget(entry *instance) string {
	target := entry.Plan.Key.Name
	if entry.Plan.Target != "" {
		target = entry.Plan.Target
	}
	if isPathTarget(target) {
		return p.canonicalTarget(target, true)
	}
	return cloneText(p.Alloc, target)
}

func formattedRuleLen(r *rule.Rule) int {
	if r == nil {
		return 0
	}
	n := 0
	if r.Kind == rule.CachedTaskRule {
		n += len("task ")
	}
	if r.Kind == rule.ServiceRule {
		n += len("service ")
	}
	for i := range r.Outputs {
		if i != 0 {
			n++
		}
		n += len(r.Outputs[i].Text)
	}
	n += len(" :")
	for i := range r.Inputs {
		n += 1 + len(r.Inputs[i].Text)
	}
	for i := range r.Body {
		n += len("\n\t") + len(r.Body[i].Text)
	}
	return n
}

func writeRulePieces(w manifestSink, r *rule.Rule) bool {
	if r == nil {
		return true
	}
	if r.Kind == rule.CachedTaskRule && !w.writeRaw("task ") {
		return false
	}
	if r.Kind == rule.ServiceRule && !w.writeRaw("service ") {
		return false
	}
	for i := range r.Outputs {
		if i != 0 && !w.writeRaw(" ") {
			return false
		}
		if !w.writeRaw(r.Outputs[i].Text) {
			return false
		}
	}
	if !w.writeRaw(" :") {
		return false
	}
	for i := range r.Inputs {
		if !w.writeRaw(" ") || !w.writeRaw(r.Inputs[i].Text) {
			return false
		}
	}
	for i := range r.Body {
		if !w.writeRaw("\n\t") || !w.writeRaw(r.Body[i].Text) {
			return false
		}
	}
	return true
}

func appendFormattedRule(w manifestSink, r *rule.Rule) bool {
	if !w.appendByte(5) || !w.appendU64(uint64(formattedRuleLen(r))) {
		return false
	}
	return writeRulePieces(w, r)
}

func writeIdentity(w manifestSink, entry *instance, target string) bool {
	if !w.appendByte('L') || !w.appendByte('M') || !w.appendByte('K') || !w.appendByte('I') || !w.appendByte(1) {
		return false
	}
	if !w.appendText(target) || !w.appendU64(uint64(len(entry.Captures))) {
		return false
	}
	for i := range entry.Captures {
		if !w.appendText(entry.Captures[i].Name) || !w.appendText(entry.Captures[i].Text) {
			return false
		}
	}
	return appendFormattedRule(w, entry.Rule)
}

func (p *Program) cacheIdentity(entry *instance) string {
	target := p.identityTarget(entry)
	enc := newCacheEncoder(p.Alloc, p.manifestLimit())
	ok := writeIdentity(&enc, entry, target)
	mem.FreeString(p.Alloc, target)
	if !ok || enc.exceeded {
		enc.free()
		return ""
	}
	identity := cloneText(p.Alloc, string(enc.buf))
	enc.free()
	return identity
}

func (p *Program) cachePath(entry *instance) string {
	key := p.cacheKey(entry)
	hex := cacheHex(p.Alloc, key)
	mem.FreeSlice(p.Alloc, key)
	root := path.Join(p.Alloc, p.Options.Directory, ".kame/cache/tasks")
	result := path.Join(p.Alloc, root, hex+".kmkr")
	mem.FreeString(p.Alloc, hex)
	mem.FreeString(p.Alloc, root)
	return result
}

func (p *Program) cacheLockPath(key []byte) string {
	const digits = "0123456789abcdef"
	name := slices.Make[byte](p.Alloc, 2)
	name[0], name[1] = digits[key[0]>>4], digits[key[0]&15]
	stripe := string(name)
	result := path.Join(p.Alloc, p.Options.Directory, ".kame/cache/locks", stripe+".lock")
	mem.FreeString(p.Alloc, stripe)
	return result
}

// cacheKey returns the identity digest that names a cached task's record. The
// host treats it as an opaque byte key, so the same key selects the same record
// on the file host and on a forwarding host.
func (p *Program) cacheKey(entry *instance) []byte {
	target := p.identityTarget(entry)
	var sink hashSink
	sink.state = newSHA256()
	writeIdentity(&sink, entry, target)
	mem.FreeString(p.Alloc, target)
	var digest [32]byte
	sink.state.Sum(digest[:])
	return slices.Clone(p.Alloc, digest[:])
}

func (p *Program) cacheFingerprint(entry *instance, script string) bool {
	_ = script
	core.FreeObservations(p.Alloc, entry.AcceptedRecord.Inputs)
	entry.AcceptedRecord.Inputs = p.acceptedInputs(entry)
	data := core.EncodeSignatureRecord(p.Alloc, &entry.AcceptedRecord)
	if len(data) > p.manifestLimit() {
		slices.Free(p.Alloc, data)
		p.cacheWarning(entry, "CACHE_UNUSABLE", "dependency manifest exceeds the cache size limit")
		return false
	}
	if len(data) == 0 {
		p.cacheWarning(entry, "CACHE_UNUSABLE", "one or more dependencies could not be fingerprinted reliably")
		return false
	}
	cacheHash(data, entry.CacheFingerprint[:])
	slices.Free(p.Alloc, entry.CacheManifest)
	entry.CacheManifest = data
	return true
}

func (e *cacheEncoder) appendValue(value core.Value) {
	if e.exceeded {
		return
	}
	if value.Kind == core.Callable && value.CallableOwner != nil && value.Text == "kash" {
		e.appendByte(11)
		e.appendText("kash")
		return
	}
	if value.Kind == core.Nil {
		e.appendByte(0)
		return
	}
	if value.Kind == core.Bool {
		if value.Bool {
			e.appendByte(2)
			return
		}
		e.appendByte(1)
		return
	}
	if value.Kind == core.Int {
		e.appendByte(3)
		e.appendU64(uint64(value.Int))
		return
	}
	if value.Kind == core.Float {
		bits64 := math.Float64bits(value.Float)
		if bits64&0x7fffffffffffffff == 0 {
			bits64 = 0
		}
		if bits64&0x7ff0000000000000 == 0x7ff0000000000000 && bits64&0x000fffffffffffff != 0 {
			bits64 = 0x7ff8000000000000
		}
		e.appendByte(4)
		e.appendU64(bits64)
		return
	}
	if value.Kind == core.String {
		e.appendText(value.Text)
		return
	}
	if value.Kind == core.Pattern {
		e.appendByte(10)
		e.appendText(value.Text)
		return
	}
	if value.Kind == core.Bytes {
		e.appendBytes(value.Bytes)
		return
	}
	if value.Kind == core.Resource {
		e.appendByte(9)
		e.appendText(resourceKindName(value.Resource.Kind))
		e.appendText(value.Resource.Name)
		return
	}
	if value.Kind == core.List {
		e.appendByte(7)
		e.appendU64(uint64(len(value.List)))
		for i := range value.List {
			e.appendValue(value.List[i])
		}
		return
	}
	if value.Kind == core.Record {
		e.appendByte(8)
		e.appendU64(uint64(len(value.Record)))
		order := make([]int, len(value.Record))
		for i := range order {
			order[i] = i
		}
		for i := 1; i < len(order); i++ {
			pick := order[i]
			j := i - 1
			for j >= 0 && value.Record[order[j]].Key > value.Record[pick].Key {
				order[j+1] = order[j]
				j--
			}
			order[j+1] = pick
		}
		for i := range order {
			field := value.Record[order[i]]
			e.appendText(field.Key)
			e.appendValue(field.Value)
		}
		return
	}
	e.appendByte(0)
}

// EncodeFingerprintValue returns the canonical tagged encoding of one value.
// The returned bytes are owned by allocator a.
func EncodeFingerprintValue(a mem.Allocator, value core.Value) []byte {
	enc := newCacheEncoder(mem.System, cacheManifestMax)
	enc.appendValue(value)
	if enc.exceeded {
		enc.free()
		return nil
	}
	result := slices.Clone(a, enc.buf)
	enc.free()
	return result
}
