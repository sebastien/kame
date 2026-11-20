package script

import (
	"kame/lang/expr"
	"kame/lang/source"
	"path"
	"strings"
)

type ModuleName struct {
	Name      string
	Span      source.Span
	Alias     string
	AliasSpan source.Span
}

type ModuleDeclaration struct {
	SourceOrigin int
	Path         string
	PathSpelling string
	PathSpan     source.Span
	Alias        string
	AliasSpan    source.Span
	Names        []ModuleName
	Comment      string
}

func (m *ModuleDeclaration) Namespace() string {
	if m.Alias != "" {
		return m.Alias
	}
	name := path.Base(m.Path)
	switch suffix := path.Ext(name); suffix {
	case ".km", ".kame", ".kash", ".ksh":
		return strings.TrimSuffix(name, suffix)
	}
	return name
}

func validModuleNamespace(name string) bool {
	switch name {
	case "x", "env", "std", "module":
		return false
	}
	return expr.ValidBindingName(name)
}

func moduleKeyword(word string) bool {
	return word == "import" || word == "from" || word == "export"
}

func (s *Script) moduleDeclaration(word string, start, operand, end int) ScriptItem {
	text := s.Source.Text
	m := &ModuleDeclaration{}
	item := ScriptItem{Module: m, Span: source.Span{Start: start, End: end}}
	switch word {
	case "import":
		item.Kind = ModuleImport
	case "from":
		item.Kind = ModuleFrom
	case "export":
		item.Kind = ModuleExport
	}
	skipSpace := func(pos int) int {
		for {
			pos = trimStart(text, pos, end)
			next := source.ContinuationEnd(text, pos, end)
			if next == pos {
				return pos
			}
			pos = next
		}
	}
	pos := skipSpace(operand)
	readToken := func() (string, source.Span) {
		begin := pos
		for pos < end && !space(text[pos]) && text[pos] != '\n' && text[pos] != ',' && text[pos] != '#' && source.ContinuationEnd(text, pos, end) == pos {
			pos++
		}
		token := text[begin:pos]
		span := source.Span{Start: begin, End: pos}
		pos = skipSpace(pos)
		return token, span
	}
	readName := func() (string, source.Span, bool) {
		name, span := readToken()
		if !expr.ValidBindingName(name) {
			s.error(span.Start, span.End, "expected a module binding name")
			return name, span, false
		}
		return name, span, true
	}
	if word != "export" {
		path, span, ok := s.literalSourcePath(pos, end)
		if !ok {
			s.error(pos, end, "expected a literal rooted module path without globs or interpolation")
			return item
		}
		m.Path, m.PathSpan, m.PathSpelling = path, span, text[span.Start:span.End]
		if strings.HasSuffix(path, ".kmt") {
			s.Diagnostics = append(s.Diagnostics, source.Diagnostic{Code: "RES_INVALID", Severity: source.Error, Message: "a document template cannot be imported as a module", Span: span})
			return item
		}
		pos = skipSpace(span.End)
	}
	if word == "import" {
		if pos < end && text[pos] != '#' {
			token, span := readToken()
			if token != "as" {
				s.error(span.Start, end, "expected 'as' followed by a namespace alias")
				return item
			}
			var ok bool
			m.Alias, m.AliasSpan, ok = readName()
			if !ok {
				return item
			}
		}
		if namespace := m.Namespace(); !validModuleNamespace(namespace) {
			span, message := m.AliasSpan, "invalid or reserved namespace alias: "+namespace
			if m.Alias == "" {
				span, message = m.PathSpan, "module path requires an unreserved namespace alias: "+namespace
			}
			s.Diagnostics = append(s.Diagnostics, source.Diagnostic{Code: "DEF_INVALID", Severity: source.Error, Message: message, Span: span})
		}
	} else {
		if word == "from" {
			token, span := readToken()
			if token != "import" {
				s.error(span.Start, span.End, "expected 'import' after the module path")
				return item
			}
		}
		for {
			name, span, ok := readName()
			if !ok {
				return item
			}
			binding := ModuleName{Name: name, Span: span}
			if word == "from" && pos < end && text[pos] != '#' && text[pos] != ',' {
				token, span := readToken()
				if token != "as" {
					s.error(span.Start, span.End, "expected 'as' or a comma after an import name")
					return item
				}
				binding.Alias, binding.AliasSpan, ok = readName()
				if !ok {
					return item
				}
			}
			m.Names = append(m.Names, binding)
			if pos == end || text[pos] != ',' {
				break
			}
			pos = skipSpace(pos + 1)
		}
	}
	if pos < end {
		if text[pos] != '#' {
			s.error(pos, end, "unexpected text after module declaration")
		} else {
			m.Comment = strings.TrimRight(text[pos:end], " \t\r")
		}
	}
	return item
}

func writeModuleDeclaration(b *strings.Builder, item ScriptItem) {
	m := item.Module
	switch item.Kind {
	case ModuleImport:
		b.WriteString("import ")
		b.WriteString(m.PathSpelling)
		if m.Alias != "" {
			b.WriteString(" as ")
			b.WriteString(m.Alias)
		}
	case ModuleFrom:
		b.WriteString("from ")
		b.WriteString(m.PathSpelling)
		b.WriteString(" import ")
	case ModuleExport:
		b.WriteString("export ")
	}
	for i, name := range m.Names {
		if i != 0 {
			b.WriteString(", ")
		}
		b.WriteString(name.Name)
		if name.Alias != "" {
			b.WriteString(" as ")
			b.WriteString(name.Alias)
		}
	}
	if m.Comment != "" {
		b.WriteByte(' ')
		b.WriteString(m.Comment)
	}
}
