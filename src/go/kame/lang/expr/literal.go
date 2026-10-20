package expr

import "solod.dev/so/unicode/utf8"

// IsLiteral accepts only closed data: no names, interpolation, patterns, calls,
// selectors or captures. Typed configuration cannot execute caller-supplied
// expressions. Keep the same nesting ceiling as portable value signatures.
func IsLiteral(e *Expr) bool { return literalAt(e, 0) }

func literalAt(e *Expr, depth int) bool {
	if e == nil || depth >= 256 || e.Pattern != nil {
		return false
	}
	switch e.Kind {
	case Nil, Boolean, Integer:
		return true
	case Float:
		return e.Float == e.Float && (e.Float == 0 || e.Float/2 != e.Float)
	case Symbol:
		return utf8.Valid([]byte(e.Text))
	case String, Path:
		if e.Kind == Path && PathHasGlob(e.Text) {
			return false
		}
		for i := range e.Parts {
			if e.Parts[i].Expr != nil || !utf8.Valid([]byte(e.Parts[i].Text)) {
				return false
			}
		}
		return utf8.Valid([]byte(e.Text))
	case List:
		for i := range e.Items {
			if !literalAt(e.Items[i], depth+1) {
				return false
			}
		}
		return true
	case Record:
		for i := range e.Fields {
			if !utf8.Valid([]byte(e.Fields[i].Key)) || !literalAt(e.Fields[i].Value, depth+1) {
				return false
			}
			for j := 0; j < i; j++ {
				if e.Fields[i].Key == e.Fields[j].Key {
					return false
				}
			}
		}
		return true
	}
	return false
}

// PathHasGlob detects authored, unescaped wildcard syntax. Such path expressions
// perform filesystem observations rather than yielding closed literal data.
func PathHasGlob(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '*' || text[i] == '?' || text[i] == '[' {
			return true
		}
	}
	return false
}
