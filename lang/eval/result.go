// Package eval evaluates LittleMake language ASTs over core values.
package eval

import (
	"littlemake/core"
	"littlemake/diagnostic"
	"littlemake/lang/source"
	"solod.dev/so/mem"
)

type Result struct {
	Value      core.Value
	Waiting    bool
	Completed  bool
	Stream     *core.Source
	Diagnostic diagnostic.Diagnostic
}

func (r *Result) Free(a mem.Allocator) {
	freeCallables(a, &r.Value)
	r.Value.Free(a)
	if r.Stream != nil {
		core.FreeSource(a, r.Stream)
	}
	r.Diagnostic.Free(a)
	*r = Result{}
}

// failure builds an owned diagnostic. Dynamic messages are stack strings and
// must be copied into the run allocator before the result escapes the call.
func failure(a mem.Allocator, code string, span source.Span, message string) Result {
	return Result{Diagnostic: diagnostic.Diagnostic{Code: cloneFailureText(a, code), Severity: diagnostic.Error, Message: cloneFailureText(a, message), Span: diagnostic.Span{Start: span.Start, End: span.End}, Owned: true}}
}

func cloneFailureText(a mem.Allocator, text string) string {
	if len(text) == 0 {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}

func diagnosticSeverity(severity source.Severity) diagnostic.Severity {
	if severity == source.Warning {
		return diagnostic.Warning
	}
	return diagnostic.Error
}
