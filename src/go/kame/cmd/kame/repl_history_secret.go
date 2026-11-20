package main

import (
	"regexp"
	"strings"
	"sync"
)

// Deliberately format-based, not an entropy test. New credential formats belong
// here with positive and negative fixtures; unknown formats may not be detected.
// Patterns compile on first use: only interactive history needs them, and
// eager compilation would cost every kame and kash invocation.
var historyTokenPatterns = sync.OnceValue(func() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
		regexp.MustCompile(`\bxapp-[A-Za-z0-9-]{20,}\b`),
		regexp.MustCompile(`\bsk_(?:live|test)_[A-Za-z0-9]{16,}\b`),
		regexp.MustCompile(`\bsk-(?:proj-|ant-api[0-9]+-)[A-Za-z0-9_-]{20,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{48}\b`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b`),
	}
})

var (
	historyReference = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_/-]*|\{[A-Za-z_][A-Za-z0-9_/-]*\})$`)
	})
	historyURL = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s/"'<>:@]+:([^\s/"'<>@]+)@`)
	})
	historyHeaderRE = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`(?i)(?:proxy-)?authorization\s*:\s*(.*)`) })
)

func historySensitiveName(name string) bool {
	name = strings.ToLower(strings.TrimLeft(name, "-"))
	name = strings.TrimPrefix(name, "env/")
	name = strings.ReplaceAll(name, "-", "_")
	name = "_" + strings.TrimSuffix(name, ":") + "_"
	for _, sensitive := range []string{"password", "passwd", "pwd", "secret", "token", "api_key", "access_key", "private_key", "client_secret", "credential"} {
		if strings.Contains(name, "_"+sensitive+"_") {
			return true
		}
	}
	return false
}

func historyLiteral(value string) bool {
	return value != "" && !historyReference().MatchString(value)
}

func historySecret(text string) bool {
	return historySecretDepth(text, 0)
}

func historySecretDepth(text string, depth int) bool {
	if depth > 8 {
		return true // Do not trust arbitrarily nested command-language quoting.
	}
	for _, pattern := range historyTokenPatterns() {
		if pattern.MatchString(text) {
			return true
		}
	}
	for _, match := range historyURL().FindAllStringSubmatch(text, -1) {
		if historyLiteral(match[1]) {
			return true
		}
	}
	words := historyWords(text)
	for i, token := range words {
		word := token.text
		if token.literalReference {
			if name, value, found := strings.Cut(word, "="); found &&
				historySensitiveName(name) && value != "" {
				return true
			}
			for _, match := range historyURL().FindAllStringSubmatch(word, -1) {
				if match[1] != "" {
					return true
				}
			}
		}
		// Also inspect quoted shell snippets and quoted assignment-shaped argv.
		// Each recursion removes quoting or separates words; unchanged words
		// cannot recurse. The original input bounds the recursion.
		if word != text && (token.decoded || strings.ContainsAny(word, " =\t\n")) && historySecretDepth(word, depth+1) {
			return true
		}
		if match := historyHeaderRE().FindStringSubmatch(word); match != nil {
			value := strings.TrimSpace(match[1])
			literalReference := token.literalReference
			if value == "" && i+1 < len(words) {
				value = words[i+1].text
				literalReference = words[i+1].literalReference
				if (strings.EqualFold(value, "Bearer") || strings.EqualFold(value, "Basic")) && i+2 < len(words) {
					value += " " + words[i+2].text
					literalReference = words[i+2].literalReference
				}
			}
			if scheme, rest, found := strings.Cut(value, " "); found &&
				(strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Basic")) {
				value = strings.TrimSpace(rest)
			}
			if historyLiteral(value) || value != "" && literalReference {
				return true
			}
		}
		if !historySensitiveName(word) {
			continue
		}
		next := i + 1
		if next < len(words) && words[next].text == "=" {
			next++
		} else if !strings.HasPrefix(word, "-") && !strings.HasSuffix(word, ":") {
			continue
		}
		if next < len(words) && (historyLiteral(words[next].text) || words[next].text != "" && words[next].literalReference) {
			return true
		}
	}
	return false
}

// Tokenize authored words without expanding anything. Quotes preserve spaces;
// backslash escapes are decoded so escaped sensitive names still match. Keep
// empty quoted words and treat an incomplete credential value conservatively.
type historyWord struct {
	text             string
	literalReference bool
	decoded          bool
}

func historyWords(text string) []historyWord {
	var words []historyWord
	var word strings.Builder
	var quote byte
	started := false
	literalReference := false
	decoded := false
	flush := func() {
		if started {
			words = append(words, historyWord{text: word.String(), literalReference: literalReference, decoded: decoded})
		}
		word.Reset()
		started = false
		literalReference = false
		decoded = false
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\\' && quote != '\'' && i+1 < len(text) {
			i++
			// Escaping can turn a reference into mixed literal/reference text.
			literalReference, decoded = true, true
			word.WriteByte(text[i])
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
				if i+1 < len(text) && !strings.ContainsRune(" \t\r\n;|&()[]=", rune(text[i+1])) {
					literalReference = true
				}
			} else {
				word.WriteByte(c)
				if quote == '\'' && c == '$' {
					literalReference = true
				}
			}
			continue
		}
		switch c {
		case '\'', '"':
			if started {
				literalReference = true
			}
			decoded = true
			quote, started = c, true
		case ' ', '\t', '\r', '\n', ';', '|', '&', '(', ')', '[', ']':
			flush()
		case '=':
			flush()
			words = append(words, historyWord{text: "="})
		default:
			started = true
			word.WriteByte(c)
		}
	}
	if quote != 0 {
		word.WriteString("<incomplete>")
	}
	flush()
	return words
}
