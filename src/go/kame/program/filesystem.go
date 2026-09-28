package program

import (
	"kame/core"
	"kame/host"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
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
	p.collectPaths(root, &names)
	var values []core.Value
	for i := range names {
		matched, err := globMatches(pattern, names[i])
		if err == nil && matched {
			relative := p.relativePath(names[i])
			values = slices.Append(p.Alloc, values, core.NewString(p.Alloc, relative))
			mem.FreeString(p.Alloc, relative)
		}
		mem.FreeString(p.Alloc, names[i])
	}
	slices.Free(p.Alloc, names)
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j].Text < values[j-1].Text; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	result := core.NewList(p.Alloc, values)
	for i := range values {
		values[i].Free(p.Alloc)
	}
	slices.Free(p.Alloc, values)
	return result
}

func (p *Program) relativePath(name string) string {
	cwd := p.Options.Directory
	if cwd == "." && !path.IsAbs(name) {
		return cloneText(p.Alloc, "./"+name)
	}
	if cwd != "" && len(name) > len(cwd) && name[:len(cwd)] == cwd && name[len(cwd)] == '/' {
		return cloneText(p.Alloc, "./"+name[len(cwd)+1:])
	}
	return cloneText(p.Alloc, name)
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

func (p *Program) collectPaths(directory string, names *[]string) {
	entries, err := p.Host.ReadDir(p.Alloc, directory)
	if err != nil {
		return
	}
	defer slices.Free(p.Alloc, entries)
	for i := range entries {
		name := path.Join(p.Alloc, directory, entries[i].Name)
		mem.FreeString(p.Alloc, entries[i].Name)
		*names = slices.Append(p.Alloc, *names, name)
		if entries[i].IsDir {
			p.collectPaths(name, names)
		}
	}
}

func globMatches(pattern string, name string) (bool, error) {
	return matchSegments(pattern, 0, name, 0)
}

func matchSegments(pattern string, pi int, name string, ni int) (bool, error) {
	if pi == len(pattern) {
		return ni == len(name), nil
	}
	pend := pi
	for pend < len(pattern) && pattern[pend] != '/' {
		pend++
	}
	nend := ni
	for nend < len(name) && name[nend] != '/' {
		nend++
	}
	segment := pattern[pi:pend]
	if segment == "**" {
		if ok, err := matchSegments(pattern, nextSegment(pattern, pend), name, ni); ok || err != nil {
			return ok, err
		}
		for cursor := ni; cursor < len(name); cursor++ {
			if name[cursor] == '/' {
				if ok, err := matchSegments(pattern, nextSegment(pattern, pend), name, cursor+1); ok || err != nil {
					return ok, err
				}
			}
		}
		return false, nil
	}
	if ni == len(name) {
		return false, nil
	}
	ok, err := path.Match(segment, name[ni:nend])
	if err != nil || !ok {
		return false, err
	}
	if pend == len(pattern) || nend == len(name) {
		return pend == len(pattern) && nend == len(name), nil
	}
	return matchSegments(pattern, pend+1, name, nend+1)
}

func nextSegment(value string, end int) int {
	if end < len(value) {
		return end + 1
	}
	return end
}
