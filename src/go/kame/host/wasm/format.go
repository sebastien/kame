package wasm

import (
	"kame/lang/format"
	"solod.dev/so/mem"
)

// FormatLanguage formats one language source and returns the canonical text, or
// the first parse diagnostic. The returned text is owned by a.
func FormatLanguage(a mem.Allocator, lang string, name string, text string, indentStyle string, indentWidth int) PureResult {
	result := format.Source(a, lang, name, text, indentStyle, indentWidth)
	if !result.OK {
		out := PureResult{Code: pureText(a, result.Code), Message: pureText(a, result.Message)}
		if result.Code != "" {
			mem.FreeString(a, result.Code)
		}
		if result.Message != "" {
			mem.FreeString(a, result.Message)
		}
		return out
	}
	return PureResult{Text: result.Text}
}
