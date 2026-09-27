// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/lang/script"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

type sourceFile struct {
	Name string
	Text string
	Data []byte
	OwnedName bool
}

type sourcePart struct { Name string; Text string; Offset int }

type buildSource struct {
	Files     []sourceFile
	Parts     []sourcePart
	Missing   bool
	Status    int
}

func freeBuildSource(source *buildSource) {
	if source == nil {
		return
	}
	for i := range source.Files {
		if len(source.Files[i].Data) != 0 { mem.FreeSlice(mem.System, source.Files[i].Data) }
		if source.Files[i].OwnedName && source.Files[i].Name != "" { mem.FreeString(mem.System, source.Files[i].Name) }
	}
	if len(source.Files) != 0 { slices.Free(mem.System, source.Files) }
	if len(source.Parts) != 0 { slices.Free(mem.System, source.Parts) }
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	if options.Command != "" {
		result := buildSource{}
		result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: "<command>", Text: options.Command})
		result.Parts = slices.Append(mem.System, result.Parts, sourcePart{Name: "<command>", Text: options.Command})
		return result
	}
	if options.File != "" {
		return readBuildSource(options.File, errOut)
	}
	candidates := []string{"Makefile.kmk", "make.kmk", "src/kmk/main.kmk"}
	for i := range candidates {
		candidate := path.Join(mem.System, options.Directory, candidates[i])
		_, statErr := os.Stat(candidate)
		if statErr == nil {
			result := readBuildSource(candidate, errOut)
			mem.FreeString(mem.System, candidate)
			return result
		}
		mem.FreeString(mem.System, candidate)
	}
	if reportMissing {
		cliError(errOut, "BUILD_NO_SOURCE", "no build source found (tried Makefile.kmk, make.kmk, src/kmk/main.kmk)")
	}
	return buildSource{Missing: true, Status: 1}
}

func cloneCommandText(text string) string {
	if text == "" {
		return ""
	}
	data := mem.AllocSlice[byte](mem.System, len(text), len(text))
	copy(data, []byte(text))
	return string(data)
}

// mergeEnvironment gives explicit CLI entries precedence without relying on
// duplicate-variable behavior in execve implementations.
func mergeEnvironment(current []string, overrides []string) []string {
	for i := range overrides {
		entry := overrides[i]
		nameEnd := 0
		for nameEnd < len(entry) && entry[nameEnd] != '=' {
			nameEnd++
		}
		replaced := false
		for j := range current {
			if len(current[j]) <= nameEnd || current[j][nameEnd] != '=' {
				continue
			}
			if current[j][:nameEnd] != entry[:nameEnd] {
				continue
			}
			mem.FreeString(mem.System, current[j])
			current[j], replaced = cloneCommandText(entry), true
			break
		}
		if !replaced {
			current = slices.Append(mem.System, current, cloneCommandText(entry))
		}
	}
	return current
}

func readBuildSource(name string, errOut io.Writer) buildSource {
	canonical := path.Clean(mem.System, name)
	data, readErr := os.ReadFile(mem.System, canonical)
	if readErr != nil {
		cliError(errOut, "FS_ERR", "cannot read source: "+canonical)
		mem.FreeString(mem.System, canonical)
		return buildSource{Status: 1}
	}
	result := buildSource{}
	result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: canonical, Text: string(data), Data: data, OwnedName: true})
	if !expandIncludes(&result, 0, errOut) { result.Status = 1 }
	return result
}

func (s *buildSource) compileSources() []program.CompileSource {
	result := slices.Make[program.CompileSource](mem.System, len(s.Parts))
	for i := range s.Parts { result[i] = program.CompileSource{Name: s.Parts[i].Name, Text: s.Parts[i].Text, Offset: s.Parts[i].Offset} }
	return result
}

func expandIncludes(s *buildSource, fileIndex int, errOut io.Writer) bool {
	file := s.Files[fileIndex]
	parsed := script.Parse(mem.System, file.Name, file.Text)
	start := 0
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Include { continue }
		if file.Name == "<command>" { cliError(errOut, "FEATURE_UNSUP", "include requires a file-backed build source"); parsed.Free(); return false }
		s.Parts = slices.Append(mem.System, s.Parts, sourcePart{Name: file.Name, Text: file.Text[start:item.Span.Start], Offset: start})
		includeName := cloneCommandText(item.Include)
		if !path.IsAbs(includeName) {
			parent := path.Dir(mem.System, file.Name)
			resolved := path.Join(mem.System, parent, includeName)
			mem.FreeString(mem.System, parent)
			mem.FreeString(mem.System, includeName)
			includeName = resolved
		}
		canonical := path.Clean(mem.System, includeName)
		mem.FreeString(mem.System, includeName)
		includeName = canonical
		for j := range s.Files {
			if s.Files[j].Name == includeName { cliError(errOut, "DEP_CYCLE", "include cycle: "+includeName); mem.FreeString(mem.System, includeName); parsed.Free(); return false }
		}
		data, readErr := os.ReadFile(mem.System, includeName)
		if readErr != nil { cliError(errOut, "FS_ERR", "cannot read included source: "+includeName); mem.FreeString(mem.System, includeName); parsed.Free(); return false }
		s.Files = slices.Append(mem.System, s.Files, sourceFile{Name: includeName, Text: string(data), Data: data, OwnedName: true})
		if !expandIncludes(s, len(s.Files)-1, errOut) { parsed.Free(); return false }
		start = item.Span.End
	}
	s.Parts = slices.Append(mem.System, s.Parts, sourcePart{Name: file.Name, Text: file.Text[start:], Offset: start})
	parsed.Free()
	return true
}
