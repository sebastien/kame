package template

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// DocumentFormatResult carries canonical document text or the first source
// diagnostic. Its strings are owned by Allocator.
type DocumentFormatResult struct {
	Allocator mem.Allocator
	Text      string
	Code      string
	Message   string
	Span      source.Span
	OK        bool
}

func (r *DocumentFormatResult) Free() {
	if r == nil {
		return
	}
	a := r.Allocator
	mem.FreeString(a, r.Text)
	mem.FreeString(a, r.Code)
	mem.FreeString(a, r.Message)
	*r = DocumentFormatResult{}
	mem.Free(a, r)
}

// FormatDocument canonicalizes recognized directive tokens while copying all
// other source bytes unchanged. Empty style selects auto inference.
func FormatDocument(a mem.Allocator, name string, text string, requestedStyle string) *DocumentFormatResult {
	style := requestedStyle
	if style == "" {
		style = "auto"
	}
	if style == "auto" {
		var ok bool
		style, ok = ResolveAutoStyle(name, text)
		if !ok {
			return documentFormatError(a, "TPL_STYLE", "ambiguous template comment style")
		}
	} else {
		var ok bool
		style, ok = NormalizeStyle(style)
		if !ok {
			return documentFormatError(a, "TPL_STYLE", "unknown comment style")
		}
	}
	doc := ParseDocument(a, name, text, style)
	if len(doc.Diagnostics) != 0 {
		diag := doc.Diagnostics[0]
		result := documentFormatError(a, diag.Code, diag.Message)
		result.Span = diag.Span
		doc.Free()
		return result
	}
	doc.Free()

	sourceText := source.New(a, name, text)
	p := docParser{a: a, s: sourceText, style: style}
	out := strings.NewBuilder(a)
	defer out.Free()
	var blocks []int
	cur := 0
	for cur < len(text) {
		line := scanLine(text, cur, len(text))
		if len(blocks) != 0 && blocks[len(blocks)-1] == dirRaw {
			if p.isEndLine(line.Start, line.ContentEnd) {
				directive := p.directiveOnLine(line.Start, line.ContentEnd)
				indentEnd := line.Start
				for indentEnd < line.ContentEnd && horizontal(text[indentEnd]) {
					indentEnd++
				}
				trailingStart := line.ContentEnd
				for trailingStart > indentEnd && horizontal(text[trailingStart-1]) {
					trailingStart--
				}
				out.WriteString(text[cur:indentEnd])
				canonical := canonicalDirective(a, directive, &blocks)
				wrapped := canonicalComment(a, style, canonical)
				out.WriteString(wrapped)
				mem.FreeString(a, wrapped)
				out.WriteString(text[trailingStart:line.ContentEnd])
				freeArgs(a, directive.Args)
				mem.FreeString(a, canonical)
				if line.LineEnd > line.ContentEnd {
					out.WriteString(text[line.ContentEnd:line.LineEnd])
				}
				cur = line.LineEnd
				continue
			}
			out.WriteString(text[cur:line.LineEnd])
			cur = line.LineEnd
			continue
		}
		lineDirective := p.directiveOnLine(line.Start, line.ContentEnd)
		if lineDirective.Kind != dirNone && lineDirective.Kind != dirError {
			indentEnd := line.Start
			for indentEnd < line.ContentEnd && horizontal(text[indentEnd]) {
				indentEnd++
			}
			trailingStart := line.ContentEnd
			for trailingStart > indentEnd && horizontal(text[trailingStart-1]) {
				trailingStart--
			}
			out.WriteString(text[cur:indentEnd])
			canonical := canonicalDirective(a, lineDirective, &blocks)
			wrapped := canonicalComment(a, style, canonical)
			out.WriteString(wrapped)
			mem.FreeString(a, wrapped)
			out.WriteString(text[trailingStart:line.ContentEnd])
			if line.LineEnd > line.ContentEnd {
				out.WriteString(text[line.ContentEnd:line.LineEnd])
			}
			freeArgs(a, lineDirective.Args)
			mem.FreeString(a, canonical)
			cur = line.LineEnd
			continue
		}
		if p.nextInlineBlockDirective(cur, line.ContentEnd) != 0 {
			directive := p.inlineFound
			out.WriteString(text[cur:directive.Start])
			canonical := canonicalDirective(a, directive.Dir, &blocks)
			if style == "html" || style == "c" || style == "powershell" {
				wrapped := canonicalComment(a, style, canonical)
				out.WriteString(wrapped)
				mem.FreeString(a, wrapped)
			} else {
				out.WriteString(canonical)
			}
			freeArgs(a, directive.Dir.Args)
			mem.FreeString(a, canonical)
			cur = directive.End
			continue
		}
		out.WriteString(text[cur:line.LineEnd])
		cur = line.LineEnd
	}
	slices.Free(a, blocks)
	sourceText.Free(a)
	result := mem.Alloc[DocumentFormatResult](a)
	result.Allocator, result.Text, result.OK = a, cloneDocumentFormatText(a, out.String()), true
	return result
}

func documentFormatError(a mem.Allocator, code string, message string) *DocumentFormatResult {
	result := mem.Alloc[DocumentFormatResult](a)
	result.Allocator = a
	result.Code = cloneDocumentFormatText(a, code)
	result.Message = cloneDocumentFormatText(a, message)
	return result
}

func cloneDocumentFormatText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	buffer := mem.AllocSlice[byte](a, len(text), len(text))
	copy(buffer, text)
	return string(buffer)
}

func canonicalDirective(a mem.Allocator, d dirOut, blocks *[]int) string {
	var name string
	switch d.Kind {
	case dirIf:
		name = "if"
	case dirElif:
		name = "elif"
	case dirElse:
		name = "else"
	case dirFor:
		name = "for"
	case dirWith:
		name = "with"
	case dirLet:
		name = "let"
	case dirInclude:
		name = "include"
	case dirRaw:
		name = "raw"
	case dirEnd:
		name = "end"
	case dirMatch:
		name = "match"
	case dirCase:
		name = "case"
	default:
		return ""
	}
	out := strings.NewBuilder(a)
	out.WriteString("@")
	if d.TrimLeft {
		out.WriteString("-")
	}
	out.WriteString(name)
	if d.Kind == dirElse || d.Kind == dirRaw || (d.Kind == dirEnd && len(d.Args) == 0) {
		if d.Kind == dirEnd && len(*blocks) != 0 {
			out.WriteString("(")
			out.WriteString(blockName((*blocks)[len(*blocks)-1]))
			out.WriteString(")")
		}
	} else {
		out.WriteString("(")
		for i := range d.Args {
			if i != 0 {
				out.WriteString(" ")
			}
			formatted := expr.Format(a, d.Args[i])
			out.WriteString(formatted)
			mem.FreeString(a, formatted)
		}
		out.WriteString(")")
	}
	if d.TrimRight {
		out.WriteString("-")
	}
	text := cloneDocumentFormatText(a, out.String())
	out.Free()
	if d.Kind == dirIf || d.Kind == dirFor || d.Kind == dirWith || d.Kind == dirMatch || d.Kind == dirRaw {
		*blocks = slices.Append(a, *blocks, d.Kind)
	} else if d.Kind == dirEnd && len(*blocks) != 0 {
		*blocks = (*blocks)[:len(*blocks)-1]
	}
	return text
}

func blockName(kind int) string {
	switch kind {
	case dirIf:
		return "if"
	case dirFor:
		return "for"
	case dirWith:
		return "with"
	case dirMatch:
		return "match"
	case dirRaw:
		return "raw"
	default:
		return ""
	}
}

func canonicalComment(a mem.Allocator, style string, directive string) string {
	out := strings.NewBuilder(a)
	switch style {
	case "html":
		out.WriteString("<!-- ")
	case "c":
		out.WriteString("/* ")
	case "powershell":
		out.WriteString("<# ")
	case "hash":
		out.WriteString("# ")
	case "dash":
		out.WriteString("-- ")
	case "semi":
		out.WriteString("; ")
	case "percent":
		out.WriteString("% ")
	case "batch":
		out.WriteString("REM ")
	}
	out.WriteString(directive)
	switch style {
	case "html":
		out.WriteString(" -->")
	case "c":
		out.WriteString(" */")
	case "powershell":
		out.WriteString(" #>")
	}
	result := cloneDocumentFormatText(a, out.String())
	out.Free()
	return result
}
