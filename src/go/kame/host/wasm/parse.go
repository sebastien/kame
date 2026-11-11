package wasm

import (
	"bytes"
	"kame/internal/mem"
	"kame/lang/ast"
)

// ParseLanguage parses text as lang ("expr", "template", "rule", or "script")
// and returns its schema-1 AST JSON. Parse diagnostics are embedded in the
// JSON, so the caller derives failure from the document exactly as the CLI
// does. The returned text is owned by a.
func ParseLanguage(a mem.Allocator, lang string, name string, text string) PureResult {
	var buffer bytes.Buffer
	ast.WriteAST(&buffer, lang, name, text)
	out := buffer.String()
	result := PureResult{Text: pureText(a, out)}
	buffer.Reset()
	return result
}
