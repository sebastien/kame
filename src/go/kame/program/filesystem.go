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
		completion.Value, completion.HasValue = core.Value{Kind: core.Bool, Bool: p.Host.Stat(filename).Exists}, true
		return completion
	}
	if op == host.OpStat {
		result := p.Host.Stat(filename)
		if !result.Exists {
			completion.Diagnostic = failure(p.Alloc, "FS_ERR", "cannot stat file")
			return completion
		}
		fields := []core.RecordField{{Key: "name", Value: core.NewString(p.Alloc, name)}, {Key: "size", Value: core.Value{Kind: core.Int, Int: result.Info.Size}}, {Key: "mode", Value: core.Value{Kind: core.Int, Int: int64(result.Info.Mode)}}, {Key: "dir", Value: core.Value{Kind: core.Bool, Bool: result.Info.IsDir}}}
		completion.Value, completion.HasValue = core.NewRecord(p.Alloc, fields), true
		for i := range fields {
			fields[i].Value.Free(p.Alloc)
		}
		return completion
	}
	if op == host.OpWildcard {
		completion.Value, completion.HasValue = p.wildcard(name), true
		return completion
	}
	completion.Diagnostic = failure(p.Alloc, "HOST_FAIL", "unknown filesystem request")
	return completion
}

func (p *Program) wildcard(pattern string) core.Value {
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
	p.collectGlob(pattern, start, root, &names)
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
	return result
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
		if pattern[i] == '*' || pattern[i] == '?' || pattern[i] == '[' {
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

func (p *Program) collectGlob(pattern string, pos int, directory string, names *[]string) {
	if pos >= len(pattern) {
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
			p.collectDescendants(directory, names)
			return
		}
		p.collectGlob(pattern, next, directory, names)
		entries, err := p.Host.ReadDir(p.Alloc, directory)
		if err != nil {
			return
		}
		for i := range entries {
			name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
			mem.FreeString(p.Alloc, entries[i].Name)
			if entries[i].IsDir {
				p.collectGlob(pattern, pos, name, names)
			}
			mem.FreeString(p.Alloc, name)
		}
		slices.Free(p.Alloc, entries)
		return
	}
	meta := false
	for i := range segment {
		if segment[i] == '*' || segment[i] == '?' || segment[i] == '[' {
			meta = true
			break
		}
	}
	if !meta {
		name := joinFilesystemPath(p.Alloc, directory, segment)
		info := p.Host.Stat(name)
		if !info.Exists {
			mem.FreeString(p.Alloc, name)
			return
		}
		if end == len(pattern) {
			*names = slices.Append(p.Alloc, *names, cloneText(p.Alloc, name))
		} else if info.Info.IsDir {
			p.collectGlob(pattern, next, name, names)
		}
		mem.FreeString(p.Alloc, name)
		return
	}
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
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
				p.collectGlob(pattern, next, name, names)
			}
		}
		mem.FreeString(p.Alloc, name)
	}
	slices.Free(p.Alloc, entries)
}

func (p *Program) collectDescendants(directory string, names *[]string) {
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		return
	}
	defer slices.Free(p.Alloc, entries)
	for i := range entries {
		name := joinFilesystemPath(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		*names = slices.Append(p.Alloc, *names, name)
		if entries[i].IsDir {
			p.collectDescendants(name, names)
		}
	}
}
