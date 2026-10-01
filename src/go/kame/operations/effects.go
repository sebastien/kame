package operations

import (
	"kame/core"
	"kame/lang/eval"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

func opOut(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectOut, true, v)
}
func opErr(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectErr, true, v)
}
func opYield(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectYield, false, v)
}
func effect(c *eval.Context, kind eval.EffectKind, returnOutput bool, v []core.Value) eval.Result {
	var output []byte
	hasBytes := false
	for i := range v {
		if v[i].Kind == core.Bytes {
			c.Emit(kind, v[i].Bytes)
			if returnOutput && c.Phase == eval.EvaluatePhase {
				for j := range v[i].Bytes {
					output = slices.Append(c.Run, output, v[i].Bytes[j])
				}
				hasBytes = true
			}
			continue
		}
		text, ok := stringValue(c.Run, v[i], false)
		if !ok {
			slices.Free(c.Run, output)
			return invalidArgument(c, v, i, "bytes or text-coercible value (nil, bool, int, float, string, pattern, list, or record)")
		}
		c.Emit(kind, []byte(text))
		if returnOutput && c.Phase == eval.EvaluatePhase {
			for j := range text {
				output = slices.Append(c.Run, output, text[j])
			}
		}
		mem.FreeString(c.Run, text)
	}
	if returnOutput && c.Phase == eval.EvaluatePhase {
		if hasBytes {
			result := core.NewBytes(c.Run, output)
			slices.Free(c.Run, output)
			return eval.Result{Value: result}
		}
		result := core.NewString(c.Run, string(output))
		slices.Free(c.Run, output)
		return eval.Result{Value: result}
	}
	slices.Free(c.Run, output)
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}
