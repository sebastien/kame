// Package eval evaluates Kame language ASTs over core values.
package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/strconv"
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

// InvalidArgument identifies an operand without retaining its potentially
// sensitive value. Indices are zero-based here and one-based in messages.
func (c *Context) InvalidArgument(index int, expected string, actual core.Kind) Result {
	span := c.Span
	if index >= 0 && index < len(c.operationArguments) {
		span = c.operationArguments[index].Span
	}
	var buffer [strconv.MaxIntBase10Len]byte
	number := strconv.FormatInt(buffer[:], int64(index+1), 10)
	result := failure(c.Run, "EXPR_INVALID", span, "`"+c.OperationName+"` argument "+number+" expects "+expected+"; got "+core.KindName(actual))
	attachSource(&result, c)
	return result
}

// InvalidElement keeps the primary location on the containing operand while
// identifying the rejected list item and its kind in the message.
func (c *Context) InvalidElement(index int, item int, expected string, actual core.Kind) Result {
	span := c.Span
	if index >= 0 && index < len(c.operationArguments) {
		span = c.operationArguments[index].Span
	}
	var argBuffer, itemBuffer [strconv.MaxIntBase10Len]byte
	argNumber := strconv.FormatInt(argBuffer[:], int64(index+1), 10)
	itemNumber := strconv.FormatInt(itemBuffer[:], int64(item+1), 10)
	result := failure(c.Run, "EXPR_INVALID", span, "`"+c.OperationName+"` argument "+argNumber+" item "+itemNumber+" expects "+expected+"; got "+core.KindName(actual))
	attachSource(&result, c)
	return result
}

// InvalidOperation reports a value constraint or result contract, rather than
// inventing a type mismatch when all operand kinds are valid.
func (c *Context) InvalidOperation(message string) Result {
	result := failure(c.Run, "EXPR_INVALID", c.Span, "`"+c.OperationName+"` "+message)
	attachSource(&result, c)
	return result
}

func diagnosticSeverity(severity source.Severity) diagnostic.Severity {
	if severity == source.Warning {
		return diagnostic.Warning
	}
	return diagnostic.Error
}
