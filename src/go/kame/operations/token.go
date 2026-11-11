package operations

import (
	"kame/core"
	"kame/lang/eval"
	"strings"
	"unicode"
	"unicode/utf8"
)

func tokenCharacter(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func opSubToken(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	for i := range values {
		if values[i].Kind != core.String {
			return invalidArgument(c, values, i, "string")
		}
	}
	from, to, text := values[0].Text, values[1].Text, values[2].Text
	if from == "" {
		return c.InvalidOperation("expects a non-empty search string")
	}
	b := strings.Builder{}
	defer b.Reset()
	written, scan := 0, 0
	for scan <= len(text)-len(from) {
		at := strings.Index(text[scan:], from)
		if at < 0 {
			break
		}
		at += scan
		end := at + len(from)
		left, right := false, false
		if at > 0 {
			r, _ := utf8.DecodeLastRuneInString(text[:at])
			left = tokenCharacter(r)
		}
		if end < len(text) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = tokenCharacter(r)
		}
		if !left && !right {
			b.WriteString(text[written:at])
			b.WriteString(to)
			written, scan = end, end
		} else {
			_, width := utf8.DecodeRuneInString(text[at:])
			scan = at + width
		}
	}
	b.WriteString(text[written:])
	return eval.Result{Value: core.NewString(c.Run, b.String())}
}
