package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/template"

	"path"
	"strings"
	"unicode/utf8"
)

func opRender(c *eval.Context, _ any, v []core.Value) eval.Result {
	return render(c, v, false)
}

// Template includes use the file loader independently of render's inline
// String and Bytes sources. The payload, when present, precedes the path.
func opTemplateInclude(c *eval.Context, _ any, v []core.Value) eval.Result {
	if len(v) == 2 && v[0].Kind != core.Record {
		return invalidArgument(c, v, 0, "record")
	}
	return render(c, v, true)
}

func render(c *eval.Context, v []core.Value, include bool) eval.Result {
	previousFinalization := c.FinalizationForbidden
	c.FinalizationForbidden = true
	defer restoreRenderFinalization(c, previousFinalization)
	if len(v) < 1 || len(v) > 3 {
		discardArgCallables(c, v)
		return c.InvalidOperation("expects 1 to 3 arguments")
	}
	var payload, styleArg *core.Value
	sourceIndex := len(v) - 1
	if len(v) == 2 {
		switch {
		case v[0].Kind == core.Record:
			payload = &v[0]
		case !include && v[1].Kind == core.Record:
			payload, sourceIndex = &v[1], 0
		case v[0].Kind == core.String:
			styleArg = &v[0]
		default:
			return invalidArgument(c, v, 0, "record or string")
		}
	}
	if len(v) == 3 {
		if v[0].Kind != core.Record {
			return invalidArgument(c, v, 0, "record")
		}
		if v[1].Kind != core.String {
			return invalidArgument(c, v, 1, "string")
		}
		payload, styleArg = &v[0], &v[1]
	}
	src := &v[sourceIndex]
	isFile := src.Kind == core.Path
	filePath := src.Text
	if include {
		if !src.IsPathText() {
			return invalidArgument(c, v, sourceIndex, "path or string")
		}
		if filePath == "" || strings.IndexByte(filePath, 0) >= 0 {
			discardArgCallables(c, v)
			return c.InvalidOperation("template include requires a nonempty path without NUL")
		}
		isFile = true
		filePath = templateIncludePath(c, filePath)
		defer mem.FreeString(c.Run, filePath)
	} else if !isFile && src.Kind != core.String && src.Kind != core.Bytes {
		return invalidArgument(c, v, sourceIndex, "path, string or bytes")
	}
	if isFile && strings.HasPrefix(filePath, "~/") {
		home, _ := c.Unit.EnvironmentValue(c.Environment, "HOME")
		if !path.IsAbs(home) {
			discardArgCallables(c, v)
			return styledFailure(c, "FS_ERR", "cannot resolve home directory for template")
		}
		filePath = path.Join(home, filePath[2:])
	}

	style := "plain"
	validStyle := true
	if styleArg != nil {
		style, validStyle = template.NormalizeStyle(styleArg.Text)
	} else if isFile {
		style, validStyle = template.InferStyle(filePath)
	}
	if !validStyle {
		discardArgCallables(c, v)
		if styleArg != nil {
			return styledFailure(c, "TPL_STYLE", "unknown comment style")
		}
		return styledFailure(c, "TPL_STYLE", "unknown template extension")
	}

	sourceName := "<render>"
	content := src.Text
	if isFile {
		sourceName = c.CanonicalPath(filePath)
		defer mem.FreeString(c.Run, sourceName)
		for _, parent := range c.RenderStack {
			if parent == sourceName {
				discardArgCallables(c, v)
				return styledFailure(c, "TPL_CYCLE", "recursive template inclusion")
			}
		}
		if !c.Allows(eval.Read, filePath) {
			discardArgCallables(c, v)
			return failure("CAP_DENIED", "read access denied")
		}
		if !c.DirectHostRequests {
			key := core.NewResourceKey(c.Run, core.ResourceFile, filePath)
			current := c.Dependency(key)
			key.Free(c.Run)
			if !current {
				discardArgCallables(c, v)
				return eval.Result{Waiting: true}
			}
		}
		got := readFileBytes(c, filePath)
		if got.Waiting || got.Diagnostic.Code != "" {
			discardArgCallables(c, v)
			return got
		}
		defer got.Value.Free(c.Run)
		switch got.Value.Kind {
		case core.Bytes:
			if !utf8.Valid(got.Value.Bytes) {
				discardArgCallables(c, v)
				return c.InvalidOperation("template file contains bytes that are not valid UTF-8")
			}
			content = string(got.Value.Bytes)
		case core.String:
			content = got.Value.Text
		default:
			discardArgCallables(c, v)
			return c.InvalidOperation("template file read must return bytes or string")
		}

		// Context copies borrow their caller's stack.
		previousStack := c.RenderStack
		c.RenderStack = make([]string, len(previousStack)+1)
		copy(c.RenderStack, previousStack)
		c.RenderStack[len(previousStack)] = sourceName
		defer func() {
			mem.FreeSlice(nil, c.RenderStack)
			c.RenderStack = previousStack
		}()
	} else if src.Kind == core.Bytes {
		if !utf8.Valid(src.Bytes) {
			discardArgCallables(c, v)
			return c.InvalidOperation("template source contains bytes that are not valid UTF-8")
		}
		content = string(src.Bytes)
	}

	doc := template.ParseDocument(c.Run, sourceName, content, style)
	defer doc.Free()
	if len(doc.Diagnostics) != 0 {
		diag := doc.Diagnostics[0]
		discardArgCallables(c, v)
		return renderFailure(c, diag.Code, sourceName, diag.Span.Start, diag.Span.End, diag.Message)
	}
	// Evaluate with payload scope and template source.
	prevSource := c.Source
	prevScope := c.Scope
	c.Source = sourceName
	var child *eval.Scope
	if payload != nil {
		child = eval.RenderChildScope(c, prevScope, *payload)
		if child == nil {
			c.Source = prevSource
			discardArgCallables(c, v)
			return c.InvalidOperation("cannot create template payload scope")
		}
		c.Scope = child
	}
	previousClone := c.CloneCallableAST
	c.CloneCallableAST = true
	res := c.Program.EvaluateWith(doc.Root, c)
	c.CloneCallableAST = previousClone
	c.Source = prevSource
	c.Scope = prevScope
	if child != nil {
		child.Free()
	}
	// Payload callables transferred into the child scope above; only the caller's
	// shallow argument-storage free remains, even if evaluation failed or waited.
	if res.Waiting || res.Diagnostic.Code != "" {
		res.Value.Free(c.Run)
		return res
	}
	if res.Value.Kind != core.String {
		d := c.InvalidOperation("template must produce string; got " + core.KindName(res.Value.Kind))
		res.Value.Free(c.Run)
		return d
	}
	return res
}

func restoreRenderFinalization(c *eval.Context, previous bool) { c.FinalizationForbidden = previous }

func opTemplateMap(c *eval.Context, state any, values []core.Value) eval.Result {
	if values[1].Kind != core.List {
		return invalidArgument(c, values, 1, "list")
	}
	return opMap(c, state, values)
}

// opTemplateApply splats the already-built positional argument list. It keeps
// document loop calls distinct from apply's unary list-consumer rule.
func opTemplateApply(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	if values[0].Kind != core.Callable {
		return invalidArgument(c, values, 0, "callable")
	}
	if values[1].Kind != core.List {
		return invalidArgument(c, values, 1, "list")
	}
	result := c.Call(values[0], values[1].List)
	discardArgCallables(c, values)
	return result
}

func opTemplateTrimStart(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	return opTemplateTrim(c, values, true)
}

func opTemplateTrimEnd(c *eval.Context, state any, values []core.Value) eval.Result {
	_ = state
	return opTemplateTrim(c, values, false)
}

func opTemplateTrim(c *eval.Context, values []core.Value, trimStart bool) eval.Result {
	if values[0].Kind != core.String {
		return invalidArgument(c, values, 0, "string")
	}
	text := values[0].Text
	start, end := 0, len(text)
	if trimStart {
		for start < end && isTemplateWhitespace(text[start]) {
			start++
		}
	} else {
		for end > start && isTemplateWhitespace(text[end-1]) {
			end--
		}
	}
	return eval.Result{Value: core.NewString(c.Run, text[start:end])}
}

func isTemplateWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func templateIncludePath(c *eval.Context, name string) string {
	if path.IsAbs(name) || strings.HasPrefix(name, "~/") || core.IsResourceURIName(name) {
		return cloneText(c.Run, name)
	}
	if core.IsResourceURIName(c.Source) {
		parsed := core.ParseResourceURI(c.Run, c.Source)
		defer parsed.URI.Free()
		if parsed.Error == "" {
			base := parsed.URI
			base.Path = path.Join(path.Dir(base.Path), name)
			return base.Canonical(c.Run)
		}
	}
	dir := c.SourceDirectory()
	defer dir.Free(c.Run)
	if dir.Diagnostic.Code == "" {
		return cloneText(c.Run, path.Join(dir.Value.Text, name))
	}
	if c.Source != "" && c.Source[0] != '<' {
		return c.CanonicalPath(path.Join(path.Dir(c.Source), name))
	}
	return c.CanonicalPath(name)
}

func styledFailure(c *eval.Context, code string, msg string) eval.Result {
	return renderFailure(c, code, c.Source, c.Span.Start, c.Span.End, msg)
}

func renderFailure(c *eval.Context, code string, srcName string, start int, end int, msg string) eval.Result {
	a := c.Run
	codeOwned := ""
	if len(code) != 0 {
		b := mem.AllocSlice[byte](a, len(code), len(code))
		copy(b, []byte(code))
		codeOwned = string(b)
	}
	srcOwned := ""
	if len(srcName) != 0 {
		b := mem.AllocSlice[byte](a, len(srcName), len(srcName))
		copy(b, []byte(srcName))
		srcOwned = string(b)
	}
	msgOwned := ""
	if len(msg) != 0 {
		b := mem.AllocSlice[byte](a, len(msg), len(msg))
		copy(b, []byte(msg))
		msgOwned = string(b)
	}
	return eval.Result{Diagnostic: diagnostic.Diagnostic{Code: codeOwned, Severity: diagnostic.Error, Message: msgOwned, Source: srcOwned, Span: diagnostic.Span{Start: start, End: end}, Owned: true}}
}

func isExplicitPath(text string) bool {
	if len(text) >= 1 && text[0] == '/' {
		return true
	}
	if len(text) >= 2 && text[0] == '.' && text[1] == '/' {
		return true
	}
	if len(text) >= 3 && text[0] == '.' && text[1] == '.' && text[2] == '/' {
		return true
	}
	return false
}

func readFileBytes(c *eval.Context, path string) eval.Result {
	// Retain each completed read across nested include waits. Otherwise an outer
	// render can consume the inner include's completion as its own source.
	return request(c, host.RequestReadFile, host.FilePayload(c.Run, host.OpRead, path))
}
