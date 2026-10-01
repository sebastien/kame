package operations

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/lang/eval"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/unicode/utf8"
)

func opRender(c *eval.Context, s any, v []core.Value) eval.Result {
	_ = s
	if len(v) < 1 || len(v) > 3 {
		freeArgCallables(c, v)
		return c.InvalidOperation("expects 1 to 3 arguments")
	}
	// Split PAYLOAD vs STYLE for 2-arg form: record is payload, string is style.
	var payload *core.Value
	var styleArg *core.Value
	if len(v) == 2 {
		if v[1].Kind == core.Record {
			payload = &v[1]
		} else if v[1].Kind == core.String {
			styleArg = &v[1]
		} else {
			return invalidArgument(c, v, 1, "record or string")
		}
	}
	if len(v) == 3 {
		if v[1].Kind != core.Record {
			return invalidArgument(c, v, 1, "record")
		}
		if v[2].Kind != core.String {
			return invalidArgument(c, v, 2, "string")
		}
		payload = &v[1]
		styleArg = &v[2]
	}
	if payload != nil && payload.Kind != core.Record {
		return invalidArgument(c, v, 1, "record")
	}
	src := &v[0]
	if src.Kind != core.String && src.Kind != core.Bytes {
		return invalidArgument(c, v, 0, "string or bytes")
	}
	// Resolve source bytes, style, and file identity.
	var content string
	var contentOwned bool
	var style string
	var filePath string
	var isFile bool
	if src.Kind == core.Bytes {
		if styleArg == nil {
			freeArgCallables(c, v)
			return styledFailure(c, "TPL_STYLE", "bytes template needs an explicit style")
		}
		norm, ok := template.NormalizeStyle(styleArg.Text)
		if !ok {
			freeArgCallables(c, v)
			return styledFailure(c, "TPL_STYLE", "unknown comment style")
		}
		style = norm
		if !utf8.Valid(src.Bytes) {
			freeArgCallables(c, v)
			return c.InvalidOperation("argument 1 contains bytes that are not valid UTF-8")
		}
		if len(src.Bytes) != 0 {
			b := mem.AllocSlice[byte](c.Run, len(src.Bytes), len(src.Bytes))
			copy(b, src.Bytes)
			content = string(b)
			contentOwned = true
		}
	} else {
		if isExplicitPath(src.Text) {
			isFile = true
			filePath = src.Text
			// Cycle check before reading.
			for i := range c.RenderStack {
				if c.RenderStack[i] == filePath {
					freeArgCallables(c, v)
					return styledFailure(c, "TPL_CYCLE", "recursive template inclusion")
				}
			}
			if !c.Allows(eval.Read, filePath) {
				freeArgCallables(c, v)
				return failure("CAP_DENIED", "read access denied")
			}
			if !c.DirectHostRequests {
				key := core.NewResourceKey(c.Run, core.ResourceFile, filePath)
				current := c.Dependency(key)
				key.Free(c.Run)
				if !current {
					freeArgCallables(c, v)
					return eval.Result{Waiting: true}
				}
			}
			got := readFileBytes(c, filePath)
			if got.Waiting || got.Diagnostic.Code != "" {
				freeArgCallables(c, v)
				if contentOwned {
					mem.FreeString(c.Run, content)
				}
				return got
			}
			if got.Value.Kind == core.Bytes {
				if !utf8.Valid(got.Value.Bytes) {
					got.Value.Free(c.Run)
					freeArgCallables(c, v)
					return c.InvalidOperation("template file contains bytes that are not valid UTF-8")
				}
				if len(got.Value.Bytes) != 0 {
					b := mem.AllocSlice[byte](c.Run, len(got.Value.Bytes), len(got.Value.Bytes))
					copy(b, got.Value.Bytes)
					content = string(b)
					contentOwned = true
				}
				got.Value.Free(c.Run)
			} else if got.Value.Kind == core.String {
				content = got.Value.Text
				// Transfer ownership: got.Value.Text is Run-owned; keep it.
				// Prevent double free by clearing got before Free.
				contentOwned = true
				// got.Value.Free would free Text; instead take ownership.
				// Mark got as empty so Free is safe.
				got.Value = core.Value{}
			} else {
				got.Value.Free(c.Run)
				freeArgCallables(c, v)
				if contentOwned {
					mem.FreeString(c.Run, content)
				}
				return c.InvalidOperation("template file read must return bytes or string")
			}
			if styleArg != nil {
				norm, ok := template.NormalizeStyle(styleArg.Text)
				if !ok {
					freeArgCallables(c, v)
					if contentOwned {
						mem.FreeString(c.Run, content)
					}
					return styledFailure(c, "TPL_STYLE", "unknown comment style")
				}
				style = norm
			} else {
				inferred, ok := inferStyle(filePath)
				if !ok {
					freeArgCallables(c, v)
					if contentOwned {
						mem.FreeString(c.Run, content)
					}
					return styledFailure(c, "TPL_STYLE", "unknown template extension")
				}
				style = inferred
			}
		} else {
			// String content.
			content = src.Text
			if styleArg != nil {
				norm, ok := template.NormalizeStyle(styleArg.Text)
				if !ok {
					freeArgCallables(c, v)
					return styledFailure(c, "TPL_STYLE", "unknown comment style")
				}
				style = norm
			} else {
				style = "plain"
			}
		}
	}
	// Push cycle entry for files.
	pushed := false
	if isFile {
		owned := ""
		if len(filePath) != 0 {
			b := mem.AllocSlice[byte](c.Run, len(filePath), len(filePath))
			copy(b, []byte(filePath))
			owned = string(b)
		}
		c.RenderStack = slices.Append(c.Run, c.RenderStack, owned)
		pushed = true
	}
	// Parse.
	sourceName := "<render>"
	if isFile {
		sourceName = filePath
	}
	doc := template.ParseDocument(c.Run, sourceName, content, style)
	if contentOwned && src.Kind == core.Bytes {
		mem.FreeString(c.Run, content)
	}
	if src.Kind == core.String && isFile && contentOwned {
		mem.FreeString(c.Run, content)
	}
	if len(doc.Diagnostics) != 0 {
		diag := doc.Diagnostics[0]
		code := diag.Code
		msg := diag.Message
		span := diag.Span
		srcName := sourceName
		doc.Free()
		if pushed {
			popRenderStack(c)
		}
		freeArgCallables(c, v)
		return renderFailure(c, code, srcName, span.Start, span.End, msg)
	}
	// Evaluate with payload scope and template source.
	prevSource := c.Source
	prevScope := c.Scope
	c.Source = sourceName
	var child *eval.Scope
	evalScope := c.Scope
	if payload != nil {
		child = eval.RenderChildScope(c, prevScope, *payload)
		if child == nil {
			c.Source = prevSource
			doc.Free()
			if pushed {
				popRenderStack(c)
			}
			freeArgCallables(c, v)
			return c.InvalidOperation("cannot create template payload scope")
		}
		evalScope = child
		c.Scope = child
	}
	_ = evalScope
	res := c.Program.EvaluateWith(doc.Root, c)
	c.Source = prevSource
	c.Scope = prevScope
	if child != nil {
		child.Free()
	}
	doc.Free()
	if pushed {
		popRenderStack(c)
	}
	freeArgCallables(c, v)
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

func popRenderStack(c *eval.Context) {
	if len(c.RenderStack) == 0 {
		return
	}
	last := len(c.RenderStack) - 1
	mem.FreeString(c.Run, c.RenderStack[last])
	c.RenderStack = c.RenderStack[:last]
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

func inferStyle(path string) (string, bool) {
	// Extension after last dot in basename, case-insensitive.
	baseStart := 0
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			baseStart = i + 1
			break
		}
	}
	dot := -1
	for i := baseStart; i < len(path); i++ {
		if path[i] == '.' {
			dot = i
		}
	}
	if dot < 0 || dot+1 >= len(path) {
		return "", false
	}
	ext := path[dot+1:]
	return extStyle(ext)
}

func extStyle(ext string) (string, bool) {
	if eqFoldExt(ext, "html") || eqFoldExt(ext, "htm") || eqFoldExt(ext, "xml") || eqFoldExt(ext, "svg") || eqFoldExt(ext, "vue") || eqFoldExt(ext, "md") || eqFoldExt(ext, "markdown") {
		return "html", true
	}
	if eqFoldExt(ext, "c") || eqFoldExt(ext, "h") || eqFoldExt(ext, "cc") || eqFoldExt(ext, "cpp") || eqFoldExt(ext, "hpp") || eqFoldExt(ext, "java") || eqFoldExt(ext, "js") || eqFoldExt(ext, "mjs") || eqFoldExt(ext, "ts") || eqFoldExt(ext, "tsx") || eqFoldExt(ext, "jsx") || eqFoldExt(ext, "go") || eqFoldExt(ext, "rs") || eqFoldExt(ext, "css") || eqFoldExt(ext, "scss") || eqFoldExt(ext, "less") || eqFoldExt(ext, "php") || eqFoldExt(ext, "swift") || eqFoldExt(ext, "kt") {
		return "c", true
	}
	if eqFoldExt(ext, "sh") || eqFoldExt(ext, "bash") || eqFoldExt(ext, "zsh") || eqFoldExt(ext, "yaml") || eqFoldExt(ext, "yml") || eqFoldExt(ext, "py") || eqFoldExt(ext, "rb") || eqFoldExt(ext, "toml") || eqFoldExt(ext, "ini") || eqFoldExt(ext, "conf") || eqFoldExt(ext, "properties") || eqFoldExt(ext, "pl") || eqFoldExt(ext, "r") {
		return "hash", true
	}
	if eqFoldExt(ext, "sql") || eqFoldExt(ext, "lua") || eqFoldExt(ext, "hs") || eqFoldExt(ext, "elm") || eqFoldExt(ext, "ada") {
		return "dash", true
	}
	if eqFoldExt(ext, "lisp") || eqFoldExt(ext, "clj") || eqFoldExt(ext, "cljs") || eqFoldExt(ext, "el") || eqFoldExt(ext, "scm") || eqFoldExt(ext, "asm") {
		return "semi", true
	}
	if eqFoldExt(ext, "tex") || eqFoldExt(ext, "erl") || eqFoldExt(ext, "hrl") || eqFoldExt(ext, "m") {
		return "percent", true
	}
	return "", false
}

func eqFoldExt(s string, lower string) bool {
	if len(s) != len(lower) {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if b != lower[i] {
			return false
		}
	}
	return true
}

func readFileBytes(c *eval.Context, path string) eval.Result {
	completion := c.TakeCompletion()
	if completion.RequestID != 0 {
		payload := host.FilePayload(c.Run, host.OpRead, path)
		payload.Free(c.Run)
		if completion.Diagnostic.Code != "" {
			result := eval.Result{Diagnostic: completion.Diagnostic.Clone(c.Run)}
			completion.Diagnostic.Free(c.Run)
			completion.Value.Free(c.Run)
			return result
		}
		if !completion.HasValue {
			completion.Value.Free(c.Run)
			return c.InvalidOperation("template file read completion omitted its result")
		}
		result := eval.Result{Value: completion.Value.Clone(c.Run)}
		completion.Value.Free(c.Run)
		return result
	}
	payload := host.FilePayload(c.Run, host.OpRead, path)
	id := c.Submit(host.RequestReadFile, payload)
	payload.Free(c.Run)
	if id == 0 {
		return failure("HOST_FAIL", "host request was not accepted")
	}
	return eval.Result{Waiting: true}
}
