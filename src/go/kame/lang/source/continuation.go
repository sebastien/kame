package source

// TopLevelByte returns the first matching byte outside double quotes and nested
// delimiters, or -1. Unmatched closing delimiters retain negative depth.
func TopLevelByte(text string, want byte) int {
	depth, quote := 0, false
	for i := 0; i < len(text); i++ {
		if quote {
			if text[i] == '\\' {
				i++
			} else if text[i] == '"' {
				quote = false
			}
			continue
		}
		if text[i] == '"' {
			quote = true
		} else if text[i] == '(' || text[i] == '[' || text[i] == '{' {
			depth++
		} else if text[i] == ')' || text[i] == ']' || text[i] == '}' {
			depth--
		} else if text[i] == want && depth == 0 {
			return i
		}
	}
	return -1
}

// ContinuationEnd returns the position after an escaped line ending, or pos.
// Parsers consume it as whitespace without rewriting authored source bytes.
func ContinuationEnd(text string, pos int, end int) int {
	if pos+1 >= end || text[pos] != '\\' {
		return pos
	}
	if text[pos+1] == '\n' {
		return pos + 2
	}
	if pos+2 < end && text[pos+1] == '\r' && text[pos+2] == '\n' {
		return pos + 3
	}
	return pos
}

// LogicalLineEnd skips continuations outside quoted strings. Recipes retain
// their physical lines and shell-owned backslashes.
func LogicalLineEnd(text string, pos int) int {
	quoted := false
	for pos < len(text) {
		if text[pos] == '\n' {
			return pos
		}
		if text[pos] == '\\' {
			if !quoted {
				next := ContinuationEnd(text, pos, len(text))
				if next != pos {
					pos = next
					continue
				}
			}
			if pos+1 < len(text) && (text[pos+1] == '\\' || text[pos+1] == '"') {
				pos += 2
				continue
			}
		} else if text[pos] == '"' {
			quoted = !quoted
		}
		pos++
	}
	return pos
}
