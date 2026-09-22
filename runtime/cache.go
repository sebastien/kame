package program

// The task cache is deliberately local to the runtime.  It stores only a
// completed fingerprint and replayable process output; the fingerprint is
// rebuilt from the accepted render before every lookup.

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"solod.dev/so/math/bits"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

const cacheFormat byte = 1
const cacheLogDefault = 64 * 1024
const cacheManifestMax = 16 * 1024 * 1024

type cacheRecord struct {
	Identity string
	Fingerprint [32]byte
	Schema byte
	StartedAt int64
	CompletedAt int64
	Duration int64
	ExitStatus int64
	Manifest []byte
	Stdout []byte
	Stderr []byte
	StdoutTruncated bool
	StderrTruncated bool
}

func (r *cacheRecord) Free(a mem.Allocator) {
	if r.Identity != "" { mem.FreeString(a, r.Identity) }
	if len(r.Manifest) != 0 { slices.Free(a, r.Manifest) }
	if len(r.Stdout) != 0 { slices.Free(a, r.Stdout) }
	if len(r.Stderr) != 0 { slices.Free(a, r.Stderr) }
	*r = cacheRecord{}
}

// sha256State is a small streaming SHA-256 implementation.  Keeping it here
// avoids a hosted crypto dependency and lets file fingerprints be calculated
// without reading complete files into memory.
type sha256State struct { h [8]uint32; block [64]byte; used int; length uint64 }

func newSHA256() sha256State {
	return sha256State{h: [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}}
}

func (s *sha256State) Write(data []byte) {
	s.length += uint64(len(data))
	for len(data) != 0 {
		n := 64 - s.used; if n > len(data) { n = len(data) }
		copy(s.block[s.used:s.used+n], data[:n]); s.used += n; data = data[n:]
		if s.used == 64 { s.compress(s.block[:]); s.used = 0 }
	}
}

func (s *sha256State) Sum(out []byte) {
	bitsLen := s.length * 8
	var pad [128]byte; pad[0] = 0x80
	n := 56 - s.used; if n <= 0 { n += 64 }
	s.Write(pad[:n])
	for i := 0; i < 8; i++ { pad[i] = byte(bitsLen >> uint(56-i*8)) }
	s.Write(pad[:8])
	for i := 0; i < 8; i++ { out[i*4] = byte(s.h[i] >> 24); out[i*4+1] = byte(s.h[i] >> 16); out[i*4+2] = byte(s.h[i] >> 8); out[i*4+3] = byte(s.h[i]) }
}

func (s *sha256State) compress(block []byte) {
	var w [64]uint32
	for i := 0; i < 16; i++ { w[i] = uint32(block[i*4])<<24 | uint32(block[i*4+1])<<16 | uint32(block[i*4+2])<<8 | uint32(block[i*4+3]) }
	for i := 16; i < 64; i++ { x, y := w[i-15], w[i-2]; w[i] = (bits.RotateLeft32(x, -7) ^ bits.RotateLeft32(x, -18) ^ (x >> 3)) + w[i-16] + (bits.RotateLeft32(y, -17) ^ bits.RotateLeft32(y, -19) ^ (y >> 10)) + w[i-7] }
	constants := [64]uint32{0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2}
	a,b,c,d,e,f,g,h := s.h[0],s.h[1],s.h[2],s.h[3],s.h[4],s.h[5],s.h[6],s.h[7]
	for i := 0; i < 64; i++ { s1 := bits.RotateLeft32(e,-6)^bits.RotateLeft32(e,-11)^bits.RotateLeft32(e,-25); choose := (e&f)^((^e)&g); t1 := h+s1+choose+constants[i]+w[i]; s0 := bits.RotateLeft32(a,-2)^bits.RotateLeft32(a,-13)^bits.RotateLeft32(a,-22); majority := (a&b)^(a&c)^(b&c); t2 := s0+majority; h,g,f,e,d,c,b,a = g,f,e,d+t1,c,b,a,t1+t2 }
	s.h[0]+=a; s.h[1]+=b; s.h[2]+=c; s.h[3]+=d; s.h[4]+=e; s.h[5]+=f; s.h[6]+=g; s.h[7]+=h
}

func cacheHash(data []byte, out []byte) { s := newSHA256(); s.Write(data); s.Sum(out) }

// FingerprintSHA256 computes the cache's streaming SHA-256 digest. It returns
// false when output is too short; callers need not allocate or retain input.
func FingerprintSHA256(data []byte, output []byte) bool {
	if len(output)<32 { return false }
	cacheHash(data,output[:32])
	return true
}

func appendU64(out []byte, n uint64) []byte { for i:=0; i<8; i++ { out=slices.Append(mem.System,out,byte(n>>uint(i*8))) }; return out }
func appendText(out []byte, text string) []byte { out=slices.Append(mem.System,out,5); out=appendU64(out,uint64(len(text))); for i:=range text { out=slices.Append(mem.System,out,text[i]) }; return out }

func resourceKindName(kind core.ResourceKind) string { if kind==core.ResourceDefinition{return "definition"};if kind==core.ResourceTarget{return "target"};if kind==core.ResourceFile{return "file"};if kind==core.ResourceTask{return "task"};if kind==core.ResourceService{return "service"};if kind==core.ResourceGlob{return "glob"};return "environment" }

// configuredEnvironment resolves the exact environment supplied to child
// processes. The host deliberately does not inherit the ambient environment,
// so os.LookupEnv would fingerprint a different input than the recipe sees.
func (p *Program) configuredEnvironment(name string) (string, bool) {
	for i := len(p.Options.Environment)-1; i >= 0; i-- {
		entry := p.Options.Environment[i]
		if len(entry) <= len(name) || entry[:len(name)] != name || entry[len(name)] != '=' { continue }
		return entry[len(name)+1:], true
	}
	return "", false
}

func (p *Program) globFingerprint(pattern string, out []byte) bool {
	value := p.wildcard(pattern)
	s := newSHA256(); s.Write([]byte("glob\x00")); var encoded []byte
	encoded=appendText(encoded,pattern); s.Write(encoded); slices.Free(mem.System,encoded)
	usable := true
	for i := range value.List {
		encoded=nil; encoded=appendText(encoded,value.List[i].Text); s.Write(encoded); slices.Free(mem.System,encoded)
		name := p.canonicalTarget(value.List[i].Text, true)
		var digest [32]byte
		finger := fileFingerprint(name, digest[:])
		mem.FreeString(p.Alloc, name)
		if finger.Missing { s.Write([]byte{cacheFileMissing}) } else if !finger.Ready { usable = false } else { s.Write(digest[:]) }
	}
	value.Free(p.Alloc)
	s.Sum(out)
	return usable
}

func cacheHex(a mem.Allocator, digest []byte) string { const hex="0123456789abcdef"; b:=strings.NewBuilder(a); for i:=0;i<32;i++ { b.WriteByte(hex[digest[i]>>4]); b.WriteByte(hex[digest[i]&15]) }; out:=cloneText(a,b.String());b.Free();return out }
func FingerprintHex(digest []byte) string { const hex="0123456789abcdef"; b:=strings.NewBuilder(mem.System); for i:=range digest { b.WriteByte(hex[digest[i]>>4]);b.WriteByte(hex[digest[i]&15]) }; result:=cloneText(mem.System,b.String());b.Free();return result }

func recordBytes(r *cacheRecord) []byte { var out []byte; out=slices.Append(mem.System,out,'L');out=slices.Append(mem.System,out,'M');out=slices.Append(mem.System,out,'K');out=slices.Append(mem.System,out,'R');out=slices.Append(mem.System,out,cacheFormat); out=appendU64(out,uint64(len(r.Identity))); for i:=range r.Identity { out=slices.Append(mem.System,out,r.Identity[i]) }; for i:=range r.Fingerprint { out=slices.Append(mem.System,out,r.Fingerprint[i]) };out=slices.Append(mem.System,out,r.Schema);out=appendU64(out,uint64(r.StartedAt));out=appendU64(out,uint64(r.CompletedAt));out=appendU64(out,uint64(r.Duration));out=appendU64(out,uint64(r.ExitStatus)); out=appendU64(out,uint64(len(r.Manifest))); for i:=range r.Manifest { out=slices.Append(mem.System,out,r.Manifest[i]) }; flags:=byte(0); if r.StdoutTruncated { flags|=1 }; if r.StderrTruncated { flags|=2 }; out=slices.Append(mem.System,out,flags); out=appendU64(out,uint64(len(r.Stdout))); for i:=range r.Stdout { out=slices.Append(mem.System,out,r.Stdout[i]) }; out=appendU64(out,uint64(len(r.Stderr))); for i:=range r.Stderr { out=slices.Append(mem.System,out,r.Stderr[i]) }; return out }
func takeU64(data []byte, at *int) uint64 { if *at+8>len(data) { return 0 }; var n uint64; for i:=0;i<8;i++ { n|=uint64(data[*at+i])<<uint(i*8) }; *at+=8; return n }
func parseRecord(a mem.Allocator,data []byte) cacheRecord {
	if len(data)<5 || string(data[:4])!="LMKR" || data[4]!=cacheFormat { return cacheRecord{} }
	at:=5
	if at+8>len(data) { return cacheRecord{} }
	n:=takeU64(data,&at)
	if n==0 || n>1024*1024 || n>uint64(len(data)-at) { return cacheRecord{} }
	r:=cacheRecord{Identity:cloneText(a,string(data[at:at+int(n)]))}
	at+=int(n)
	if at+32+1+32+8>len(data) { r.Free(a); return cacheRecord{} }
	for i:=range r.Fingerprint { r.Fingerprint[i]=data[at+i] }
	at+=32
	r.Schema=data[at]; if r.Schema!=1 { r.Free(a); return cacheRecord{} }; at++
	r.StartedAt=int64(takeU64(data,&at));r.CompletedAt=int64(takeU64(data,&at));r.Duration=int64(takeU64(data,&at));r.ExitStatus=int64(takeU64(data,&at))
	if r.StartedAt<=0 || r.CompletedAt<r.StartedAt || r.Duration<0 || r.ExitStatus!=0 { r.Free(a); return cacheRecord{} }
	n=takeU64(data,&at)
	if n==0 || n>cacheManifestMax || n>uint64(len(data)-at) { r.Free(a); return cacheRecord{} }
	r.Manifest=slices.Clone(a,data[at:at+int(n)])
	at+=int(n)
	if at>=len(data) { r.Free(a); return cacheRecord{} }
	flags:=data[at]
	if flags&^byte(3)!=0 { r.Free(a); return cacheRecord{} }
	at++
	if at+8>len(data) { r.Free(a); return cacheRecord{} }
	n=takeU64(data,&at)
	if n>uint64(len(data)-at) { r.Free(a); return cacheRecord{} }
	r.Stdout=slices.Clone(a,data[at:at+int(n)])
	r.StdoutTruncated=flags&1!=0
	at+=int(n)
	if at+8>len(data) { r.Free(a); return cacheRecord{} }
	n=takeU64(data,&at)
	if n!=uint64(len(data)-at) { r.Free(a); return cacheRecord{} }
	r.Stderr=slices.Clone(a,data[at:])
	r.StderrTruncated=flags&2!=0
	return r
}

func (p *Program) cacheLoad(entry *instance, fingerprint []byte) cacheRecord {
	name:=p.cachePath(entry)
	info,statErr:=os.Stat(name)
	maxLog:=p.Options.CacheRetainBytes
	if maxLog<cacheLogDefault { maxLog=cacheLogDefault }
	maxRecord:=int64(cacheManifestMax)+int64(maxLog)*2+1024*1024
	if statErr!=nil { mem.FreeString(p.Alloc,name); return cacheRecord{} }
	if info.Size()<0 || info.Size()>maxRecord { p.cacheWarning(entry,"CACHE_RECORD","cache record exceeds the supported size"); mem.FreeString(p.Alloc,name); return cacheRecord{} }
	data,err:=os.ReadFile(p.Alloc,name);mem.FreeString(p.Alloc,name);if err!=nil{return cacheRecord{}}
	r:=parseRecord(p.Alloc,data);mem.FreeSlice(p.Alloc,data)
	if r.Identity=="" { p.cacheWarning(entry,"CACHE_RECORD","malformed or unsupported cache record"); return r }
	identity:=p.cacheIdentity(entry);var digest [32]byte;if len(r.Manifest)!=0{cacheHash(r.Manifest,digest[:])}
	if r.Identity==""||r.Identity!=identity||string(r.Fingerprint[:])!=string(fingerprint)||string(r.Fingerprint[:])!=string(digest[:]){r.Free(p.Alloc)}
	mem.FreeString(p.Alloc,identity);return r
}

func (p *Program) cacheWarning(entry *instance, code string, message string) {
	if !p.Options.Verbose { return }
	target:=""
	if entry!=nil { target=entry.Plan.Target }
	p.emit(Event{Kind:CacheWarning,Target:target,Diagnostic:diagnostic.Diagnostic{Code:code,Severity:diagnostic.Warning,Message:message}})
}
func (p *Program) cacheSave(entry *instance,r *cacheRecord) bool {
	name:=p.cachePath(entry)
	if !mkdirParent(name) { mem.FreeString(p.Alloc,name); return false }
	data:=recordBytes(r)
	separator:=strings.LastIndexByte(name,'/')
	directory:=name[:separator]
	buffer:=make([]byte,os.MaxPathLen)
	f,err:=os.CreateTemp(buffer,directory,".littlemake-cache-")
	ok:=err==nil
	tmp:=""
	if ok {
		tmp=f.Name()
		_,err=f.Write(data)
		if err==nil { err=f.Sync() }
		closeErr:=f.Close()
		if err==nil { err=closeErr }
		ok=err==nil
	}
	if ok { ok=os.Rename(tmp,name)==nil }
	if !ok && tmp!="" { os.Remove(tmp) }
	mem.FreeSlice(mem.System,data);mem.FreeString(p.Alloc,name);return ok
}
