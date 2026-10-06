package core

import (
	"solod.dev/so/math"
	"solod.dev/so/math/bits"
)

// SignatureMode is part of identity. Metadata is never mistaken for content,
// and an unavailable observation never proves equality, including with itself.
type SignatureMode int

const (
	SignatureUnavailable SignatureMode = iota
	SignatureContent
	SignatureMetadata
	SignatureMissing
)

type Signature struct {
	Mode   SignatureMode
	Digest [32]byte
}

func (s Signature) Equal(other Signature) bool {
	if s.Mode < SignatureContent || s.Mode > SignatureMissing || s.Mode != other.Mode {
		return false
	}
	for i := range s.Digest {
		if s.Digest[i] != other.Digest[i] {
			return false
		}
	}
	return true
}

// Digest is the portable, incremental SHA-256 primitive used for values,
// resources and accepted computation records. It does not retain input bytes.
type Digest struct {
	h      [8]uint32
	block  [64]byte
	used   int
	length uint64
}

func NewDigest() Digest {
	return Digest{h: [8]uint32{0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19}}
}

func (s *Digest) Write(data []byte) {
	s.length += uint64(len(data))
	for len(data) != 0 {
		n := 64 - s.used
		if n > len(data) {
			n = len(data)
		}
		copy(s.block[s.used:s.used+n], data[:n])
		s.used += n
		data = data[n:]
		if s.used == 64 {
			s.compress(s.block[:])
			s.used = 0
		}
	}
}

func (s *Digest) Sum(out []byte) {
	copy := *s
	copy.finish(out)
}

func (s *Digest) finish(out []byte) {
	bitsLen := s.length * 8
	var pad [128]byte
	pad[0] = 0x80
	n := 56 - s.used
	if n <= 0 {
		n += 64
	}
	s.Write(pad[:n])
	for i := 0; i < 8; i++ {
		pad[i] = byte(bitsLen >> uint(56-i*8))
	}
	s.Write(pad[:8])
	for i := 0; i < 8; i++ {
		out[i*4] = byte(s.h[i] >> 24)
		out[i*4+1] = byte(s.h[i] >> 16)
		out[i*4+2] = byte(s.h[i] >> 8)
		out[i*4+3] = byte(s.h[i])
	}
}

func (s *Digest) compress(block []byte) {
	var w [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = uint32(block[i*4])<<24 | uint32(block[i*4+1])<<16 | uint32(block[i*4+2])<<8 | uint32(block[i*4+3])
	}
	for i := 16; i < 64; i++ {
		x, y := w[i-15], w[i-2]
		w[i] = (bits.RotateLeft32(x, -7) ^ bits.RotateLeft32(x, -18) ^ (x >> 3)) + w[i-16] + (bits.RotateLeft32(y, -17) ^ bits.RotateLeft32(y, -19) ^ (y >> 10)) + w[i-7]
	}
	constants := [64]uint32{0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2}
	a, b, c, d, e, f, g, h := s.h[0], s.h[1], s.h[2], s.h[3], s.h[4], s.h[5], s.h[6], s.h[7]
	for i := 0; i < 64; i++ {
		t1 := h + (bits.RotateLeft32(e, -6) ^ bits.RotateLeft32(e, -11) ^ bits.RotateLeft32(e, -25)) + ((e & f) ^ ((^e) & g)) + constants[i] + w[i]
		t2 := (bits.RotateLeft32(a, -2) ^ bits.RotateLeft32(a, -13) ^ bits.RotateLeft32(a, -22)) + ((a & b) ^ (a & c) ^ (b & c))
		h, g, f, e, d, c, b, a = g, f, e, d+t1, c, b, a, t1+t2
	}
	s.h[0] += a
	s.h[1] += b
	s.h[2] += c
	s.h[3] += d
	s.h[4] += e
	s.h[5] += f
	s.h[6] += g
	s.h[7] += h
}

func ContentSignature(data []byte) Signature {
	s := NewDigest()
	s.Write(data)
	out := Signature{Mode: SignatureContent}
	s.Sum(out.Digest[:])
	return out
}

func (s *Digest) Uint64(n uint64) {
	var data [8]byte
	for i := range data {
		data[i] = byte(n >> uint(i*8))
	}
	s.Write(data[:])
}

func (s *Digest) Text(text string) { s.Uint64(uint64(len(text))); s.Write([]byte(text)) }

// ValueSignature fingerprints immutable data, not pointers or process handles.
// Unsupported values are deliberately unavailable rather than encoded as nil.
func ValueSignature(value Value) Signature {
	s := NewDigest()
	s.Text("kame-value-v1")
	if !s.value(value, 0) {
		return Signature{}
	}
	out := Signature{Mode: SignatureContent}
	s.Sum(out.Digest[:])
	return out
}

func (s *Digest) value(v Value, depth int) bool {
	if depth >= 256 {
		return false
	}
	s.Uint64(uint64(v.Kind))
	switch v.Kind {
	case Nil:
	case Bool:
		if v.Bool {
			s.Uint64(1)
		} else {
			s.Uint64(0)
		}
	case Int:
		s.Uint64(uint64(v.Int))
	case Float:
		n := math.Float64bits(v.Float)
		if n&0x7fffffffffffffff == 0 {
			n = 0
		}
		if n&0x7ff0000000000000 == 0x7ff0000000000000 && n&0x000fffffffffffff != 0 {
			n = 0x7ff8000000000000
		}
		s.Uint64(n)
	case String, Pattern:
		s.Text(v.Text)
	case Bytes:
		s.Uint64(uint64(len(v.Bytes)))
		s.Write(v.Bytes)
	case Resource:
		s.Uint64(uint64(v.Resource.Kind))
		s.Text(v.Resource.Name)
	case List:
		s.Uint64(uint64(len(v.List)))
		for i := range v.List {
			if !s.value(v.List[i], depth+1) {
				return false
			}
		}
	case Record:
		s.Uint64(uint64(len(v.Record)))
		// Records are unordered. Select canonical keys without allocating a copy.
		// ponytail: quadratic in field count; sort an owned index if large records dominate hashing.
		previous, hasPrevious := "", false
		for n := 0; n < len(v.Record); n++ {
			pick := -1
			for i := range v.Record {
				key := v.Record[i].Key
				if hasPrevious && key <= previous {
					continue
				}
				if pick < 0 || key < v.Record[pick].Key {
					pick = i
				}
			}
			if pick < 0 {
				return false
			}
			previous, hasPrevious = v.Record[pick].Key, true
			s.Text(previous)
			if !s.value(v.Record[pick].Value, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	return true
}
