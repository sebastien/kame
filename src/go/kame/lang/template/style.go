package template

// InferStyle selects the document comment syntax from a filename extension.
func InferStyle(path string) (string, bool) {
	// Extension after last dot in basename, case-insensitive.
	baseStart := 0
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			baseStart = i + 1
			break
		}
	}
	dot := -1
	for i := baseStart; i < len(path); i++ {
		if path[i] == '.' {
			dot = i
		}
	}
	if dot < 0 || dot+1 >= len(path) {
		return "", false
	}
	ext := path[dot+1:]
	return extStyle(ext)
}

func extStyle(ext string) (string, bool) {
	if eqFold(ext, "html") || eqFold(ext, "htm") || eqFold(ext, "xml") || eqFold(ext, "svg") || eqFold(ext, "vue") || eqFold(ext, "md") || eqFold(ext, "markdown") {
		return "html", true
	}
	if eqFold(ext, "c") || eqFold(ext, "h") || eqFold(ext, "cc") || eqFold(ext, "cpp") || eqFold(ext, "hpp") || eqFold(ext, "java") || eqFold(ext, "js") || eqFold(ext, "mjs") || eqFold(ext, "ts") || eqFold(ext, "tsx") || eqFold(ext, "jsx") || eqFold(ext, "go") || eqFold(ext, "rs") || eqFold(ext, "css") || eqFold(ext, "scss") || eqFold(ext, "less") || eqFold(ext, "php") || eqFold(ext, "swift") || eqFold(ext, "kt") {
		return "c", true
	}
	if eqFold(ext, "sh") || eqFold(ext, "bash") || eqFold(ext, "zsh") || eqFold(ext, "yaml") || eqFold(ext, "yml") || eqFold(ext, "py") || eqFold(ext, "rb") || eqFold(ext, "toml") || eqFold(ext, "ini") || eqFold(ext, "conf") || eqFold(ext, "properties") || eqFold(ext, "pl") || eqFold(ext, "r") {
		return "hash", true
	}
	if eqFold(ext, "sql") || eqFold(ext, "lua") || eqFold(ext, "hs") || eqFold(ext, "elm") || eqFold(ext, "ada") {
		return "dash", true
	}
	if eqFold(ext, "lisp") || eqFold(ext, "clj") || eqFold(ext, "cljs") || eqFold(ext, "el") || eqFold(ext, "scm") || eqFold(ext, "asm") {
		return "semi", true
	}
	if eqFold(ext, "tex") || eqFold(ext, "erl") || eqFold(ext, "hrl") || eqFold(ext, "m") {
		return "percent", true
	}
	if eqFold(ext, "ps1") || eqFold(ext, "psm1") || eqFold(ext, "psd1") {
		return "powershell", true
	}
	if eqFold(ext, "bat") || eqFold(ext, "cmd") {
		return "batch", true
	}
	return "", false
}

// InferContentStyle detects one complete comment style from directive tokens.
// No detected style falls back to plain; mixed detected styles are ambiguous.
func InferContentStyle(text string) (string, bool) {
	found := ""
	cur := 0
	for cur < len(text) {
		line := scanLine(text, cur, len(text))
		candidate := lineStyle(text[line.Start:line.ContentEnd])
		if candidate != "" {
			if found != "" && found != candidate {
				return "", false
			}
			found = candidate
		}
		cur = line.LineEnd
	}
	if found == "" {
		return "plain", true
	}
	return found, true
}

// ResolveAutoStyle prefers a registered file extension and otherwise detects
// a unique style from complete comment directive tokens in the content.
func ResolveAutoStyle(path string, text string) (string, bool) {
	if style, ok := InferStyle(path); ok {
		return style, true
	}
	return InferContentStyle(text)
}

func lineStyle(line string) string {
	start, end := trimHorizontal(line, 0, len(line))
	if start >= end {
		return ""
	}
	line = line[start:end]
	if directiveWrapper(line, "<!--", "-->") { return "html" }
	if directiveWrapper(line, "/*", "*/") { return "c" }
	if directiveWrapper(line, "<#", "#>") { return "powershell" }
	if directiveLine(line, "//") { return "c" }
	if directiveLine(line, "#") { return "hash" }
	if directiveLine(line, "--") { return "dash" }
	if directiveLine(line, ";") { return "semi" }
	if directiveLine(line, "%") { return "percent" }
	if len(line) >= 4 && (eqFold(line[:3], "rem") && horizontal(line[3])) && hasAtAfterSpace(line, 3) { return "batch" }
	if len(line) >= 3 && line[:2] == "::" && hasAtAfterSpace(line, 2) { return "batch" }
	return ""
}

func directiveWrapper(line string, open string, close string) bool {
	if len(line) < len(open)+len(close) || line[:len(open)] != open || line[len(line)-len(close):] != close {
		return false
	}
	start, end := trimHorizontal(line, len(open), len(line)-len(close))
	return isDirectiveToken(line[start:end])
}

func directiveLine(line string, prefix string) bool {
	if len(line) < len(prefix) || line[:len(prefix)] != prefix { return false }
	return hasAtAfterSpace(line, len(prefix))
}

func hasAtAfterSpace(line string, pos int) bool {
	for pos < len(line) && horizontal(line[pos]) { pos++ }
	return pos < len(line) && isDirectiveToken(line[pos:])
}

func isDirectiveToken(text string) bool {
	if len(text) < 2 || text[0] != '@' { return false }
	i := 1
	for i < len(text) && isKeywordLetter(text[i]) { i++ }
	if i == 1 { return false }
	switch text[1:i] {
	case "if", "elif", "else", "for", "with", "let", "include", "raw", "end", "match", "case":
		return true
	}
	return false
}

func trimHorizontal(text string, start int, end int) (int, int) {
	for start < end && horizontal(text[start]) { start++ }
	for end > start && horizontal(text[end-1]) { end-- }
	return start, end
}

func horizontal(b byte) bool { return b == ' ' || b == '\t' }
