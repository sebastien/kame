package operations

import (
	"littlemake/core"
	"littlemake/lang/eval"
)

func opOut(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectOut, v)
}
func opErr(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectErr, v)
}
func opYield(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	return effect(c, eval.EffectYield, v)
}
func effect(c *eval.Context, kind eval.EffectKind, v []core.Value) eval.Result {
	for i := range v {
		if v[i].Kind == core.String || v[i].Kind == core.Pattern {
			c.Emit(kind, []byte(v[i].Text))
		} else if v[i].Kind == core.Bytes {
			c.Emit(kind, v[i].Bytes)
		} else {
			return invalid()
		}
	}
	return eval.Result{Value: core.Value{Kind: core.Nil}}
}
