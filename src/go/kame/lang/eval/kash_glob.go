package eval

import (
	"kame/core"
	"kame/internal/mem"
	"kame/lang/expr"
	"path"
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
	result := p.invokeOperation(operation, values, nil, c, word.Span)
	if result.Waiting || result.Completed || result.Diagnostic.Code != "" {
		return result
	}
	result.Value.Unshare(c.Run)
	kept := 0
	for _, match := range result.Value.List {
		absolute := canonicalPath(c.Run, p.DefinitionCwd, match.Text)
		// The filesystem reader also serves explicit wildcard expressions,
		// which include dotfiles. Apply the command-word rule after discovery.
		if strings.Contains(absolute, "/.") && !commandGlobVisible(name, absolute) {
			mem.FreeString(c.Run, absolute)
			match.Free(c.Run)
			continue
		}
		if cwd != p.DefinitionCwd {
			mem.FreeString(c.Run, match.Text)
			match.Text = absolute
		} else {
			mem.FreeString(c.Run, absolute)
		}
		if directoryOnly {
			text := owned(c.Run, strings.TrimSuffix(match.Text, "/")+"/")
			mem.FreeString(c.Run, match.Text)
			match.Text = text
		}
		result.Value.List[kept] = match
		kept++
	}
	result.Value.List = result.Value.List[:kept]
	if kept == 0 {
		result.Free(c.Run)
		return failure(c.Run, "GLOB_NO_MATCH", word.Span, "no paths match the command argument")
	}
	return result
}

func commandGlobVisible(pattern, match string) bool {
	parts, names := strings.Split(pattern, "/"), strings.Split(match, "/")
	// Each reachable index is a prefix match after the current pattern part.
	reached := make([]bool, len(names)+1)
	reached[0] = true
	for _, part := range parts {
		if part == "**" {
			for i, name := range names {
				reached[i+1] = reached[i+1] || reached[i] && !strings.HasPrefix(name, ".")
			}
			continue
		}
		dot := strings.HasPrefix(part, ".") || strings.HasPrefix(part, `\.`)
		for i := len(names) - 1; i >= 0; i-- {
			matched, _ := path.Match(part, names[i])
			reached[i+1] = reached[i] && matched && (dot || !strings.HasPrefix(names[i], "."))
		}
		reached[0] = false
	}
	return reached[len(names)]
}
