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
	return "", false
}
