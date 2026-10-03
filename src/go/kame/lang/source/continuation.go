package source

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
