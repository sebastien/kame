// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/diagnostic"
	"kame/lang/script"
	"kame/lang/source"
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
	Parent int
}

type sourcePart struct { Name string; Text string; Offset int }

type buildSource struct {
	Files     []sourceFile
	Parts     []sourcePart
	Missing   bool
	Status    int
	JSON bool
}

func freeBuildSource(source *buildSource) {
	if source == nil {
		return
	}
	for i := range source.Files {
		mem.FreeSlice(mem.System, source.Files[i].Data)
		if source.Files[i].OwnedName { mem.FreeString(mem.System, source.Files[i].Name) }
	}
	slices.Free(mem.System, source.Files)
	slices.Free(mem.System, source.Parts)
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	if options.Command != "" {
		result := buildSource{JSON: options.JSON}
		result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: "<command>", Text: options.Command, Parent: -1})
		if !expandIncludesLanguage(&result, 0, errOut, "kmk") { result.Status = 1 }
		return result
	}
	if options.File != "" {
		name := options.File
		if path.IsAbs(name) { return readBuildSource(name, errOut) }
		name = path.Join(mem.System, options.Directory, name)
		result := readBuildSource(name, errOut)
		mem.FreeString(mem.System, name)
		return result
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
	return readBuildSourceLanguage(name, errOut, "kmk")
}

func readBuildSourceLanguage(name string, errOut io.Writer, lang string) buildSource {
	return readRunSource(name, errOut, lang, false)
}

func sourceError(out io.Writer, code string, message string, json bool) {
	if !json { cliError(out, code, message); return }
	emitDiagnostic(out, diagnostic.Diagnostic{Code: code, Message: message, Severity: diagnostic.Error}, true, nil)
}

func readRunSource(name string, errOut io.Writer, lang string, json bool) buildSource {
	canonical := path.Clean(mem.System, name)
	data, readErr := os.ReadFile(mem.System, canonical)
	if readErr != nil {
		sourceError(errOut, "FS_ERR", "cannot read source: "+canonical, json)
		mem.FreeString(mem.System, canonical)
		return buildSource{Status: 1}
	}
	result := buildSource{JSON: json}
	// Text wraps Data backing (zero-copy string conversion in Solod):
	// freeBuildSource frees Data only, Text never outlives it.
	result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: canonical, Text: string(data), Data: data, OwnedName: true, Parent: -1})
	if lang == "template" { result.Parts = slices.Append(mem.System, result.Parts, sourcePart{Name: canonical, Text: string(data)}) } else if !expandIncludesLanguage(&result, 0, errOut, lang) { result.Status = 1 }
	return result
}

func (s *buildSource) compileSources() []program.CompileSource {
	result := slices.Make[program.CompileSource](mem.System, len(s.Parts))
	for i := range s.Parts { result[i] = program.CompileSource{Name: s.Parts[i].Name, Text: s.Parts[i].Text, Offset: s.Parts[i].Offset} }
	return result
}

func expandIncludesLanguage(s *buildSource, fileIndex int, errOut io.Writer, lang string) bool {
	file := s.Files[fileIndex]
	authored := source.New(mem.System, file.Name, file.Text)
	parsed := script.ParseFragment(mem.System, authored, lang, 0, len(file.Text))
	defer authored.Free(mem.System)
	start := 0
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Include { continue }
		if file.Name == "<command>" { sourceError(errOut, "FEATURE_UNSUP", "include requires a file-backed build source", s.JSON); parsed.Free(); return false }
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
		for j := fileIndex; j >= 0; j = s.Files[j].Parent {
			if s.Files[j].Name == includeName { sourceError(errOut, "DEP_CYCLE", "include cycle: "+includeName, s.JSON); mem.FreeString(mem.System, includeName); parsed.Free(); return false }
		}
		data, readErr := os.ReadFile(mem.System, includeName)
		if readErr != nil { sourceError(errOut, "FS_ERR", "cannot read included source: "+includeName, s.JSON); mem.FreeString(mem.System, includeName); parsed.Free(); return false }
		s.Files = slices.Append(mem.System, s.Files, sourceFile{Name: includeName, Text: string(data), Data: data, OwnedName: true, Parent: fileIndex})
		if !expandIncludesLanguage(s, len(s.Files)-1, errOut, lang) { parsed.Free(); return false }
		start = item.Span.End
	}
	s.Parts = slices.Append(mem.System, s.Parts, sourcePart{Name: file.Name, Text: file.Text[start:], Offset: start})
	parsed.Free()
	return true
}
