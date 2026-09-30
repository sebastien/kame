// Package format canonicalizes Kame language sources. It is portable so both
// the native CLI and the freestanding host can share the exact output.
package format

import (
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
)

// Result carries either the formatted text (OK) or the first parse diagnostic.
// Text and the diagnostic strings are owned by the caller's allocator.
type Result struct {
	Text    string
	Code    string
	Message string
	OK      bool
}

// Source formats one language source. indentStyle is "tabs" or "spaces";
// indentWidth is used for spaces.
func Source(a mem.Allocator, lang string, name string, text string, indentStyle string, indentWidth int) Result {
	var indentBuf []byte
	indent := "\t"
	if indentStyle == "spaces" {
		for i := 0; i < indentWidth; i++ {
			indentBuf = slices.Append(a, indentBuf, ' ')
		}
		indent = string(indentBuf)
	}
	if lang == "expr" {
		result := expr.Parse(a, name, text)
		if len(result.Diagnostics) != 0 {
			out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message)
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := expr.Format(a, result.Expr)
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	if lang == "template" {
		result := template.ParseString(a, name, text)
		if len(result.Diagnostics) != 0 {
			out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message)
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := template.FormatString(a, result)
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	if lang == "rule" {
		result := rule.ParseRule(a, name, text)
		if len(result.Diagnostics) != 0 {
			out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message)
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := rule.FormatRuleWithIndent(a, result.Rule, indent)
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	result := script.Parse(a, name, text)
	if len(result.Diagnostics) != 0 {
		out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message)
		result.Free()
		freeIndent(a, indentBuf)
		return out
	}
	formatted := script.FormatWithIndent(a, result, indent)
	result.Free()
	freeIndent(a, indentBuf)
	return Result{Text: formatted, OK: true}
}

func diagnosticResult(a mem.Allocator, code string, message string) Result {
	return Result{Code: cloneText(a, code), Message: cloneText(a, message)}
}

func cloneText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	buffer := slices.Make[byte](a, len(text))
	copy(buffer, text)
	return string(buffer)
}

func freeIndent(a mem.Allocator, indent []byte) {
	slices.Free(a, indent)
}
