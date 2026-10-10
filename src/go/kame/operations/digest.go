package operations

import (
	"kame/core"
	"kame/lang/eval"
)

// Hash raw bytes, never engine value/resource fingerprints or their framing.
func opDigest(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if v[0].Kind != core.String { return invalidArgument(c, v, 0, "algorithm string: sha1, sha256, or sha512") }
	var data []byte
	if v[1].Kind == core.Bytes { data = v[1].Bytes
	} else if v[1].Kind == core.String { data = []byte(v[1].Text)
	} else { return invalidArgument(c, v, 1, "bytes or string") }
	var sum [64]byte
	length := 0
	switch v[0].Text {
	case "sha1":
		sha1Sum(data, sum[:20]); length = 20
	case "sha256":
		digest := core.NewDigest()
		digest.Write(data); digest.Sum(sum[:32]); length = 32
	case "sha512":
		sha512Sum(data, sum[:]); length = 64
	default:
		return c.InvalidOperation("digest algorithm must be sha1, sha256, or sha512")
	}
	const hex = "0123456789abcdef"
	var output [128]byte
	for i := 0; i < length; i++ { output[i*2], output[i*2+1] = hex[sum[i]>>4], hex[sum[i]&15] }
	return eval.Result{Value: core.NewString(c.Run, string(output[:length*2]))}
}
