package operations

import (
	"kame/core"
	"kame/lang/eval"
)

func opFinally(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	return c.Finally(values)
}
