package expr

import (
	"solod.dev/so/slices"
)

// IsCommandGroup discriminates only at process-operand position. Nested value
// expressions, captures, strings, and collection commas belong to their grammar.
func IsCommandGroup(text string, start int) bool {
	if start >= len(text) || text[start] != '(' { return false }
	depth, collections := 0, 0
	quoted := false
	for i := start; i < len(text); i++ {
		b := text[i]
		if b == '\\' { i++; continue }
		if b == '"' { quoted = !quoted; continue }
		if quoted { continue }
		if b == '#' && (i == start || commandWhitespace(text[i-1])) {
			for i < len(text) && text[i] != '\n' { i++ }
			continue
		}
		if b == '[' || b == '{' { collections++ }
		if b == ']' || b == '}' { collections-- }
		if b == '(' { depth++ }
		if b == ')' { depth--; if depth == 0 { return false } }
		if b == ',' && depth == 1 && collections == 0 { return true }
	}
	return false
}

func (p *parser) processGroup() *Expr {
	group := p.node(CommandGroup, p.pos)
	p.pos++
	for {
		p.commandSpace(true)
		group.Items = slices.Append(p.a, group.Items, p.commandPipeline(p.pos, false, true))
		if len(p.diags) != 0 { break }
		if p.pos == len(p.s.Text) { p.error(group.Span.Start, p.pos, "unclosed Kash process group"); break }
		if p.s.Text[p.pos] == ')' { p.pos++; break }
		if p.s.Text[p.pos] != ',' { p.error(p.pos, p.pos+1, "expected comma between process group members"); break }
		p.pos++
	}
	group.Span.End = p.pos
	return group
}
