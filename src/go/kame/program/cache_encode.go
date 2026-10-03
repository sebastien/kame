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

const cacheFileMissing byte = 0
const cacheFileRegular byte = 1

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

func (e *cacheEncoder) beginBytes() (int, int) {
	if !e.appendByte(6) {
		return -1, 0
	}
	at := len(e.buf)
	if !e.appendU64(0) {
		return -1, 0
	}
	return at, len(e.buf)
}

func (e *cacheEncoder) beginText() (int, int) {
	if !e.appendByte(5) {
		return -1, 0
	}
	at := len(e.buf)
	if !e.appendU64(0) {
		return -1, 0
	}
	return at, len(e.buf)
}

func (e *cacheEncoder) patchU64(at int, start int) {
	if at < 0 || e.exceeded {
		return
	}
	n := uint64(len(e.buf) - start)
	for i := 0; i < 8; i++ {
		e.buf[at+i] = byte(n >> uint(i*8))
	}
}

func (e *cacheEncoder) beginSection(key string) (int, int) {
	if !e.appendText(key) {
		return -1, 0
	}
	return e.beginBytes()
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
	enc := newCacheEncoder(p.Alloc, p.manifestLimit())
	p.encodeManifest(&enc, entry, script)
	if enc.exceeded {
		enc.free()
		p.cacheWarning(entry, "CACHE_UNUSABLE", "dependency manifest exceeds the cache size limit")
		return false
	}
	if enc.unusable {
		enc.free()
		p.cacheWarning(entry, "CACHE_UNUSABLE", "one or more dependencies could not be fingerprinted reliably")
		return false
	}
	cacheHash(enc.buf, entry.CacheFingerprint[:])
	slices.Free(p.Alloc, entry.CacheManifest)
	entry.CacheManifest = enc.buf
	enc.buf = nil
	return true
}

func (p *Program) encodeManifest(e *cacheEncoder, entry *instance, script string) {
	e.appendByte('L')
	e.appendByte('M')
	e.appendByte('K')
	e.appendByte('F')
	e.appendByte(cacheFormat)
	e.appendByte(8)
	e.appendU64(8)
	appendCaptureSection(e, entry)
	p.appendDynamicSection(e, entry)
	p.appendExecutionSection(e, script)
	appendFormatSection(e)
	p.appendInputSection(e, entry)
	appendOperationSection(e, entry)
	appendRuleSection(e, entry)
	p.appendTaskSection(e, entry)
}

func appendCaptureSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("captures")
	e.appendU64(uint64(len(entry.Captures)))
	for i := range entry.Captures {
		e.appendText(entry.Captures[i].Name)
		e.appendText(entry.Captures[i].Text)
	}
	e.patchU64(at, start)
}

func (p *Program) appendDynamicSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("dynamic")
	e.appendU64(uint64(len(entry.Node.Dynamic)))
	order := make([]int, len(entry.Node.Dynamic))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		pick := order[i]
		j := i - 1
		for j >= 0 {
			left, right := entry.Node.Dynamic[order[j]].Key, entry.Node.Dynamic[pick].Key
			if left.Kind < right.Kind || (left.Kind == right.Kind && left.Name <= right.Name) {
				break
			}
			order[j+1] = order[j]
			j--
		}
		order[j+1] = pick
	}
	for n := range order {
		if e.exceeded {
			break
		}
		dependency := entry.Node.Dynamic[order[n]]
		key := dependency.Key
		e.appendByte(byte(key.Kind))
		e.appendText(key.Name)
		if key.Kind == core.ResourceFile {
			canonical := p.canonicalTarget(key.Name, true)
			p.appendLiveFile(e, canonical)
			mem.FreeString(p.Alloc, canonical)
		} else if key.Kind == core.ResourceGlob {
			var fp [32]byte
			if !p.globFingerprint(key.Name, fp[:]) {
				e.unusable = true
			}
			e.appendBytes(fp[:])
		} else if key.Kind == core.ResourceEnvironment {
			env, ok := p.configuredEnvironment(key.Name)
			if ok {
				e.appendByte(1)
				e.appendText(env)
			} else {
				e.appendByte(0)
			}
		} else if key.Kind == core.ResourceTool {
			e.appendValue(dependency.Latest)
		} else if key.Kind == core.ResourceDefinition {
			p.appendDefinitionDependency(e, dependency, 0)
		} else if key.Kind == core.ResourceTask {
			index := p.instanceIndex(dependency)
			if index >= 0 {
				e.appendBytes(p.Instances[index].CacheFingerprint[:])
			} else {
				e.unusable = true
				e.appendByte(0)
			}
		}
	}
	e.patchU64(at, start)
}

func (p *Program) appendExecutionSection(e *cacheEncoder, script string) {
	at, start := e.beginSection("execution")
	e.appendU64(uint64(len(p.Options.Shell)))
	for i := range p.Options.Shell {
		e.appendText(p.Options.Shell[i])
	}
	e.appendText(p.Options.Directory)
	e.appendU64(uint64(len(p.Options.Environment)))
	for i := range p.Options.Environment {
		e.appendText(p.Options.Environment[i])
	}
	e.appendText(script)
	e.appendU64(uint64(p.Options.TimeoutMS))
	e.appendU64(uint64(p.Options.RetryCount))
	e.patchU64(at, start)
}

func appendFormatSection(e *cacheEncoder) {
	at, start := e.beginSection("format")
	e.appendByte(3)
	e.appendU64(uint64(cacheFormat))
	e.patchU64(at, start)
}

func (p *Program) appendInputSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("inputs")
	inputs, resources := entry.Plan.Inputs, entry.Plan.ResourceInputs
	if entry.Plan.Resolved {
		inputs, resources = entry.Plan.ResolvedInputs, entry.Plan.ResolvedResourceInputs
	}
	e.appendU64(uint64(len(inputs)))
	for i := range inputs {
		if e.exceeded {
			break
		}
		kind := core.ResourceTarget
		name := inputs[i]
		if i < len(resources) {
			kind = resources[i].Key.Kind
			name = resources[i].Key.Name
		}
		file := kind == core.ResourceFile || (kind == core.ResourceTarget && isFileName(name))
		if file {
			canonical := p.canonicalTarget(name, true)
			e.appendByte(byte(kind))
			e.appendText(canonical)
			p.appendLiveFile(e, canonical)
			mem.FreeString(p.Alloc, canonical)
			continue
		}
		e.appendByte(byte(kind))
		e.appendText(name)
	}
	e.patchU64(at, start)
}

func appendOperationSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("operations")
	e.appendU64(uint64(len(entry.Operations)))
	order := make([]int, len(entry.Operations))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		pick := order[i]
		j := i - 1
		for j >= 0 && entry.Operations[order[j]] > entry.Operations[pick] {
			order[j+1] = order[j]
			j--
		}
		order[j+1] = pick
	}
	for i := range order {
		if e.exceeded {
			break
		}
		e.appendText(entry.Operations[order[i]])
	}
	e.patchU64(at, start)
}

func appendRuleSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("rule")
	appendFormattedRule(e, entry.Rule)
	e.patchU64(at, start)
}

func (p *Program) appendTaskSection(e *cacheEncoder, entry *instance) {
	at, start := e.beginSection("task")
	textAt, textStart := e.beginText()
	target := p.identityTarget(entry)
	writeIdentity(e, entry, target)
	mem.FreeString(p.Alloc, target)
	e.patchU64(textAt, textStart)
	e.patchU64(at, start)
}

func (p *Program) appendLiveFile(e *cacheEncoder, name string) {
	var digest [32]byte
	finger := p.fileFingerprint(name, digest[:])
	if finger.Missing {
		e.appendByte(cacheFileMissing)
		return
	}
	if !finger.Ready {
		e.unusable = true
	}
	e.appendBytes(digest[:])
}

func (e *cacheEncoder) appendValue(value core.Value) {
	if e.exceeded {
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

func (p *Program) appendDefinitionDependency(e *cacheEncoder, dependency *core.Node, depth int) {
	if e.exceeded {
		return
	}
	if dependency == nil || !dependency.Current {
		e.appendByte(0)
		return
	}
	e.appendValue(dependency.Latest)
	if e.exceeded {
		return
	}
	if depth >= 32 {
		e.appendByte(0)
		return
	}
	order := make([]int, len(dependency.Dynamic))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		pick := order[i]
		j := i - 1
		for j >= 0 {
			left, right := dependency.Dynamic[order[j]].Key, dependency.Dynamic[pick].Key
			if left.Kind < right.Kind || (left.Kind == right.Kind && left.Name <= right.Name) {
				break
			}
			order[j+1] = order[j]
			j--
		}
		order[j+1] = pick
	}
	e.appendU64(uint64(len(order)))
	for i := range order {
		if e.exceeded {
			return
		}
		child := dependency.Dynamic[order[i]]
		key := child.Key
		e.appendByte(byte(key.Kind))
		e.appendText(key.Name)
		switch key.Kind {
		case core.ResourceFile:
			name := p.canonicalTarget(key.Name, true)
			p.appendLiveFile(e, name)
			mem.FreeString(p.Alloc, name)
		case core.ResourceGlob:
			var fp [32]byte
			if !p.globFingerprint(key.Name, fp[:]) {
				e.unusable = true
			}
			e.appendBytes(fp[:])
		case core.ResourceEnvironment:
			env, ok := p.configuredEnvironment(key.Name)
			if ok {
				e.appendByte(1)
				e.appendText(env)
			} else {
				e.appendByte(0)
			}
		case core.ResourceTool:
			e.appendValue(child.Latest)
		case core.ResourceDefinition:
			p.appendDefinitionDependency(e, child, depth+1)
		case core.ResourceTask:
			index := p.instanceIndex(child)
			if index >= 0 {
				e.appendBytes(p.Instances[index].CacheFingerprint[:])
			} else {
				e.appendByte(0)
			}
		default:
			if child.Current {
				e.appendValue(child.Latest)
			} else {
				e.appendByte(0)
			}
		}
	}
}

// fileFingerprintResult distinguishes a cacheable missing path from an
// unusable filesystem failure. Ready means out holds a regular-file digest.
type fileFingerprintResult struct {
	Ready   bool
	Missing bool
}

// fileFingerprint hashes a regular file without following links. Directories,
// symlinks, and other non-regular types are unusable so they never share a
// regular-file marker. Missing paths are cacheable and carry no content digest.
func (p *Program) fileFingerprint(name string, out []byte) fileFingerprintResult {
	result := p.Host.Lstat(name)
	if result.Failed {
		return fileFingerprintResult{}
	}
	if !result.Exists {
		return fileFingerprintResult{Missing: true}
	}
	info := result.Info
	if !info.Regular || len(out) < 32 {
		return fileFingerprintResult{}
	}
	s := newSHA256()
	s.Write([]byte("file\x00" + name))
	s.Write([]byte{cacheFileRegular})
	var meta [16]byte
	size := uint64(info.Size)
	mt := uint64(info.ModTime)
	for i := 0; i < 8; i++ {
		meta[i] = byte(size >> uint(i*8))
		meta[8+i] = byte(mt >> uint(i*8))
	}
	s.Write(meta[:])
	data, readErr := p.Host.ReadFile(p.Alloc, name)
	if readErr != nil {
		return fileFingerprintResult{}
	}
	s.Write(data)
	mem.FreeSlice(p.Alloc, data)
	s.Sum(out)
	return fileFingerprintResult{Ready: true}
}
