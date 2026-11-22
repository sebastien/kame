package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"path"
	"slices"
	"strings"
)

func (p *Program) commandArg(word *expr.Expr, c *Context, cwd string, glob bool) Result {
	pattern := glob && word.CommandGlob
	destination := "argument"
	if !glob {
		destination = "command head"
	}
	previous := c.sequencedOperand
	c.sequencedOperand = previous || pattern
	r := p.renderCommandWord(word, c, destination, pattern)
	c.sequencedOperand = previous
	if r.Waiting || r.Completed || r.Diagnostic.Code != "" {
		return r
	}
	if word.CommandHome && (r.Value.Text == "~" || strings.HasPrefix(r.Value.Text, "~/")) {
		home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
		if !path.IsAbs(home) {
			r.Free(c.Run)
			return failure(c.Run, "RES_INVALID", word.Span, "tilde expansion requires an absolute HOME")
		}
		if pattern {
			var literal strings.Builder
			expr.WriteCommandPatternLiteral(&literal, home)
			home = literal.String()
		}
		value := core.NewString(c.Run, strings.TrimSuffix(home, "/")+r.Value.Text[1:])
		if value.Text == "" {
			value.Text = "/"
		}
		r.Free(c.Run)
		r.Value = value
	}
	if !pattern {
		return r
	}
	defer r.Free(c.Run)
	if cwd == "" {
		cwd = c.commandDirectory()
	}
	name := r.Value.Text
	hasGlob := false
	for _, part := range strings.Split(name, "/") {
		if hasGlob && part == ".." {
			return failure(c.Run, "PAT_INVALID", word.Span, "parent directory cannot follow a command wildcard")
		}
		hasGlob = hasGlob || expr.PathHasGlob(part)
	}
	directoryOnly := strings.HasSuffix(name, "/") || strings.HasSuffix(name, "/.")
	if !path.IsAbs(name) && path.IsAbs(cwd) {
		var directory strings.Builder
		expr.WriteCommandPatternLiteral(&directory, cwd)
		name = path.Join(directory.String(), name)
	} else {
		name = path.Clean(name)
	}
	operation := p.Registry.lookup("wildcard")
	if operation == nil {
		return failure(c.Run, "OP_UNKNOWN", word.Span, "environment does not support wildcard expansion")
	}
	values := []core.Value{{Kind: core.PathPattern, Text: name}}
	if directoryOnly {
		values = append(values, core.Value{Kind: core.Symbol, Text: "+dir"})
	}
	commandGlob := c.CommandGlob
	c.CommandGlob = true
	result := p.invokeOperation(operation, values, nil, c, word.Span)
	c.CommandGlob = commandGlob
	if result.Waiting || result.Completed || result.Diagnostic.Code != "" {
		return result
	}
	if len(result.Value.List) == 0 {
		result.Free(c.Run)
		return failure(c.Run, "GLOB_NO_MATCH", word.Span, "no paths match the command argument")
	}
	result.Value.Unshare(c.Run)
	for i := range result.Value.List {
		match := &result.Value.List[i]
		if cwd != p.DefinitionCwd {
			absolute := canonicalPath(c.Run, p.DefinitionCwd, match.Text)
			mem.FreeString(c.Run, match.Text)
			match.Text = absolute
		}
		if directoryOnly {
			text := owned(c.Run, strings.TrimSuffix(match.Text, "/")+"/")
			mem.FreeString(c.Run, match.Text)
			match.Text = text
		}
	}
	slices.SortFunc(result.Value.List, func(a, b core.Value) int { return strings.Compare(a.Text, b.Text) })
	return result
}
