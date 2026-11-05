package script

import (
	"kame/lang/expr"
	"kame/lang/source"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
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
		for pos < len(text) && !space(text[pos]) && text[pos] != '\n' {
			pos++
		}
		key := text[keyStart:pos]
		kind, valid := expr.SetupInvalid, false
		if len(key) > 1 {
			kind, valid = expr.ClassifySetupKey(key[1:])
		}
		if !valid || (kind != expr.SetupCwd && kind != expr.SetupEnvRecord) {
			s.error(keyStart, pos, "with accepts only :cwd and :env setup pairs on its header line")
			break
		}
		for i := range e.Items {
			if e.Items[i].Text == key[1:] {
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
		part := expr.ParseCommandSetupValuePrefix(s.Alloc, s.Source, pos)
		s.takeDiagnostics(part.Diagnostics)
		setup := mem.Alloc[expr.Expr](s.Alloc)
		setup.Kind, setup.Text, setup.Span = expr.CommandSetup, key[1:], source.Span{Start: keyStart, End: part.End}
		setup.Items = slices.Append(s.Alloc, setup.Items, part.Expr)
		e.Items = slices.Append(s.Alloc, e.Items, setup)
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
