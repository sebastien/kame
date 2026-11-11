package main

import (
	"io"
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/script"
	"kame/lang/source"
	"kame/program"
	"os"
	"path"
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
	Name            string
	Text            string
	SourceDirectory string
	Data            []byte
	OwnedName       bool
	FileBacked      bool
	Parent          int
	Includes        []diagnostic.Frame
}

type buildSource struct {
	Files   []sourceFile
	Parts   []program.CompileSource
	Missing bool
	Status  int
	JSON    bool
	// Borrowed configuration used only while expanding includes/branches.
	SelectionPrefix      []program.CompileSource
	SelectionDefines     []string
	SelectionEnvironment []string
	SelectionDirectory   string
}

func freeBuildSource(source *buildSource) {
	if source == nil {
		return
	}
	for i := range source.Files {
		mem.FreeString(mem.System, source.Files[i].SourceDirectory)
		mem.FreeSlice(nil, source.Files[i].Includes)
		mem.FreeSlice(mem.System, source.Files[i].Data)
		if source.Files[i].OwnedName {
			mem.FreeString(mem.System, source.Files[i].Name)
		}
	}
	mem.FreeSlice(nil, source.Files)
	mem.FreeSlice(nil, source.Parts)
	posix.FreeEnvironment(mem.System, source.SelectionEnvironment)
	*source = buildSource{}
}

func loadBuildSource(options buildArguments, errOut io.Writer, reportMissing bool) buildSource {
	options.JSON = options.JSON || cliDiagnosticJSON
	return loadBuildSourceWithPrefix(options, diagnosticWriter(cliDiagnosticOut, errOut, options.JSON), reportMissing, nil)
}

func loadBuildSourceWithPrefix(options buildArguments, errOut io.Writer, reportMissing bool, prefix []program.CompileSource) buildSource {
	defines := selectionOverrides(options.Defines, options.Parameters)
	defer mem.FreeSlice(nil, defines)
	if options.Command != "" {
		return inlineRunSource("<command>", options.Command, "kmk", options.JSON, defines, options.Environment, errOut, prefix, options.Directory)
	}
	if options.File != "" {
		name := options.File
		if path.IsAbs(name) {
			return readRunSourceConfigured(name, errOut, "kmk", options.JSON, defines, options.Environment, prefix, options.Directory)
		}
		name = path.Join(options.Directory, name)
		result := readRunSourceConfigured(name, errOut, "kmk", options.JSON, defines, options.Environment, prefix, options.Directory)
		mem.FreeString(mem.System, name)
		return result
	}
	candidates := cli.BuildSourceCandidates()
	for i := range candidates {
		candidate := path.Join(options.Directory, candidates[i])
		_, statErr := os.Stat(candidate)
		if statErr == nil {
			result := readRunSourceConfigured(candidate, errOut, "kmk", options.JSON, defines, options.Environment, prefix, options.Directory)
			mem.FreeString(mem.System, candidate)
			return result
		}
		mem.FreeString(mem.System, candidate)
	}
	if reportMissing {
		list := cli.BuildSourceList()
		cliError(errOut, "BUILD_NO_SOURCE", "no build source found (tried "+list+")")
		mem.FreeString(mem.System, list)
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
			current = append(current, cloneCommandText(entry))
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

func readRunSourceConfigured(name string, errOut io.Writer, lang string, json bool, defines []string, environment []string, prefix []program.CompileSource, directory string) buildSource {
	canonical := path.Clean(name)
	data, readErr := os.ReadFile(canonical)
	if readErr != nil {
		sourceError(errOut, "FS_ERR", "cannot read source: "+canonical, json)
		mem.FreeString(mem.System, canonical)
		return buildSource{Status: 1}
	}
	result := buildSource{JSON: json, SelectionPrefix: prefix, SelectionDefines: defines, SelectionEnvironment: mergeEnvironment(posix.Environment(mem.System), environment), SelectionDirectory: directory}
	result.Files = append(result.Files, sourceFile{Name: canonical, Text: string(data), SourceDirectory: fileSourceDirectory(canonical), Data: data, OwnedName: true, FileBacked: true, Parent: -1})
	if lang == "template" {
		result.Parts = append(result.Parts, program.CompileSource{Name: canonical, Text: string(data), Lang: lang, SourceDirectory: result.Files[0].SourceDirectory})
	} else if !expandIncludesLanguage(&result, 0, errOut, lang) {
		result.Status = 1
	}
	result.SelectionPrefix = nil
	result.SelectionDefines = nil
	return result
}

func expandIncludesLanguage(s *buildSource, fileIndex int, errOut io.Writer, lang string) bool {
	file := s.Files[fileIndex]
	var signature core.Signature
	if file.FileBacked {
		signature = core.ContentSignature(file.Data)
	}
	rememberDiagnosticSource(file.Name, file.Text)
	authored := source.New(mem.System, file.Name, file.Text)
	defer authored.Free(mem.System)
	parsed := script.ParseFragment(mem.System, authored, lang, 0, len(file.Text))
	defer parsed.Free()
	if lang == "kash" && file.Parent >= 0 {
		script.ValidateKashLibrary(parsed)
	}
	if len(parsed.Diagnostics) != 0 {
		for i := range parsed.Diagnostics {
			d := parsed.Diagnostics[i]
			emitDiagnostic(errOut, diagnostic.Diagnostic{Source: file.Name, Code: d.Code, Severity: diagnostic.Error, Message: d.Message, Span: diagnostic.Span{Start: d.Span.Start, End: d.Span.End}, Frames: file.Includes}, s.JSON, authored)
		}
		return false
	}
	start := 0
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind != script.Include && item.Kind != script.Composition {
			continue
		}
		s.Parts = append(s.Parts, program.CompileSource{Name: file.Name, Text: file.Text[start:item.Span.Start], Lang: lang, Signature: signature, SourceDirectory: file.SourceDirectory, Offset: start, Includes: file.Includes})
		start = item.Span.End
		if item.Kind == script.Include {
			if !s.expandSourceInclude(fileIndex, item.Include, item.OptionalInclude, item.Span, errOut, authored, lang) {
				return false
			}
		} else if !s.expandSourceComposition(fileIndex, item.Expression, errOut, authored, lang) {
			return false
		}
	}
	s.Parts = append(s.Parts, program.CompileSource{Name: file.Name, Text: file.Text[start:], Lang: lang, Signature: signature, SourceDirectory: file.SourceDirectory, Offset: start, Includes: file.Includes})
	return true
}

func fileSourceDirectory(name string) string {
	if path.IsAbs(name) {
		return path.Dir(name)
	}
	buffer := mem.AllocSlice[byte](mem.System, 4096, 4096)
	defer mem.FreeSlice(mem.System, buffer)
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	absolute := path.Join(cwd, name)
	defer mem.FreeString(mem.System, absolute)
	return path.Dir(absolute)
}
