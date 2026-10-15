// Command kame provides the native Kame command-line interface.
package main

import (
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/script"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
)

// languageSourcePath preserves authored spelling and returns an exactly sized
// owned string. The OS resolves dot segments; source labels need no cleaning.
func languageSourcePath(directory string, name string) string {
	if path.IsAbs(name) || directory == "." {
		return cloneCommandText(name)
	}
	return cloneCommandText(directory + "/" + name)
}

type sourceFile struct {
	Name       string
	Text       string
	Data       []byte
	OwnedName  bool
	FileBacked bool
	Parent     int
}

type buildSource struct {
	Files   []sourceFile
	Parts   []program.CompileSource
	Missing bool
	Status  int
	JSON    bool
	// Borrowed configuration used only while expanding includes/branches.
	SelectionPrefix      string
	SelectionDefines     []string
	SelectionEnvironment []string
}

func freeBuildSource(source *buildSource) {
	if source == nil {
		return
	}
	for i := range source.Files {
		mem.FreeSlice(mem.System, source.Files[i].Data)
		if source.Files[i].OwnedName {
			mem.FreeString(mem.System, source.Files[i].Name)
		}
	}
	slices.Free(mem.System, source.Files)
	slices.Free(mem.System, source.Parts)
	posix.FreeEnvironment(mem.System, source.SelectionEnvironment)
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	options.JSON = options.JSON || cliDiagnosticJSON
	return loadBuildSourceWithPrefix(options, diagnosticWriter(cliDiagnosticOut, errOut, options.JSON), reportMissing, "")
}

func loadBuildSourceWithPrefix(options buildArguments, errOut io.Writer, reportMissing bool, prefix string) buildSource {
	defines := selectionOverrides(options.Defines, options.Parameters)
	defer slices.Free(mem.System, defines)
	if options.Command != "" {
		return inlineRunSource("<command>", options.Command, "kmk", options.JSON, defines, options.Environment, errOut, prefix)
	}
	if options.File != "" {
		name := options.File
		if path.IsAbs(name) {
			return readRunSourceConfigured(name, errOut, "kmk", options.JSON, defines, options.Environment, prefix)
		}
		name = path.Join(mem.System, options.Directory, name)
		result := readRunSourceConfigured(name, errOut, "kmk", options.JSON, defines, options.Environment, prefix)
		mem.FreeString(mem.System, name)
		return result
	}
	candidates := []string{"Makefile.kmk", "make.kmk", "src/kmk/main.kmk"}
	for i := range candidates {
		candidate := path.Join(mem.System, options.Directory, candidates[i])
		_, statErr := os.Stat(candidate)
		if statErr == nil {
			result := readRunSourceConfigured(candidate, errOut, "kmk", options.JSON, defines, options.Environment, prefix)
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
			if !posix.EnvironmentNameEqual(current[j][:nameEnd], entry[:nameEnd]) {
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

func sourceError(out io.Writer, code string, message string, json bool) {
	if !json {
		cliError(out, code, message)
		return
	}
	emitDiagnostic(out, diagnostic.Diagnostic{Code: code, Message: message, Severity: diagnostic.Error}, true, nil)
}

func readRunSourceConfigured(name string, errOut io.Writer, lang string, json bool, defines []string, environment []string, prefix string) buildSource {
	canonical := path.Clean(mem.System, name)
	data, readErr := os.ReadFile(mem.System, canonical)
	if readErr != nil {
		sourceError(errOut, "FS_ERR", "cannot read source: "+canonical, json)
		mem.FreeString(mem.System, canonical)
		return buildSource{Status: 1}
	}
	result := buildSource{JSON: json, SelectionPrefix: prefix, SelectionDefines: defines, SelectionEnvironment: mergeEnvironment(posix.Environment(mem.System), environment)}
	// Text wraps Data backing (zero-copy string conversion in Solod):
	// freeBuildSource frees Data only, Text never outlives it.
	result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: canonical, Text: string(data), Data: data, OwnedName: true, FileBacked: true, Parent: -1})
	if lang == "template" {
		result.Parts = slices.Append(mem.System, result.Parts, program.CompileSource{Name: canonical, Text: string(data)})
	} else if !expandIncludesLanguage(&result, 0, errOut, lang) {
		result.Status = 1
	}
	result.SelectionPrefix = ""
	result.SelectionDefines = nil
	return result
}

func expandIncludesLanguage(s *buildSource, fileIndex int, errOut io.Writer, lang string) bool {
	file := s.Files[fileIndex]
	authored := source.New(mem.System, file.Name, file.Text)
	defer authored.Free(mem.System)
	parsed := script.ParseFragment(mem.System, authored, lang, 0, len(file.Text))
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		for i := range parsed.Diagnostics {
			d := parsed.Diagnostics[i]
			emitDiagnostic(errOut, diagnostic.Diagnostic{Source: file.Name, Code: d.Code, Severity: diagnostic.Error, Message: d.Message, Span: diagnostic.Span{Start: d.Span.Start, End: d.Span.End}}, s.JSON, authored)
		}
		return false
	}
	start := 0
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Include && item.Kind != script.Composition {
			continue
		}
		s.Parts = slices.Append(mem.System, s.Parts, program.CompileSource{Name: file.Name, Text: file.Text[start:item.Span.Start], Offset: start})
		start = item.Span.End
		if item.Kind == script.Include {
			if !s.expandSourceInclude(fileIndex, item.Include, item.OptionalInclude, item.Span, errOut, authored, lang) {
				return false
			}
		} else if !s.expandSourceComposition(fileIndex, item.Expression, errOut, authored, lang) {
			return false
		}
	}
	s.Parts = slices.Append(mem.System, s.Parts, program.CompileSource{Name: file.Name, Text: file.Text[start:], Offset: start})
	return true
}
