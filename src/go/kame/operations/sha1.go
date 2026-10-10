package operations

import "solod.dev/so/math/bits"

// Portable SHA-1 for compatibility identifiers, not collision-resistant security.
func sha1Sum(data []byte, out []byte) {
	h := [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	blocks := len(data)/64 + 1
	if len(data)%64 >= 56 { blocks++ }
	for block := 0; block < blocks; block++ {
		var chunk [64]byte
		start := block*64
		if start < len(data) { copy(chunk[:], data[start:]) }
		if start <= len(data) && len(data)-start < 64 { chunk[len(data)-start] = 0x80 }
		if block == blocks-1 {
			length := uint64(len(data))*8
			for i := 0; i < 8; i++ { chunk[63-i] = byte(length >> uint(i*8)) }
		}
		var w [80]uint32
		for i := 0; i < 16; i++ { w[i] = uint32(chunk[i*4])<<24 | uint32(chunk[i*4+1])<<16 | uint32(chunk[i*4+2])<<8 | uint32(chunk[i*4+3]) }
		for i := 16; i < 80; i++ { w[i] = bits.RotateLeft32(w[i-3]^w[i-8]^w[i-14]^w[i-16], 1) }
		a, b, c, d, e := h[0], h[1], h[2], h[3], h[4]
		for i := 0; i < 80; i++ {
			var f, k uint32
			if i < 20 { f, k = (b&c)|((^b)&d), 0x5a827999
			} else if i < 40 { f, k = b^c^d, 0x6ed9eba1
			} else if i < 60 { f, k = (b&c)|(b&d)|(c&d), 0x8f1bbcdc
			} else { f, k = b^c^d, 0xca62c1d6 }
			t := bits.RotateLeft32(a, 5)+f+e+k+w[i]
			e, d, c, b, a = d, c, bits.RotateLeft32(b, 30), a, t
		}
		h[0] += a; h[1] += b; h[2] += c; h[3] += d; h[4] += e
	}
	for i := range h { for j := 0; j < 4; j++ { out[i*4+j] = byte(h[i] >> uint(24-j*8)) } }
}
