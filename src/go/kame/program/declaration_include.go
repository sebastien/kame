package program

import (
	"kame/core"
	"kame/diagnostic"
	"kame/host"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/script"
	"path"
	"slices"
	"strings"
)

func (p *Program) registerDeclarationInclude(item script.ScriptItem, scope *declarationScope) diagnostic.Diagnostic {
	var author eval.SourcePart
	if item.SourceOrigin >= 1 && item.SourceOrigin <= len(p.Eval.SourceParts) {
		author = p.Eval.SourceParts[item.SourceOrigin-1]
	}
	location := p.Eval.LocateSource(p.Parsed.Source.Name, item.Span)
	if author.Included != nil || author.Module {
		location.Source, location.Span = author.Name, item.Span
	}
	span := diagnostic.Span{Start: location.Span.Start, End: location.Span.End}
	locateError := func(d *diagnostic.Diagnostic) {
		d.Span = span
		d.Source = cloneText(p.Alloc, location.Source)
		appendIncludeFrames(p.Alloc, d, author.Includes)
	}
	includeError := func(code, message string) diagnostic.Diagnostic {
		d := failure(p.Alloc, code, message)
		locateError(&d)
		return d
	}
	name := item.Include
	readDenied := ""
	if item.BatchInclude {
		return includeError("FEATURE_UNSUP", "batch includes require file source composition before declaration registration")
	}
	if item.Expression != nil {
		r := p.registrationValue(item.Expression, scope.Scope)
		if r.Diagnostic.Code != "" {
			return r.Diagnostic
		}
		defer r.Free(p.Alloc)
		name = r.Value.Text
		if !rootedIncludePath(r.Value) {
			return includeError("EXPR_INVALID", "computed include requires a rooted local Path")
		}
		readDenied = "computed include requires read access; grant the required path with --allow-read=ROOT"
	}
	name, d := p.resolveDeclarationIncludePath(name, author.Directory, readDenied)
	if d.Code != "" {
		locateError(&d)
		return d
	}
	defer mem.FreeString(p.Alloc, name)
	if script.IsKashSource(author.Name) && !script.IsKashSource(name) {
		return includeError("RES_INVALID", "Kash includes require a .kash or .ksh source")
	}
	previous := p.declarationIncludeStack
	defer func() { p.declarationIncludeStack = previous }()
	if len(previous) == 0 {
		for _, frame := range author.Includes {
			ancestor := frame.Source
			if !path.IsAbs(ancestor) {
				ancestor = path.Join(p.Options.Directory, ancestor)
			}
			p.declarationIncludeStack = append(p.declarationIncludeStack, ancestor)
		}
		p.declarationIncludeStack = append(p.declarationIncludeStack, path.Join(author.Directory, path.Base(author.Name)))
	}
	if i := slices.Index(p.declarationIncludeStack, name); i >= 0 {
		return includeError("DEP_CYCLE", "include cycle: "+strings.Join(p.declarationIncludeStack[i:], " -> ")+" -> "+name)
	}
	p.declarationIncludeStack = append(p.declarationIncludeStack, name)
	payload := core.NewString(p.Alloc, name)
	result := p.declarationSource(host.Request{Kind: host.RequestLoadSource, Payload: payload})
	payload.Free(p.Alloc)
	defer result.Value.Free(p.Alloc)
	defer result.Diagnostic.Free(p.Alloc)
	if result.Diagnostic.Code != "" {
		return includeError(result.Diagnostic.Code, result.Diagnostic.Message+": "+name)
	}
	if !result.HasValue {
		p.registrationPending = true
		return diagnostic.Diagnostic{}
	}
	if result.Value.Kind == core.Nil {
		if item.OptionalInclude {
			return diagnostic.Diagnostic{}
		}
		return includeError("FS_ERR", "cannot read included source: "+name)
	}
	if result.Value.Kind != core.Bytes {
		return includeError("HOST_FAIL", "source host did not return bytes")
	}
	text := string(result.Value.Bytes)
	var parsed *script.Script
	if script.IsKashSource(name) {
		parsed = script.ParseKash(p.Alloc, name, text)
		script.ValidateKashModule(parsed)
	} else {
		parsed = script.ParseKameFile(p.Alloc, name, text)
	}
	parsed.ValidateAssignments()
	p.declarationIncludes = append(p.declarationIncludes, parsed)
	frames := append(slices.Clone(author.Includes), diagnostic.Frame{Kind: "include", Label: name, Source: location.Source, Span: span})
	if len(parsed.Diagnostics) != 0 {
		first := parsed.Diagnostics[0]
		d := failureAt(p.Alloc, first.Code, diagnostic.Span{Start: first.Span.Start, End: first.Span.End}, first.Message)
		d.Source = cloneText(p.Alloc, name)
		appendIncludeFrames(p.Alloc, &d, frames)
		return d
	}
	signature := core.ContentSignature(result.Value.Bytes)
	origin := p.Eval.AddDeclarationIncludeSource(parsed, signature, frames)
	remaining := parsed.Items
	for {
		end := len(remaining)
		if at := slices.IndexFunc(remaining, func(item script.ScriptItem) bool { return item.Kind == script.Include }); at >= 0 {
			end = at
		}
		child, d := p.Eval.BindDeclarationInclude(scope.Scope, remaining[:end], origin, scope.Included)
		if d.Code != "" {
			return d
		}
		next := p.includeScopeSignature(*scope, signature, name)
		scope.Scope.Free()
		scope.Scope, scope.Signature = child, next
		if d := p.registerDeclarationItems(remaining[:end], scope); d.Code != "" || p.registrationPending || end == len(remaining) {
			return d
		}
		if d := p.registerDeclarationInclude(remaining[end], scope); d.Code != "" || p.registrationPending {
			return d
		}
		remaining = remaining[end+1:]
	}
}

func rootedIncludePath(value core.Value) bool {
	name := value.Text
	return value.Kind == core.Path && !strings.ContainsRune(name, 0) &&
		(path.IsAbs(name) || strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "~/"))
}

// Resolve against the author, then check the canonical path for computed
// operands. Literal sources use the host's source-loading policy.
func (p *Program) resolveDeclarationIncludePath(name string, directory string, readDenied string) (string, diagnostic.Diagnostic) {
	if suffix, homeRelative := strings.CutPrefix(name, "~/"); homeRelative {
		home, _ := p.configuredEnvironment("HOME")
		if !path.IsAbs(home) {
			return "", failure(p.Alloc, "FS_ERR", "cannot resolve home directory for include")
		}
		name = path.Join(home, suffix)
	} else if path.IsAbs(name) {
		name = path.Clean(name)
	} else {
		if directory == "" {
			return "", failure(p.Alloc, "RES_INVALID", "relative include requires a file-backed source")
		}
		name = path.Join(directory, name)
	}
	if readDenied != "" {
		c := eval.Context{Run: p.Alloc, Cwd: p.Options.Directory, Grants: p.Options.Grants}
		if !c.Allows(eval.Read, name) {
			mem.FreeString(p.Alloc, name)
			return "", failure(p.Alloc, "CAP_DENIED", readDenied)
		}
	}
	return name, diagnostic.Diagnostic{}
}

func (p *Program) includeScopeSignature(scope declarationScope, signature core.Signature, name string) core.Signature {
	if scope.Scope == p.Eval.Scope || scope.Signature.Mode == core.SignatureContent {
		digest := core.NewDigest()
		digest.Text("declaration-include-v1")
		digest.Text(name)
		digest.Write(scope.Signature.Digest[:])
		digest.Write(signature.Digest[:])
		digest.Sum(signature.Digest[:])
	} else {
		signature = core.Signature{}
	}
	return signature
}
