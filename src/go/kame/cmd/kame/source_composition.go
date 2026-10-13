// File-backed source composition runs before declaration registration.
package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/expr"
	"kame/lang/script"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

func (s *buildSource) compositionCondition(condition *expr.Expr, out io.Writer, authored *source.Source, lang string) program.PredicateResult {
	b := strings.NewBuilder(mem.System)
	defer b.Free()
	b.WriteString(s.SelectionPrefix)
	for i := range s.Parts {
		writeSelectionDefinitions(&b, s.Parts[i].Name, s.Parts[i].Text, lang)
	}
	result := program.CompositionCondition(mem.System, authored.Name, b.String(), authored.Text[condition.Span.Start:condition.Span.End], s.SelectionDefines, s.SelectionEnvironment)
	if result.Diagnostic.Code != "" {
		view := result.Diagnostic
		view.Owned = false
		view.Source = authored.Name
		view.Span = diagnostic.Span{Start: condition.Span.Start, End: condition.Span.End}
		emitDiagnostic(out, view, s.JSON, authored)
		s.Status = 1
	}
	return result
}

// Only validated source-composition branches reach this traversal. Runtime
// expressions are never evaluated here to discover includes.
func (s *buildSource) expandSourceComposition(fileIndex int, expression *expr.Expr, out io.Writer, authored *source.Source, lang string) bool {
	if expression == nil || expression.Kind == expr.Nil {
		return true
	}
	include := script.CompositionInclude(expression)
	if include.OK {
		return s.expandSourceInclude(fileIndex, include.Path, include.Optional, include.Span, out, authored, lang)
	}
	if expression.Kind != expr.Application || len(expression.Items) < 3 || expression.Items[0].Kind != expr.Name || expression.Items[0].Text != "if" {
		emitDiagnostic(out, diagnostic.Diagnostic{Source: authored.Name, Code: "EXPR_INVALID", Severity: diagnostic.Error, Message: "source composition requires an include, if branch, or :nil", Span: diagnostic.Span{Start: expression.Span.Start, End: expression.Span.End}}, s.JSON, authored)
		return false
	}
	for i := 1; i+1 < len(expression.Items); i += 2 {
		result := s.compositionCondition(expression.Items[i], out, authored, lang)
		selected := result.Selected
		failed := result.Diagnostic.Code != ""
		result.Diagnostic.Free(mem.System)
		if failed {
			return false
		}
		if selected {
			return s.expandSourceComposition(fileIndex, expression.Items[i+1], out, authored, lang)
		}
	}
	if len(expression.Items)%2 == 0 {
		return s.expandSourceComposition(fileIndex, expression.Items[len(expression.Items)-1], out, authored, lang)
	}
	return true
}

func (s *buildSource) expandSourceInclude(fileIndex int, name string, optional bool, span source.Span, out io.Writer, authored *source.Source, lang string) bool {
	file := s.Files[fileIndex]
	if !file.FileBacked {
		message := "include requires a file-backed source; this fragment was supplied inline"
		if file.Name == "<stdin>" {
			message = "include requires a file-backed source; this fragment was supplied on stdin"
		}
		tips := []string{"save this fragment as a .km or .kmk file and run that file"}
		emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "FEATURE_UNSUP", Severity: diagnostic.Error, Message: message, Span: diagnostic.Span{Start: span.Start, End: span.End}, Tips: tips}, s.JSON, authored)
		return false
	}
	includeName := cloneCommandText(name)
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
		if s.Files[j].Name == includeName {
			emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "DEP_CYCLE", Severity: diagnostic.Error, Message: "include cycle: " + includeName, Span: diagnostic.Span{Start: span.Start, End: span.End}}, s.JSON, authored)
			mem.FreeString(mem.System, includeName)
			return false
		}
	}
	data, readErr := os.ReadFile(mem.System, includeName)
	if readErr != nil {
		// Both missing optional and failed required paths stay watch dependencies.
		s.Files = slices.Append(mem.System, s.Files, sourceFile{Name: includeName, OwnedName: true, FileBacked: true, Parent: fileIndex})
		if optional && readErr == os.ErrNotExist {
			return true
		}
		emitDiagnostic(out, diagnostic.Diagnostic{Source: file.Name, Code: "FS_ERR", Severity: diagnostic.Error, Message: "cannot read included source: " + includeName, Span: diagnostic.Span{Start: span.Start, End: span.End}}, s.JSON, authored)
		return false
	}
	s.Files = slices.Append(mem.System, s.Files, sourceFile{Name: includeName, Text: string(data), Data: data, OwnedName: true, FileBacked: true, Parent: fileIndex})
	return expandIncludesLanguage(s, len(s.Files)-1, out, lang)
}

func inlineRunSource(name string, text string, lang string, json bool, defines []string, environment []string, out io.Writer, prefix string) buildSource {
	result := buildSource{JSON: json, SelectionPrefix: prefix, SelectionDefines: defines, SelectionEnvironment: mergeEnvironment(posix.Environment(mem.System), environment)}
	result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: name, Text: text, Parent: -1})
	if !expandIncludesLanguage(&result, 0, out, lang) {
		result.Status = 1
	}
	result.SelectionPrefix = ""
	result.SelectionDefines = nil
	return result
}

func selectionPrefix(fragments []program.Fragment) string {
	b := strings.NewBuilder(mem.System)
	defer b.Free()
	for i := range fragments {
		if fragments[i].Lang == "km" || fragments[i].Lang == "kmk" {
			writeSelectionDefinitions(&b, fragments[i].Name, fragments[i].Text, fragments[i].Lang)
		}
	}
	return cloneCommandText(b.String())
}

// selectionOverrides returns an owned slice of borrowed declaration-selection
// operands. Definitions and ambiguous shorthand remain distinct in Invocation.
func selectionOverrides(defines []string, parameters []string) []string {
	values := slices.Clone(mem.System, defines)
	for i := range parameters {
		values = slices.Append(mem.System, values, parameters[i])
	}
	return values
}

func loadRunSourceInput(inv cli.Invocation, input cli.RunInput, fragments []program.Fragment, out io.Writer) buildSource {
	defines := selectionOverrides(inv.Defines, inv.Parameters)
	defer slices.Free(mem.System, defines)
	prefix := selectionPrefix(fragments)
	defer mem.FreeString(mem.System, prefix)
	if input.Kind == "discover" {
		return loadBuildSourceWithPrefix(buildArguments{Directory: inv.Directory, JSON: inv.JSON, Defines: defines, Environment: inv.Environment}, out, true, prefix)
	}
	name := input.Value
	if path.IsAbs(name) {
		return readRunSourceConfigured(name, out, input.Lang, inv.JSON, defines, inv.Environment, prefix)
	}
	resolved := path.Join(mem.System, inv.Directory, name)
	defer mem.FreeString(mem.System, resolved)
	return readRunSourceConfigured(resolved, out, input.Lang, inv.JSON, defines, inv.Environment, prefix)
}

func inlineRunSourceInput(inv cli.Invocation, name string, text string, lang string, fragments []program.Fragment, out io.Writer) buildSource {
	defines := selectionOverrides(inv.Defines, inv.Parameters)
	defer slices.Free(mem.System, defines)
	prefix := selectionPrefix(fragments)
	defer mem.FreeString(mem.System, prefix)
	return inlineRunSource(name, text, lang, inv.JSON, defines, inv.Environment, out, prefix)
}

// Predicate configuration consists only of declarations. Value statements and
// recipes retain their own grammar and execute later in the composed session.
func writeSelectionDefinitions(b *strings.Builder, name string, text string, lang string) {
	authored := source.New(mem.System, name, text)
	defer authored.Free(mem.System)
	parsed := script.ParseFragment(mem.System, authored, lang, 0, len(text))
	defer parsed.Free()
	for i := range parsed.Items {
		item := parsed.Items[i]
		if item.Kind == script.Definition {
			b.WriteString(text[item.Span.Start:item.Span.End])
			b.WriteByte('\n')
		}
	}
}
