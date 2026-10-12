package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (p *Program) fileCompletion(request host.Request, op string, name string) core.Completion {
	completion := core.Completion{NodeID: request.NodeID, Generation: request.Generation, Attempt: request.Attempt, RequestID: request.ID}
	filename := p.canonicalTarget(name, true)
	defer mem.FreeString(p.Alloc, filename)
	if op == host.OpFileContent || op == host.OpToolContent || op == host.OpOutputContent {
		result := p.Host.Stat(filename)
		if result.Failed {
			// A link can be inspected even when following it fails (e.g. a loop).
			// Its content is unavailable, not a failed no-follow metadata view.
			if op == host.OpFileContent {
				link := p.Host.Lstat(filename)
				if link.Exists && link.Info.Symlink { completion.Value, completion.HasValue = core.Value{Kind: core.Bool}, true; return completion }
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
		// ponytail: the existing ReadFile contract buffers whole files; use
		// chunked host reads when large-resource memory becomes a measured limit.
		data, err := p.Host.ReadFile(p.Alloc, filename)
		if err != nil {
			return completion
		}
		completion.Value = core.NewBytes(p.Alloc, data)
		mem.FreeSlice(p.Alloc, data)
		return completion
	}
	if op == host.OpRead {
		data, err := p.Host.ReadFile(p.Alloc, filename)
		if err != nil {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read file")
			return completion
		}
		completion.Value, completion.HasValue = core.NewBytes(p.Alloc, data), true
		mem.FreeSlice(p.Alloc, data)
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
	if op == host.OpReadlink {
		result := p.Host.Readlink(p.Alloc, filename)
		if result.Failed { mem.FreeString(p.Alloc, result.Target); completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot read symbolic link"); return completion }
		completion.Value, completion.HasValue = core.NewString(p.Alloc, result.Target), true
		mem.FreeString(p.Alloc, result.Target)
		return completion
	}
	if op == host.OpStat || op == host.OpLstat {
		var result host.StatResult
		if op == host.OpLstat { result = p.Host.Lstat(filename) } else { result = p.Host.Stat(filename) }
		if !result.Exists {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file")
			return completion
		}
		fields := []core.RecordField{{Key: "name", Value: core.NewString(p.Alloc, name)}, {Key: "size", Value: core.Value{Kind: core.Int, Int: result.Info.Size}}, {Key: "mode", Value: core.Value{Kind: core.Int, Int: int64(result.Info.Mode)}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: result.Info.IsDir}}, {Key: "link", Value: core.Value{Kind: core.Bool, Bool: result.Info.Symlink}}, {Key: "regular", Value: core.Value{Kind: core.Bool, Bool: result.Info.Regular}}}
		if op == host.OpStat { fields = fields[:4] }
		completion.Value, completion.HasValue = core.NewRecord(p.Alloc, fields), true
		for i := range fields {
			fields[i].Value.Free(p.Alloc)
		}
		return completion
	}
	if op == host.OpWildcard {
		result := p.wildcard(name)
		completion.Value, completion.HasValue, completion.Diagnostic = result.Value, result.HasValue, result.Diagnostic
		return completion
	}
	completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unknown filesystem request")
	return completion
}

func (p *Program) wildcard(pattern string) core.Completion {
	pattern = p.canonicalTarget(pattern, true)
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
	p.collectGlob(pattern, start, root, &names, &failed)
	if failed {
		for i := range names { mem.FreeString(p.Alloc, names[i]) }
		slices.Free(p.Alloc, names)
		return core.Completion{Diagnostic: failure(p.Alloc, "FS_ERR", "cannot traverse wildcard path")}
	}
	var values []core.Value
	for i := range names {
		relative := p.relativePath(names[i])
		values = slices.Append(p.Alloc, values, core.NewString(p.Alloc, relative))
		mem.FreeString(p.Alloc, relative)
		mem.FreeString(p.Alloc, names[i])
	}
	slices.Free(p.Alloc, names)
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].Text < values[j-1].Text; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	unique := 0
	for i := range values {
		if unique > 0 && values[i].Text == values[unique-1].Text {
			values[i].Free(p.Alloc)
			continue
		}
		values[unique] = values[i]
		unique++
	}
	values = values[:unique]
	result := core.NewList(p.Alloc, values)
	for i := range values {
		values[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, values)
	return core.Completion{Value: result, HasValue: true}
}

func (p *Program) relativePath(name string) string {
	if core.IsResourceURIName(name) {
		return cloneText(p.Alloc, name)
	}
	cwd := p.Options.Directory
	if cwd == "." && !path.IsAbs(name) {
		return cloneText(p.Alloc, "./"+name)
	}
	if cwd != "" && len(name) > len(cwd) && name[:len(cwd)] == cwd && name[len(cwd)] == '/' {
		return cloneText(p.Alloc, "./"+name[len(cwd)+1:])
	}
	return cloneText(p.Alloc, name)
}

func joinFilesystemPath(a mem.Allocator, directory string, child string) string {
	if !core.IsResourceURIName(directory) {
		return path.Join(a, directory, child)
	}
	if strings.HasPrefix(directory, "file://") {
		// POSIX directory entries are decoded names. Join in path space, then
		// serialize once; memory-host entries already use canonical URI spelling.
		name := directory
		if name == "file://" { name = "file:///" }
		parsed := core.ParseResourceURI(a, name)
		if parsed.Error == "" {
			joined := path.Join(a, parsed.URI.Path, child)
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
		return path.Join(a, directory, child)
	}
	for pathStart < len(directory) && directory[pathStart] != '/' {
		pathStart++
	}
	if pathStart == len(directory) {
		b := strings.NewBuilder(a)
		b.WriteString(directory)
		b.WriteByte('/')
		b.WriteString(child)
		result := cloneText(a, b.String())
		b.Free()
		return result
	}
	joined := path.Join(a, directory[pathStart:], child)
	defer mem.FreeString(a, joined)
	b := strings.NewBuilder(a)
	b.WriteString(directory[:pathStart])
	b.WriteString(joined)
	result := cloneText(a, b.String())
	b.Free()
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

func (p *Program) collectGlob(pattern string, pos int, directory string, names *[]string, failed *bool) {
	if *failed { return }
	if pos >= len(pattern) {
		info := p.Host.Lstat(directory)
		if info.Failed { *failed = true } else if info.Exists { *names = slices.Append(p.Alloc, *names, cloneText(p.Alloc, directory)) }
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
			p.collectDescendants(directory, names, failed)
			return
		}
		p.collectGlob(pattern, next, directory, names, failed)
		entries, err := p.Host.ReadDir(p.Alloc, directory)
		if err != nil {
			*failed = *failed || p.globDirectoryFailed(directory)
			return
		}
		for i := range entries {
			name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
			mem.FreeString(p.Alloc, entries[i].Name)
			if entries[i].IsDir {
				p.collectGlob(pattern, pos, name, names, failed)
			}
			mem.FreeString(p.Alloc, name)
		}
		slices.Free(p.Alloc, entries)
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
		if end == len(pattern) { info = p.Host.Lstat(name) } else { info = p.Host.Stat(name) }
		if !info.Exists {
			if info.Failed { *failed = true }
			mem.FreeString(p.Alloc, name)
			return
		}
		if end == len(pattern) {
			*names = slices.Append(p.Alloc, *names, cloneText(p.Alloc, name))
		} else if info.Info.IsDir {
			p.collectGlob(pattern, next, name, names, failed)
		}
		mem.FreeString(p.Alloc, name)
		return
	}
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		*failed = *failed || p.globDirectoryFailed(directory)
		return
	}
	for i := range entries {
		name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		matched, matchErr := path.Match(segment, path.Base(name))
		if matchErr == nil && matched {
			if end == len(pattern) {
				*names = slices.Append(p.Alloc, *names, cloneText(p.Alloc, name))
			} else if entries[i].IsDir {
				p.collectGlob(pattern, next, name, names, failed)
			}
		}
		mem.FreeString(p.Alloc, name)
	}
	slices.Free(p.Alloc, entries)
}

func (p *Program) globDirectoryFailed(directory string) bool {
	info := p.Host.Stat(directory)
	return info.Failed || (info.Exists && info.Info.IsDir)
}

func (p *Program) collectDescendants(directory string, names *[]string, failed *bool) {
	if *failed { return }
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		*failed = *failed || p.globDirectoryFailed(directory)
		return
	}
	defer slices.Free(p.Alloc, entries)
	for i := range entries {
		name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		*names = slices.Append(p.Alloc, *names, name)
		if entries[i].IsDir {
			p.collectDescendants(name, names, failed)
		}
	}
}
