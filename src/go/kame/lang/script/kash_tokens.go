package script

import "strings"

// KashTokenKind classifies Kash source text for presentation.
type KashTokenKind uint8

const (
	KashTokenText       KashTokenKind = iota // argument words and other plain text
	KashTokenCommand                         // command word at command position
	KashTokenKeyword                         // statement keyword
	KashTokenDefinition                      // name bound by a definition or loop
	KashTokenOption                          // argument starting with -
	KashTokenString                          // quoted text
	KashTokenVariable                        // $name, ${name}, $...name
	KashTokenOperator                        // separators, pipes, redirections, =, $( )
	KashTokenComment                         // # comment
	KashTokenFunction                        // head of a Kame call
	KashTokenConstant                        // :keyword constants and stage setup keys
	KashTokenNumber                          // numeric literal in an expression
	KashTokenPunct                           // expression brackets
)

// KashToken is a byte range of the source with its kind.
type KashToken struct {
	Kind       KashTokenKind
	Start, End int
}

var kashStatementKeywords = map[string]bool{
	"alias": true, "unalias": true,
	"def": true, "defer": true, "elif": true, "else": true, "exit": true, "for": true, "if": true,
	"let": true, "match": true, "return": true, "while": true, "with": true,
}

// KashTokens lexes Kash text for highlighting. It never fails: unclosed
// quotes, expressions, and substitutions extend to the end of the text, so
// partially typed input is classified as far as it goes. Blanks are not
// tokens. Classification is lexical and approximate; it does not validate.
func KashTokens(text string) []KashToken {
	l := kashLexer{text: text}
	l.commands(0, 0)
	return l.tokens
}

type kashLexer struct {
	text   string
	tokens []KashToken
}

func (l *kashLexer) emit(kind KashTokenKind, start, end int) int {
	if end > start {
		l.tokens = append(l.tokens, KashToken{Kind: kind, Start: start, End: end})
	}
	return end
}

func kashBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\r' }

func kashWordEnd(c byte) bool {
	return kashBlank(c) || c == '\n' || c == ';' || c == '|' || c == '&' || c == '<' || c == '>' || c == ')'
}

// commands lexes statements from i until the closing byte at depth zero
// (0 for the end of the text) and returns the index after it.
func (l *kashLexer) commands(i int, closing byte) int {
	text := l.text
	command := true // the next word starts a command
	setup := false  // the next word is a stage setup value
	for i < len(text) {
		c := text[i]
		switch {
		case closing != 0 && c == closing:
			return l.emit(KashTokenOperator, i, i+1)
		case kashBlank(c):
			i++
		case c == '\n' || c == ';':
			if c == ';' {
				l.emit(KashTokenOperator, i, i+1)
			}
			i, command, setup = i+1, true, false
		case c == ')':
			i = l.emit(KashTokenPunct, i, i+1)
		case c == '#':
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			i = l.emit(KashTokenComment, i, i+end)
		case strings.HasPrefix(text[i:], "&&") || strings.HasPrefix(text[i:], "||"):
			i, command = l.emit(KashTokenOperator, i, i+2), true
		case c == '|' || c == '&' && !strings.HasPrefix(text[i:], "&>"):
			i, command = l.emit(KashTokenOperator, i, i+1), true
		case l.redirection(i) > i:
			i = l.emit(KashTokenOperator, i, l.redirection(i))
			i = l.word(i, KashTokenText)
		case c == '(':
			i, command = l.expression(i, command), false
		case setup:
			i, setup = l.word(i, KashTokenText), false
		case command && c == ':':
			i, setup = l.word(i, KashTokenConstant), true
		case command:
			start := i
			end := l.wordEnd(i)
			word := text[start:end]
			switch {
			case kashStatementKeywords[word]:
				i = l.emit(KashTokenKeyword, start, end)
				i = l.afterKeyword(word, i)
				command = word == "if" || word == "elif" || word == "while" || word == "else" || word == "defer"
			default:
				i, command = l.word(start, KashTokenCommand), false
			}
		case c == '-':
			i = l.word(i, KashTokenOption)
		default:
			i = l.word(i, KashTokenText)
		}
	}
	return i
}

// redirection returns the end of a redirection operator at i, or i.
func (l *kashLexer) redirection(i int) int {
	text := l.text
	j := i
	if j < len(text) && (text[j] >= '0' && text[j] <= '9' || text[j] == '&') {
		j++
	}
	if j >= len(text) || text[j] != '>' && text[j] != '<' {
		return i
	}
	j++
	if j < len(text) && text[j] == '>' {
		j++
	}
	if j+1 < len(text) && text[j] == '&' && text[j+1] >= '0' && text[j+1] <= '9' {
		j += 2
	}
	return j
}

func kashName(word string) bool {
	if word == "" {
		return false
	}
	for i := 0; i < len(word); i++ {
		c := word[i]
		if !(c == '_' || c == '-' || c == '?' || c == '!' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// definitionFollows reports whether `=` or `?=` follows i after blanks.
func (l *kashLexer) definitionFollows(i int) bool {
	for i < len(l.text) && kashBlank(l.text[i]) {
		i++
	}
	rest := l.text[i:]
	return strings.HasPrefix(rest, "?=") || strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==")
}

// definitionValue lexes `= VALUE` after a definition head: the value is an
// expression running to the end of the line.
func (l *kashLexer) definitionValue(i int) int {
	for i < len(l.text) && kashBlank(l.text[i]) {
		i++
	}
	end := i + 1
	if l.text[i] == '?' {
		end++
	}
	return l.expressionLine(l.emit(KashTokenOperator, i, end))
}

// afterKeyword lexes the parts of a keyword statement that are not commands.
func (l *kashLexer) afterKeyword(word string, i int) int {
	skip := func() {
		for i < len(l.text) && kashBlank(l.text[i]) {
			i++
		}
	}
	switch word {
	case "alias":
		skip()
		start := i
		for i < len(l.text) && !kashWordEnd(l.text[i]) && l.text[i] != '=' {
			i++
		}
		l.emit(KashTokenDefinition, start, i)
		if l.definitionFollows(i) {
			return l.definitionValue(i)
		}
		return i
	case "let":
		skip()
		end := l.wordEnd(i)
		l.emit(KashTokenDefinition, i, end)
		if l.definitionFollows(end) {
			return l.definitionValue(end)
		}
		return end
	case "def":
		skip()
		i = l.emit(KashTokenDefinition, i, l.wordEnd(i))
		for {
			if l.definitionFollows(i) {
				return l.definitionValue(i)
			}
			skip()
			if strings.HasPrefix(l.text[i:], "()") {
				i = l.emit(KashTokenPunct, i, i+2)
				continue
			}
			end := l.wordEnd(i)
			if end == i {
				return i
			}
			i = l.emit(KashTokenDefinition, i, end)
		}
	case "for":
		skip()
		i = l.emit(KashTokenDefinition, i, l.wordEnd(i))
		skip()
		if strings.HasPrefix(l.text[i:], "in") && (i+2 == len(l.text) || kashWordEnd(l.text[i+2])) {
			i = l.emit(KashTokenKeyword, i, i+2)
		}
		return l.expressionLine(i)
	case "match", "return", "exit", "with":
		return l.expressionLine(i)
	}
	return i
}

func (l *kashLexer) wordEnd(i int) int {
	for i < len(l.text) && !kashWordEnd(l.text[i]) && l.text[i] != '(' {
		i++
	}
	return i
}

// word lexes one command word starting at i: literal text of kind, with
// quoted, substituted, and expression parts classified separately.
func (l *kashLexer) word(i int, kind KashTokenKind) int {
	text := l.text
	start := i
	flush := func(end int) { l.emit(kind, start, end) }
	for i < len(text) && !kashWordEnd(text[i]) {
		switch c := text[i]; {
		case c == '\\':
			i = min(i+2, len(text))
		case c == '\'':
			flush(i)
			end := strings.IndexByte(text[i+1:], '\'')
			if end < 0 {
				end = len(text) - i - 2
			}
			i = l.emit(KashTokenString, i, i+end+2)
			start = i
		case c == '"':
			flush(i)
			i = l.doubleQuoted(i)
			start = i
		case c == '$':
			flush(i)
			i = l.substitution(i)
			start = i
		case c == '(':
			flush(i)
			i = l.expression(i, false)
			start = i
		default:
			i++
		}
	}
	flush(i)
	return i
}

// doubleQuoted lexes "..." with substitutions inside.
func (l *kashLexer) doubleQuoted(i int) int {
	text := l.text
	start := i
	i++
	for i < len(text) && text[i] != '"' {
		switch text[i] {
		case '\\':
			i = min(i+2, len(text))
		case '$':
			if i+1 < len(text) && (text[i+1] == '(' || text[i+1] == '{' || kashNameStart(text[i+1])) {
				l.emit(KashTokenString, start, i)
				i = l.substitution(i)
				start = i
				continue
			}
			i++
		default:
			i++
		}
	}
	return l.emit(KashTokenString, start, min(i+1, len(text)))
}

func kashNameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// substitution lexes $name, ${name}, $...name, or $(commands).
func (l *kashLexer) substitution(i int) int {
	text := l.text
	j := i + 1
	if strings.HasPrefix(text[j:], "...") {
		j += 3
	}
	switch {
	case j < len(text) && text[j] == '(' && j == i+1:
		l.emit(KashTokenOperator, i, j+1)
		return l.commands(j+1, ')')
	case j < len(text) && text[j] == '{':
		end := strings.IndexByte(text[j:], '}')
		if end < 0 {
			return l.emit(KashTokenVariable, i, len(text))
		}
		return l.emit(KashTokenVariable, i, j+end+1)
	}
	for j < len(text) && (kashNameStart(text[j]) || text[j] >= '0' && text[j] <= '9' || text[j] == '-' || text[j] == '.') {
		j++
	}
	if j == i+1 {
		return l.emit(KashTokenText, i, j)
	}
	return l.emit(KashTokenVariable, i, j)
}

// expressionLine lexes a Kame expression running to the end of the line,
// or further while brackets stay open.
func (l *kashLexer) expressionLine(i int) int {
	return l.expressionItems(i, 0, false)
}

// expression lexes a parenthesized Kame expression at i.
func (l *kashLexer) expression(i int, _ bool) int {
	l.emit(KashTokenPunct, i, i+1)
	return l.expressionItems(i+1, ')', true)
}

// expressionItems lexes expression items until closing (0 for the end of the
// line). head marks the first item of a call as its function.
func (l *kashLexer) expressionItems(i int, closing byte, head bool) int {
	text := l.text
	for i < len(text) {
		c := text[i]
		switch {
		case closing != 0 && c == closing:
			return l.emit(KashTokenPunct, i, i+1)
		case closing == 0 && (c == '\n' || c == ';' || c == '#'):
			return i
		case kashBlank(c) || c == '\n':
			i++
		case c == '(':
			i, head = l.expression(i, false), false
		case c == '[':
			l.emit(KashTokenPunct, i, i+1)
			i, head = l.expressionItems(i+1, ']', false), false
		case c == '{':
			l.emit(KashTokenPunct, i, i+1)
			i, head = l.expressionItems(i+1, '}', false), false
		case c == ')' || c == ']' || c == '}' || c == ',':
			i = l.emit(KashTokenPunct, i, i+1)
		case c == '"':
			i, head = l.expressionString(i), false
		case c == '$':
			i, head = l.substitution(i), false
		case c == '|' && closing == 0:
			// A ?? composition or pipe-like operator in a definition value.
			i = l.emit(KashTokenOperator, i, i+1)
		default:
			end := i
			for end < len(text) && !kashBlank(text[end]) && strings.IndexByte("\n()[]{}\";,", text[end]) < 0 {
				end++
			}
			if end == i {
				end++
			}
			word := text[i:end]
			kind := KashTokenText
			switch {
			case head:
				kind = KashTokenFunction
			case word[0] == ':':
				kind = KashTokenConstant
			case word[0] >= '0' && word[0] <= '9' || len(word) > 1 && word[0] == '-' && word[1] >= '0' && word[1] <= '9':
				kind = KashTokenNumber
			case word == "??" || word == "=":
				kind = KashTokenOperator
			}
			i, head = l.emit(kind, i, end), false
		}
	}
	return i
}

// expressionString lexes a Kame string literal.
func (l *kashLexer) expressionString(i int) int {
	text := l.text
	j := i + 1
	for j < len(text) && text[j] != '"' {
		if text[j] == '\\' {
			j++
		}
		j++
	}
	return l.emit(KashTokenString, i, min(j+1, len(text)))
}
