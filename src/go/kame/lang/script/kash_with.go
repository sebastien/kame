package script

import (
	"kame/internal/mem"
	"kame/lang/expr"
	"kame/lang/source"
)

func (p *kashParser) withBlock(level int, start int, wordEnd int) *expr.Expr {
	s, text := p.Script, p.Script.Source.Text
	e := mem.Alloc[expr.Expr](s.Alloc)
	e.Kind, e.Span.Start = expr.KashWith, start
	pos := wordEnd
	for len(s.Diagnostics) == 0 {
		for pos < len(text) && space(text[pos]) {
			pos++
		}
		if pos == len(text) || text[pos] == '\n' || text[pos] == '#' {
			break
		}
		keyStart := pos
		key, end := expr.CommandSetupKeyPrefix(text, pos)
		pos = end
		kind, valid := expr.ClassifySetupKey(key)
		if !valid || (kind != expr.SetupCwd && kind != expr.SetupEnvRecord) {
			s.error(keyStart, max(pos, keyStart+1), "with accepts only cwd: and env: setup pairs on its header line")
			break
		}
		for i := range e.Items {
			if e.Items[i].Text == key {
				s.error(keyStart, pos, "duplicate with setup key")
			}
		}
		for pos < len(text) && space(text[pos]) {
			pos++
		}
		if pos == len(text) || text[pos] == '\n' || text[pos] == '#' {
			s.error(keyStart, pos, "with setup requires a value")
			break
		}
		part := expr.ParseCommandSetupValuePrefix(s.Alloc, s.Source, pos, key)
		s.takeDiagnostics(part.Diagnostics)
		setup := mem.Alloc[expr.Expr](s.Alloc)
		setup.Kind, setup.Text, setup.Span = expr.CommandSetup, key, source.Span{Start: keyStart, End: part.End}
		setup.Items = append(setup.Items, part.Expr)
		e.Items = append(e.Items, setup)
		if part.End == pos {
			s.error(pos, pos, "with setup requires a value")
			break
		}
		pos = part.End
	}
	for pos < len(text) && text[pos] != '\n' {
		pos++
	}
	p.Pos = nextLine(text, pos)
	p.branchBody(e, level+1)
	e.Span.End = p.Pos
	return e
}
