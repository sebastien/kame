// Package format canonicalizes Kame language sources. It is portable so both
// the native CLI and the freestanding host can share the exact output.
package format

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/rule"
	"kame/lang/script"
	"kame/lang/source"
	"kame/lang/template"
	"strings"
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
			indentBuf = append(indentBuf, ' ')
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
		formatted := formatExpressionSource(a, result)
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
		body := script.InspectStandaloneKashRule(a, result.Source, result.Rule)
		if body != nil && len(body.Diagnostics) != 0 {
			out := diagnosticResult(a, body.Diagnostics[0].Code, body.Diagnostics[0].Message, body.Diagnostics[0].Span)
			body.Free()
			result.Free()
			freeIndent(a, indentBuf)
			return out
		}
		formatted := script.FormatInspectedRule(a, result.Rule, body, indent)
		body.Free()
		result.Free()
		freeIndent(a, indentBuf)
		return Result{Text: formatted, OK: true}
	}
	var result *script.Script
	var authored *source.Source
	if lang == "kash" {
		result = script.ParseKash(a, name, text)
	} else if lang == "km" || lang == script.KameFileLanguage {
		authored = source.New(a, name, text)
		result = script.ParseFragment(a, authored, lang, 0, len(text))
		script.InspectKashRecipes(result)
	} else {
		result = script.Parse(a, name, text)
	}
	if len(result.Diagnostics) != 0 {
		out := diagnosticResult(a, result.Diagnostics[0].Code, result.Diagnostics[0].Message, result.Diagnostics[0].Span)
		result.Free()
		if authored != nil {
			authored.Free(a)
		}
		freeIndent(a, indentBuf)
		return out
	}
	formatted := script.FormatWithIndent(a, result, indent)
	result.Free()
	if authored != nil {
		authored.Free(a)
	}
	freeIndent(a, indentBuf)
	return Result{Text: formatted, OK: true}
}

func formatExpressionSource(a mem.Allocator, parsed *expr.Result) string {
	var out strings.Builder
	writeLines := func(text string, includeLast bool) {
		for text != "" {
			line, rest, newline := strings.Cut(text, "\n")
			if !newline && !includeLast {
				return
			}
			line = strings.TrimSpace(line)
			if line != "\\" {
				out.WriteString(line)
				out.WriteByte('\n')
			}
			text = rest
		}
	}
	text, span := parsed.Source.Text, parsed.Expr.Span
	writeLines(text[:span.Start], false)
	formatted := expr.Format(a, parsed.Expr)
	out.WriteString(formatted)
	mem.FreeString(a, formatted)
	line, rest, _ := strings.Cut(text[span.End:], "\n")
	if comment := strings.TrimSpace(line); strings.HasPrefix(comment, "#") {
		out.WriteByte(' ')
		out.WriteString(comment)
	}
	out.WriteByte('\n')
	writeLines(rest, true)
	return cloneText(a, out.String())
}

func diagnosticResult(a mem.Allocator, code string, message string, span source.Span) Result {
	return Result{Code: cloneText(a, code), Message: cloneText(a, message), Span: span}
}

func cloneText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	buffer := make([]byte, len(text))
	copy(buffer, text)
	return string(buffer)
}

func freeIndent(a mem.Allocator, indent []byte) {
	mem.FreeSlice(nil, indent)
}
