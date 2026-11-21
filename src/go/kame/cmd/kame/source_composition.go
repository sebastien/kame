// File-backed source composition runs before declaration registration.
package main

import (
	"io"
	"kame/cli"
	"kame/core"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/internal/mem"
	"kame/lang/eval"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/program"
	"os"
	"path"
	"slices"
	"strings"
)

func (s *buildSource) compositionCondition(condition *expr.Expr, out io.Writer, authored *source.Source, includes []diagnostic.Frame) program.PredicateResult {
	sources := slices.Clone(s.SelectionPrefix)
	for i := range s.Parts {
		sources = selectionDefinitions(sources, s.Parts[i], s.Parts[i].Lang)
	}
	defer mem.FreeSlice(nil, sources)
	directory := s.SelectionDirectory
	ownedDirectory := false
	buffer := mem.AllocSlice[byte](mem.System, 4096, 4096)
	defer mem.FreeSlice(mem.System, buffer)
	if !path.IsAbs(directory) {
		cwd, err := os.Getwd()
		if err != nil || cwd == "" {
			sourceError(out, "FS_ERR", "cannot resolve configuration evaluation directory", s.JSON)
			s.Status = 1
			return program.PredicateResult{Diagnostic: diagnostic.Diagnostic{Code: "FS_ERR"}}
		}
		directory = path.Join(cwd, directory)
		ownedDirectory = true
	}
	grants := effectiveGrants(s.SelectionGrants, []eval.Grant{{Capability: eval.Read, Names: []string{directory}}}, s.SelectionNoDefault)
	discovery := program.Options{Host: posix.New(mem.System), Grants: grants}
	result := program.TrackedCompositionCondition(mem.System, sources, program.CompileSource{Name: authored.Name, Text: authored.Text[condition.Span.Start:condition.Span.End], Offset: condition.Span.Start, Includes: includes}, s.SelectionDefines, s.SelectionEnvironment, directory, &discovery)
	s.Discoveries = append(s.Discoveries, result.Discoveries...)
	mem.FreeSlice(nil, result.Discoveries)
	result.Discoveries = nil
	if ownedDirectory {
		mem.FreeString(mem.System, directory)
	}
	if result.Diagnostic.Code != "" {
		emitDiagnostic(out, result.Diagnostic, s.JSON, authored)
		s.Status = 1
	}
	return result
}

func (s *buildSource) expandSourceComposition(fileIndex int, signature core.Signature, branches []script.DeclarationBranch, out io.Writer, authored *source.Source, lang string) bool {
	for _, branch := range branches {
		if branch.Condition != nil {
			result := s.compositionCondition(branch.Condition, out, authored, s.Files[fileIndex].Includes)
			selected := result.Selected
			failed := result.Diagnostic.Code != ""
			result.Diagnostic.Free(mem.System)
			if failed {
				return false
			}
			if !selected {
				continue
			}
		}
		for _, item := range branch.Items {
			switch item.Kind {
			case script.Include:
				if !s.expandSourceInclude(fileIndex, item.Include, item.OptionalInclude, item.Span, out, authored, lang) {
					return false
				}
			case script.Composition, script.Rule, script.DeclarationFor, script.Generate:
				if item.Kind == script.Composition && script.CompositionLoadsSources(item.Branches) {
					if !s.expandSourceComposition(fileIndex, signature, item.Branches, out, authored, lang) {
						return false
					}
					continue
				}
				s.RuleWork = s.RuleWork || declaresRuleWork(item)
				file := s.Files[fileIndex]
				s.Parts = append(s.Parts, program.CompileSource{Name: file.Name, Text: file.Text[item.Span.Start:item.Span.End], Lang: "km", Signature: signature, SourceDirectory: file.SourceDirectory, Offset: item.Span.Start, Includes: file.Includes})
			case script.DeclarationLet:
				file := s.Files[fileIndex]
				emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: "execution of declaration-time let is not available", Span: diagnostic.Span{Start: item.Span.Start, End: item.Span.End}, Frames: file.Includes}, s.JSON, authored)
				return false
			}
		}
		break
	}
	return true
}

func declaresRuleWork(item script.ScriptItem) bool {
	switch item.Kind {
	case script.Rule, script.Generate:
		return true
	case script.DeclarationFor:
		for _, child := range item.Loop.Items {
			if declaresRuleWork(child) {
				return true
			}
		}
	case script.Composition:
		for _, branch := range item.Branches {
			for _, child := range branch.Items {
				if declaresRuleWork(child) {
					return true
				}
			}
		}
	}
	return false
}

func (s *buildSource) expandSourceInclude(fileIndex int, name string, optional bool, span source.Span, out io.Writer, authored *source.Source, lang string) bool {
	file := s.Files[fileIndex]
	if !file.FileBacked && !path.IsAbs(name) && !strings.HasPrefix(name, "~/") {
		message := "relative include requires a file-backed source; this fragment was supplied inline"
		if file.Name == "<stdin>" {
			message = "relative include requires a file-backed source; this fragment was supplied on stdin"
		}
		tips := []string{"save this fragment as a .km or .kash file and run that file"}
		emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "RES_INVALID", Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: span.Start, End: span.End}, Tips: tips}, s.JSON, authored)
		return false
	}
	kash := script.IsKashSource(name)
	if lang == "kash" && !kash {
		emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "RES_INVALID", Severity: diagnostic.Error, Message: "Kash includes require a .kash or .ksh source", Span: diagnostic.Span{Start: span.Start, End: span.End}, Frames: file.Includes}, s.JSON, authored)
		return false
	}
	includeName := cloneCommandText(name)
	if strings.HasPrefix(name, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || !path.IsAbs(home) {
			emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "FS_ERR", Severity: diagnostic.Error, Message: "cannot resolve the home directory for include", Span: diagnostic.Span{Start: span.Start, End: span.End}, Frames: file.Includes}, s.JSON, authored)
			mem.FreeString(mem.System, includeName)
			return false
		}
		mem.FreeString(mem.System, includeName)
		includeName = path.Join(home, name[2:])
	} else if !path.IsAbs(includeName) {
		parent := path.Dir(file.Name)
		resolved := path.Join(parent, includeName)
		mem.FreeString(mem.System, parent)
		mem.FreeString(mem.System, includeName)
		includeName = resolved
	}
	canonical := path.Clean(includeName)
	mem.FreeString(mem.System, includeName)
	includeName = canonical
	for j := fileIndex; j >= 0; j = s.Files[j].Parent {
		if s.Files[j].Name == includeName {
			emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "DEP_CYCLE", Severity: diagnostic.Error, Message: "include cycle: " + includeName, Span: diagnostic.Span{Start: span.Start, End: span.End}}, s.JSON, authored)
			mem.FreeString(mem.System, includeName)
			return false
		}
	}
	data, readErr := os.ReadFile(includeName)
	if readErr != nil {
		// Both missing optional and failed required paths stay watch dependencies.
		s.Files = append(s.Files, sourceFile{Name: includeName, OwnedName: true, FileBacked: true, Parent: fileIndex})
		if optional && os.IsNotExist(readErr) {
			return true
		}
		emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "FS_ERR", Severity: diagnostic.Error, Message: "cannot read included source: " + includeName, Span: diagnostic.Span{Start: span.Start, End: span.End}}, s.JSON, authored)
		return false
	}
	includes := slices.Clone(file.Includes)
	includes = append(includes, diagnostic.Frame{Kind: "include", Label: includeName, Source: file.Name, Span: diagnostic.Span{Start: span.Start, End: span.End}})
	s.Files = append(s.Files, sourceFile{Name: includeName, Text: string(data), SourceDirectory: fileSourceDirectory(includeName), Data: data, OwnedName: true, FileBacked: true, Parent: fileIndex, Includes: includes})
	if kash {
		lang = "kash"
	}
	return expandIncludesLanguage(s, len(s.Files)-1, out, lang)
}

func newCompositionSource(inv cli.Invocation, defines []string, prefix []program.CompileSource) buildSource {
	return buildSource{JSON: inv.JSON, SelectionPrefix: prefix, SelectionDefines: defines, SelectionEnvironment: mergeEnvironment(posix.Environment(mem.System), inv.Environment), SelectionDirectory: inv.Directory, SelectionGrants: inv.Grants, SelectionNoDefault: inv.NoDefaultGrants || inv.ExplicitGrantsOnly}
}

func inlineRunSource(name string, text string, lang string, inv cli.Invocation, defines []string, out io.Writer, prefix []program.CompileSource) buildSource {
	result := newCompositionSource(inv, defines, prefix)
	result.Files = append(result.Files, sourceFile{Name: name, Text: text, Parent: -1})
	if !expandIncludesLanguage(&result, 0, out, lang) {
		result.Status = 1
	}
	result.SelectionPrefix = nil
	result.SelectionDefines = nil
	return result
}

func selectionPrefix(fragments []program.Fragment) []program.CompileSource {
	var sources []program.CompileSource
	for i := range fragments {
		if fragments[i].Lang == "km" || (fragments[i].Lang == "kash" && len(fragments[i].Includes) != 0) {
			f := fragments[i]
			sources = selectionDefinitions(sources, program.CompileSource{Name: f.Name, Text: f.Text, Offset: f.Offset, Includes: f.Includes}, f.Lang)
		}
	}
	return sources
}

// selectionOverrides returns an owned slice of borrowed declaration-selection
// operands. Definitions and ambiguous shorthand remain distinct in Invocation.
func selectionOverrides(defines []string, parameters []string) []string {
	values := slices.Clone(defines)
	for i := range parameters {
		values = append(values, parameters[i])
	}
	return values
}

func loadRunSourceInput(inv cli.Invocation, input cli.RunInput, fragments []program.Fragment, out io.Writer) buildSource {
	defines := selectionOverrides(inv.Defines, inv.Parameters)
	defer mem.FreeSlice(nil, defines)
	prefix := selectionPrefix(fragments)
	defer mem.FreeSlice(nil, prefix)
	if input.Kind == "discover" {
		return loadBuildSourceWithPrefix(buildArguments{Directory: inv.Directory, JSON: inv.JSON, Defines: defines, Environment: inv.Environment, Grants: inv.Grants, NoDefaultGrants: inv.NoDefaultGrants || inv.ExplicitGrantsOnly}, out, true, prefix)
	}
	name := input.Value
	if path.IsAbs(name) {
		return readRunSourceConfigured(name, out, input.Lang, inv, defines, prefix)
	}
	resolved := path.Join(inv.Directory, name)
	defer mem.FreeString(mem.System, resolved)
	return readRunSourceConfigured(resolved, out, input.Lang, inv, defines, prefix)
}

func inlineRunSourceInput(inv cli.Invocation, name string, text string, lang string, fragments []program.Fragment, out io.Writer) buildSource {
	defines := selectionOverrides(inv.Defines, inv.Parameters)
	defer mem.FreeSlice(nil, defines)
	prefix := selectionPrefix(fragments)
	defer mem.FreeSlice(nil, prefix)
	return inlineRunSource(name, text, lang, inv, defines, out, prefix)
}

// Predicate configuration consists only of declarations. Value statements and
// recipes retain their own grammar and execute later in the composed session.
func selectionDefinitions(sources []program.CompileSource, part program.CompileSource, lang string) []program.CompileSource {
	authored := source.New(mem.System, part.Name, part.Text)
	defer authored.Free(mem.System)
	parsed := script.ParseFragment(mem.System, authored, lang, 0, len(part.Text))
	defer parsed.Free()
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind == script.Definition {
			sources = append(sources, program.CompileSource{Name: part.Name, Text: part.Text[item.Span.Start:item.Span.End], Lang: lang, Offset: part.Offset + item.Span.Start, Includes: part.Includes})
		}
	}
	return sources
}
