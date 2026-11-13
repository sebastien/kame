package program

import (
	"kame/diagnostic"
	"kame/lang/definition"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/lang/source"
	"strings"
)

// AppendKash parses one interactive Kash entry into the live session and
// queues its statements after the existing work. AST spans index the session
// source, which grows by the entry text; earlier ASTs keep borrowing the
// previous text because Go owns the storage. The entry becomes a source part
// named name, so diagnostics locate it as its own source. On failure the
// session is unchanged, no work is queued, and diagnostics are entry-relative.
// It returns the index of the first new work item; the caller starts items
// from there through len(s.Work)-1.
func (s *Session) AppendKash(name string, text string) (int, []diagnostic.Diagnostic) {
	p := s.Program
	src := p.Parsed.Source
	first, start := len(s.Work), len(src.Text)
	src.Text += text + "\n"
	part := script.ParseFragment(p.Alloc, src, "kash", start, start+len(text))
	var diagnostics []diagnostic.Diagnostic
	located := func(code string, span source.Span, message string) diagnostic.Diagnostic {
		return diagnostic.Diagnostic{Source: name, Code: code, Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: span.Start - start, End: span.End - start}}
	}
	for _, d := range part.Diagnostics {
		if d.Severity == source.Error {
			diagnostics = append(diagnostics, located(d.Code, d.Span, d.Message))
		}
	}
	var definitions []*definition.Definition
	for _, item := range part.Items {
		switch item.Kind {
		case script.Definition:
			definitions = append(definitions, item.Definition)
		case script.Expression, script.Command:
		default:
			diagnostics = append(diagnostics, located("FEATURE_UNSUP", item.Span, "this declaration is not supported interactively"))
		}
	}
	if len(diagnostics) == 0 {
		diagnostics = p.Eval.RegisterInteractive(definitions)
		for i := range diagnostics {
			diagnostics[i].Source = name
			diagnostics[i].Span.Start -= start
			diagnostics[i].Span.End -= start
		}
	}
	if len(diagnostics) != 0 {
		part.Free()
		src.Text = src.Text[:start]
		return first, diagnostics
	}
	for _, item := range part.Items {
		if item.Kind == script.Definition && item.Definition.Eager {
			s.Work = append(s.Work, SessionWork{Expression: item.Definition.Expression})
		}
		if item.Kind == script.Expression || item.Kind == script.Command {
			s.Work = append(s.Work, SessionWork{Expression: item.Expression, Display: item.Kind == script.Expression})
		}
	}
	appendSourceFragment(p.Parsed, part)
	p.Eval.AddSourcePart(name, start, start+len(text), 0)
	return first, nil
}

// PendingNodes lists engine nodes with outstanding host requests, so an
// interactive driver can wait for one cancelled entry without joining work
// started by earlier entries.
func (p *Program) PendingNodes() []int64 {
	nodes := make([]int64, 0, len(p.Pending))
	for i := range p.Pending {
		nodes = append(nodes, p.Pending[i].NodeID)
	}
	return nodes
}

// SetDirectory moves the session working directory used by later work. When
// followGrants is set, read/write grants naming the previous directory, as
// the default session policy does, move with it; explicit grants never do.
func (p *Program) SetDirectory(directory string, followGrants bool) {
	previous := p.Options.Directory
	p.Options.Directory = cloneText(p.Alloc, directory)
	p.Eval.SetDefinitionCwd(directory)
	if followGrants {
		for i := range p.Options.Grants {
			grant := &p.Options.Grants[i]
			if grant.Capability != eval.Read && grant.Capability != eval.Write {
				continue
			}
			for j := range grant.Names {
				if grant.Names[j] == previous {
					grant.Names[j] = p.Options.Directory
				}
			}
		}
		p.Eval.SetGrants(p.Options.Grants)
	}
}

// SetEnvironment replaces or, when present is false, removes one variable of
// the child environment that later processes inherit. Kame's own process
// environment is unchanged. Names compare exactly, as configured reads do.
func (p *Program) SetEnvironment(name string, value string, present bool) {
	kept := p.Options.Environment[:0]
	for _, entry := range p.Options.Environment {
		key, _, _ := strings.Cut(entry, "=")
		if key != name {
			kept = append(kept, entry)
		}
	}
	p.Options.Environment = kept
	if present {
		p.Options.Environment = append(p.Options.Environment, cloneText(p.Alloc, name+"="+value))
	}
}
