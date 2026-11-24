package program

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"kame/core"
	"kame/host"
	"kame/internal/mem"
	"path"
	"slices"

	"strings"
)

func (p *Program) isSourcePath(name string) bool {
	if p.Eval.HasAuthoredSourceRequest(name) {
		return true
	}
	for i := range p.Eval.SourceParts {
		part := p.Eval.SourceParts[i]
		if part.Signature.Mode != core.SignatureContent || part.Directory == "" {
			continue
		}
		filename := path.Join(part.Directory, path.Base(part.Name))
		matches := filename == name
		mem.FreeString(p.Alloc, filename)
		if matches {
			return true
		}
	}
	return false
}

func (p *Program) fileCompletion(request host.Request, op string, name string) core.Completion {
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	if op == host.OpWildcard {
		key := host.PayloadText(request.Payload, host.FieldDiscoveryKey)
		if _, _, command := host.CommandDiscovery(key); command {
			name = key
		}
		result := p.wildcard(name)
		completion.Value, completion.HasValue, completion.Diagnostic = result.Value, result.HasValue, result.Diagnostic
		return completion
	}
	filename := p.canonicalTarget(name, true)
	defer mem.FreeString(p.Alloc, filename)
	if op == host.OpOutputContent {
		info := p.Host.Lstat(filename)
		if info.Failed {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot inspect output")
			return completion
		}
		if info.Exists && info.Info.Symlink {
			link := p.Host.Readlink(p.Alloc, filename)
			if link.Failed {
				mem.FreeString(p.Alloc, link.Target)
				completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read symbolic link")
				return completion
			}
			value := core.NewString(p.Alloc, link.Target)
			mem.FreeString(p.Alloc, link.Target)
			completion.Value = core.NewRecord(p.Alloc, []core.RecordField{{Key: "link", Value: value}})
			value.Free(p.Alloc)
			completion.HasValue = true
			return completion
		}
	}
	if op == host.OpFileContent || op == host.OpToolContent || op == host.OpOutputContent {
		result := p.Host.Stat(filename)
		if result.Failed {
			// A link can be inspected even when following it fails (e.g. a loop).
			// Its content is unavailable, not a failed no-follow metadata view.
			if op == host.OpFileContent {
				link := p.Host.Lstat(filename)
				if link.Exists && link.Info.Symlink {
					completion.Value, completion.HasValue = core.Value{Kind: core.Bool}, true
					return completion
				}
			}
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file")
			return completion
		}
		completion.HasValue = true
		if !result.Exists {
			return completion
		}
		// A present non-regular or unreadable path has no reusable content
		// identity. Do not read devices/FIFOs, or turn read failure into absence.
		completion.Value = core.Value{Kind: core.Bool, Bool: result.Info.Regular}
		if !result.Info.Regular {
			return completion
		}
		signature := p.Host.FileSignature(filename)
		if signature.Mode != core.SignatureContent {
			return completion
		}
		var encoded [64]byte
		hex.Encode(encoded[:], signature.Digest[:])
		fields := []core.RecordField{{Key: "sha256", Value: core.NewString(p.Alloc, string(encoded[:]))}}
		completion.Value = core.NewRecord(p.Alloc, fields)
		fields[0].Value.Free(p.Alloc)
		return completion
	}
	if op == host.OpRead {
		data, err := p.Host.ReadFile(p.Alloc, filename)
		if request.Kind == host.RequestLoadSource && errors.Is(err, fs.ErrNotExist) {
			completion.Value, completion.HasValue = core.Value{Kind: core.Nil}, true
			return completion
		}
		if err != nil {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read file")
			return completion
		}
		completion.Value, completion.HasValue = core.Value{Kind: core.Bytes, Bytes: data}, true
		return completion
	}
	if op == host.OpExists {
		result := p.Host.Stat(filename)
		if result.Failed {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file")
			return completion
		}
		completion.Value, completion.HasValue = core.Value{Kind: core.Bool, Bool: result.Exists}, true
		return completion
	}
	if op == host.OpDirectoryCheck {
		info := p.Host.Stat(filename)
		completion.Value, completion.HasValue = core.Value{Kind: core.Bool, Bool: info.Exists && info.Info.IsDir}, true
		return completion
	}
	if op == host.OpReadlink {
		result := p.Host.Readlink(p.Alloc, filename)
		if result.Failed {
			mem.FreeString(p.Alloc, result.Target)
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read symbolic link")
			return completion
		}
		completion.Value, completion.HasValue = core.NewString(p.Alloc, result.Target), true
		mem.FreeString(p.Alloc, result.Target)
		return completion
	}
	if op == host.OpStat || op == host.OpLstat {
		var result host.StatResult
		if op == host.OpLstat {
			result = p.Host.Lstat(filename)
		} else {
			result = p.Host.Stat(filename)
		}
		if !result.Exists {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file")
			return completion
		}
		fields := []core.RecordField{{Key: "name", Value: core.NewString(p.Alloc, name)}, {Key: "size", Value: core.Value{Kind: core.Int, Int: result.Info.Size}}, {Key: "mode", Value: core.Value{Kind: core.Int, Int: int64(result.Info.Mode)}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: result.Info.IsDir}}, {Key: "link", Value: core.Value{Kind: core.Bool, Bool: result.Info.Symlink}}, {Key: "regular", Value: core.Value{Kind: core.Bool, Bool: result.Info.Regular}}}
		if op == host.OpStat {
			fields = fields[:4]
		}
		completion.Value, completion.HasValue = core.NewRecord(p.Alloc, fields), true
		for i := range fields {
			fields[i].Value.Free(p.Alloc)
		}
		return completion
	}
	completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unknown filesystem request")
	return completion
}

func (p *Program) wildcard(pattern string) core.Completion {
	key := pattern
	_, _, command := host.CommandDiscovery(key)
	pattern = p.canonicalTarget(host.DiscoveryScan(key), true)
	defer mem.FreeString(p.Alloc, pattern)
	root := globRoot(p.Alloc, pattern)
	defer mem.FreeString(p.Alloc, root)
	var names []string
	start := 0
	if len(root) > 0 && len(pattern) >= len(root) && pattern[:len(root)] == root {
		start = len(root)
		if start < len(pattern) && pattern[start] == '/' {
			start++
		}
	}
	failed := false
	p.collectGlob(pattern, start, root, &names, &failed, command)
	if failed {
		for i := range names {
			mem.FreeString(p.Alloc, names[i])
		}
		mem.FreeSlice(nil, names)
		return core.Completion{Diagnostic: failure(p.Alloc, "FS_ERR", "cannot traverse wildcard path")}
	}
	for i, name := range names {
		names[i] = p.relativePath(name)
		mem.FreeString(p.Alloc, name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	values := make([]core.Value, len(names))
	for i, name := range names {
		values[i] = core.Value{Kind: core.String, Text: name}
	}
	result := core.Value{Kind: core.List, List: values}
	if command {
		// Visibility was enforced during the walk.
		return core.Completion{Value: result, HasValue: true}
	}
	value, d := host.FilterDiscovery(p.Alloc, key, result)
	return core.Completion{Value: value, HasValue: d.Code == "", Diagnostic: d}
}

func (p *Program) relativePath(name string) string {
	if core.IsResourceURIName(name) {
		return cloneText(p.Alloc, name)
	}
	cwd := p.Options.Directory
	if cwd == "." && !path.IsAbs(name) {
		return cloneText(p.Alloc, "./"+name)
	}
	if cwd != "" {
		prefix := strings.TrimSuffix(cwd, "/") + "/"
		if suffix, inside := strings.CutPrefix(name, prefix); inside && suffix != "" {
			return cloneText(p.Alloc, "./"+suffix)
		}
	}
	return cloneText(p.Alloc, name)
}

func joinFilesystemPath(a mem.Allocator, directory string, child string) string {
	if !core.IsResourceURIName(directory) {
		return path.Join(directory, child)
	}
	if strings.HasPrefix(directory, "file://") {
		// POSIX directory entries are decoded names. Join in path space, then
		// serialize once; memory-host entries already use canonical URI spelling.
		name := directory
		if name == "file://" {
			name = "file:///"
		}
		parsed := core.ParseResourceURI(a, name)
		if parsed.Error == "" {
			joined := path.Join(parsed.URI.Path, child)
			mem.FreeString(a, parsed.URI.Path)
			parsed.URI.Path = joined
			result := parsed.URI.Canonical(a)
			parsed.URI.Free()
			return result
		}
		parsed.URI.Free()
	}
	pathStart := -1
	for i := 0; i+2 < len(directory); i++ {
		if directory[i] == ':' && directory[i+1] == '/' && directory[i+2] == '/' {
			pathStart = i + 3
			break
		}
	}
	if pathStart < 0 {
		return path.Join(directory, child)
	}
	for pathStart < len(directory) && directory[pathStart] != '/' {
		pathStart++
	}
	if pathStart == len(directory) {
		b := strings.Builder{}
		b.WriteString(directory)
		b.WriteByte('/')
		b.WriteString(child)
		result := cloneText(a, b.String())
		b.Reset()
		return result
	}
	joined := path.Join(directory[pathStart:], child)
	defer mem.FreeString(a, joined)
	b := strings.Builder{}
	b.WriteString(directory[:pathStart])
	b.WriteString(joined)
	result := cloneText(a, b.String())
	b.Reset()
	return result
}

func globRoot(a mem.Allocator, pattern string) string {
	cut := -1
	for i := range pattern {
		if pattern[i] == '*' || pattern[i] == '?' || pattern[i] == '[' || pattern[i] == '\\' {
			cut = i
			break
		}
	}
	if cut < 0 {
		return cloneText(a, pattern)
	}
	for cut > 0 && pattern[cut-1] != '/' {
		cut--
	}
	if cut == 0 {
		return cloneText(a, ".")
	}
	if cut == 1 {
		return cloneText(a, "/")
	}
	return cloneText(a, pattern[:cut-1])
}

func (p *Program) collectGlob(pattern string, pos int, directory string, names *[]string, failed *bool, command bool) {
	if *failed {
		return
	}
	if pos >= len(pattern) {
		info := p.Host.Lstat(directory)
		if info.Failed {
			*failed = true
		} else if info.Exists {
			*names = append(*names, cloneText(p.Alloc, directory))
		}
		return
	}
	end := pos
	for end < len(pattern) && pattern[end] != '/' {
		end++
	}
	segment, next := pattern[pos:end], end
	if next < len(pattern) {
		next++
	}
	if segment == "**" {
		if end == len(pattern) {
			p.collectDescendants(directory, names, failed, command)
			return
		}
		p.collectGlob(pattern, next, directory, names, failed, command)
		entries, err := p.Host.ReadDir(p.Alloc, directory)
		if err != nil {
			*failed = *failed || p.globDirectoryFailed(directory)
			return
		}
		for i := range entries {
			entry := entries[i]
			if entry.IsDir && (!command || !strings.HasPrefix(entry.Name, ".")) {
				name := joinFilesystemPath(p.Alloc, directory, entry.Name)
				p.collectGlob(pattern, pos, name, names, failed, command)
				mem.FreeString(p.Alloc, name)
			}
			mem.FreeString(p.Alloc, entry.Name)
		}
		mem.FreeSlice(nil, entries)
		return
	}
	meta := false
	for i := range segment {
		if segment[i] == '*' || segment[i] == '?' || segment[i] == '[' || segment[i] == '\\' {
			meta = true
			break
		}
	}
	if !meta {
		name := joinFilesystemPath(p.Alloc, directory, segment)
		var info host.StatResult
		if end == len(pattern) {
			info = p.Host.Lstat(name)
		} else {
			info = p.Host.Stat(name)
		}
		if !info.Exists {
			if info.Failed {
				*failed = true
			}
			mem.FreeString(p.Alloc, name)
			return
		}
		if end == len(pattern) {
			*names = append(*names, cloneText(p.Alloc, name))
		} else if info.Info.IsDir {
			p.collectGlob(pattern, next, name, names, failed, command)
		}
		mem.FreeString(p.Alloc, name)
		return
	}
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		*failed = *failed || p.globDirectoryFailed(directory)
		return
	}
	dot := !command || host.GlobExplicitDot(segment)
	for i := range entries {
		if !dot && strings.HasPrefix(entries[i].Name, ".") {
			mem.FreeString(p.Alloc, entries[i].Name)
			continue
		}
		name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		matched, matchErr := path.Match(segment, path.Base(name))
		if matchErr == nil && matched {
			if end == len(pattern) {
				*names = append(*names, cloneText(p.Alloc, name))
			} else if entries[i].IsDir {
				p.collectGlob(pattern, next, name, names, failed, command)
			}
		}
		mem.FreeString(p.Alloc, name)
	}
	mem.FreeSlice(nil, entries)
}

func (p *Program) globDirectoryFailed(directory string) bool {
	info := p.Host.Stat(directory)
	return info.Failed || (info.Exists && info.Info.IsDir)
}

func (p *Program) collectDescendants(directory string, names *[]string, failed *bool, command bool) {
	if *failed {
		return
	}
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		*failed = *failed || p.globDirectoryFailed(directory)
		return
	}
	defer mem.FreeSlice(nil, entries)
	for i := range entries {
		if command && strings.HasPrefix(entries[i].Name, ".") {
			mem.FreeString(p.Alloc, entries[i].Name)
			continue
		}
		name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		*names = append(*names, name)
		if entries[i].IsDir {
			p.collectDescendants(name, names, failed, command)
		}
	}
}
