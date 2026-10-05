package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/eval"
	"solod.dev/so/mem"
)

func opResource(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "resource URI string")
	}
	parsed := core.ParseResourceURI(c.Run, values[0].Text)
	if parsed.Error != "" {
		return eval.Result{Diagnostic: core.Diagnostic{
			Code:     cloneText(c.Run, "RES_INVALID"),
			Severity: diagnostic.Error,
			Message:  cloneText(c.Run, parsed.Error),
			Span:     diagnostic.Span{Start: c.Span.Start, End: c.Span.End},
			Owned:    true,
		}}
	}
	canonical := parsed.URI.Canonical(c.Run)
	parsed.URI.Free()
	resource := core.NewResource(c.Run, core.ResourceFile, canonical)
	mem.FreeString(c.Run, canonical)
	return eval.Result{Value: resource}
}
