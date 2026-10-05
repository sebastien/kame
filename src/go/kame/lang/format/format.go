// Package format canonicalizes Kame language sources. It is portable so both
// the native CLI and the freestanding host can share the exact output.
package format

import (
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
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
	Span    source.Span
	OK      bool
}

// Source formats one language source. indentStyle is "tabs" or "spaces";
// indentWidth is used for spaces.
func Source(a mem.Allocator, lang string, name string, text string, indentStyle string, indentWidth int) Result {
	return SourceWithComment(a, lang, name, text, indentStyle, indentWidth, "")
}

// SourceWithComment formats source with an optional template comment style.
func SourceWithComment(a mem.Allocator, lang string, name string, text string, indentStyle string, indentWidth int, comment string) Result {
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
			out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message, result.Diagnostics[0].Span)
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
		result := template.FormatDocument(a, name, text, comment)
		if !result.OK {
			out := Result{Code: result.Code, Message: result.Message, Span: result.Span}
			result.Code, result.Message = "", ""
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := result.Text
		result.Text = ""
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	if lang == "rule" {
		result := rule.ParseRule(a, name, text)
		if len(result.Diagnostics) != 0 {
			out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message, result.Diagnostics[0].Span)
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := rule.FormatRuleWithIndent(a, result.Rule, indent)
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	var result *script.Script
	if lang == "kash" {
		result = script.ParseKash(a, name, text)
	} else {
		result = script.Parse(a, name, text)
	}
	if len(result.Diagnostics) != 0 {
		out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message, result.Diagnostics[0].Span)
		result.Free()
		freeIndent(a, indentBuf)
		return out
	}
	formatted := script.FormatWithIndent(a, result, indent)
	result.Free()
	freeIndent(a, indentBuf)
	return Result{Text: formatted, OK: true}
}

func diagnosticResult(a mem.Allocator, code string, message string, span source.Span) Result {
	return Result{Code: cloneText(a, code), Message: cloneText(a, message), Span: span}
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
