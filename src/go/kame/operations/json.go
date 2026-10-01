package operations

import (
    "kame/core"
    "kame/lang/eval"
)

func opParseJSON(c *eval.Context, s any, v []core.Value) eval.Result {
    _ = s
    if v[0].Kind != core.String { return invalidArgument(c, v, 0, "string") }
    var value core.Value
    if !core.ParseJSON(c.Run, []byte(v[0].Text), &value) {
        return failure("JSON_INVALID", "malformed JSON input")
    }
    return eval.Result{Value: value}
}

func opFail(c *eval.Context, s any, v []core.Value) eval.Result {
    _ = s
    if v[0].Kind != core.String { return invalidArgument(c, v, 0, "string") }
    return ownedFailure(c.Run, "USER_ERROR", v[0].Text)
}
