package eval

import (
	"kame/core"
	"kame/diagnostic"
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"

	"strconv"
)

type Result struct {
	Value        core.Value
	Waiting      bool
	Completed    bool
	Returned     bool      // call-local Kash control flow, consumed by its def invocation
	LoopTransfer expr.Kind // KashBreak/KashContinue, consumed by the nearest loop.
	Stream       *core.Source
	Diagnostic   diagnostic.Diagnostic
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

func renderFailure(a mem.Allocator, expression *expr.Expr, span source.Span, actual core.Kind) Result {
	result := failure(a, "EXPR_INVALID", span, "cannot render "+core.KindName(actual)+" as text; use an explicit conversion")
	if actual == core.Callable {
		tip := "Invoke the callable explicitly before rendering its result."
		if expression != nil && expression.Expansion == "@{" && (expression.Kind == expr.Name || expression.Kind == expr.Reference) {
			name := expr.Compact(a, expression)
			tip = owned(a, "Use @("+name+") to invoke it; @{...} only reads the reference.")
			mem.FreeString(a, name)
			result.Diagnostic.Tips = append(result.Diagnostic.Tips, tip)
		} else {
			result.Diagnostic.Tips = append(result.Diagnostic.Tips, owned(a, tip))
		}
	}
	return result
}

func (c *Context) InvalidRender(index int, actual core.Kind) Result {
	var expression *expr.Expr
	span := c.Span
	if index >= 0 && index < len(c.operationArguments) {
		expression = c.operationArguments[index]
		span = expression.Span
	}
	return renderFailure(c.Run, expression, span, actual)
}

func literalSelectorTip(a mem.Allocator, result Result) Result {
	if result.Diagnostic.Code == "SEL_NO_CONTEXT" || result.Diagnostic.Code == "SEL_INDEX_INVALID" {
		result.Diagnostic.Tips = append(result.Diagnostic.Tips, owned(a, "Escape \\@ for a literal at-sign in template text (for example, --mtime=\\@0)."))
	}
	return result
}

func environmentDenied(a mem.Allocator, span source.Span, name string) Result {
	message := "environment-read access denied"
	if name != "" {
		message += " for " + name
	}
	result := failure(a, "CAP_DENIED", span, message)
	result.Diagnostic.Tips = append(result.Diagnostic.Tips, owned(a, "Use scoped --allow-env=NAME; explicit grants replace defaults. Definition configuration and child environment settings do not authorize environment reads."))
	return result
}

func (c *Context) EnvironmentDenied(name string) Result {
	return environmentDenied(c.Run, c.Span, name)
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

	number := strconv.FormatInt(int64(index+1), 10)
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

	argNumber := strconv.FormatInt(int64(index+1), 10)
	itemNumber := strconv.FormatInt(int64(item+1), 10)
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
