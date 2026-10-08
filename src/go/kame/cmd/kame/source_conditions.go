package main

import (
	"kame/cli"
	"kame/diagnostic"
	"kame/host/posix"
	"kame/lang/script"
	"kame/lang/source"
	"kame/program"
	"solod.dev/so/io"
	"solod.dev/so/mem"
	"solod.dev/so/path"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

type sourceBranch struct {
	Parent   bool
	Selected bool
}

func declarationActive(stack []sourceBranch) bool {
	return len(stack) == 0 || (stack[len(stack)-1].Parent && stack[len(stack)-1].Selected)
}

func (s *buildSource) declarationBranch(item script.ScriptItem, text string, name string, stack []sourceBranch, out io.Writer, authored *source.Source, lang string) []sourceBranch {
	if item.Kind == script.Otherwise {
		stack[len(stack)-1].Selected = !stack[len(stack)-1].Selected
		return stack
	}
	if item.Kind == script.EndWhen {
		return stack[:len(stack)-1]
	}
	parent := declarationActive(stack)
	selected := false
	if parent {
		b := strings.NewBuilder(mem.System)
		b.WriteString(s.SelectionPrefix)
		for i := range s.Parts {
			writeSelectionDefinitions(&b, s.Parts[i].Name, s.Parts[i].Text, lang)
		}
		prefix := b.String()
		result := program.DeclarationPredicate(mem.System, name, prefix, text[item.Expression.Span.Start:item.Expression.Span.End], s.SelectionDefines, s.SelectionEnvironment)
		b.Free()
		if result.Diagnostic.Code != "" {
			emitDiagnostic(out, diagnostic.Diagnostic{Code: result.Diagnostic.Code, Message: result.Diagnostic.Message, Source: name, Severity: diagnostic.Error, Span: diagnostic.Span{Start: item.Expression.Span.Start, End: item.Expression.Span.End}}, s.JSON, authored)
			result.Diagnostic.Free(mem.System)
			s.Status = 1
		}
		selected = result.Selected
	}
	return slices.Append(mem.System, stack, sourceBranch{Parent: parent, Selected: selected})
}

func inlineRunSource(name string, text string, lang string, json bool, defines []string, environment []string, out io.Writer, prefix string) buildSource {
	result := buildSource{JSON: json, SelectionPrefix: prefix, SelectionDefines: defines, SelectionEnvironment: mergeEnvironment(posix.Environment(mem.System), environment)}
	result.Files = slices.Append(mem.System, result.Files, sourceFile{Name: name, Text: text, Parent: -1})
	if !expandIncludesLanguage(&result, 0, out, lang) {
		result.Status = 1
	}
	result.SelectionPrefix = ""
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
	for i := range parameters { values = slices.Append(mem.System, values, parameters[i]) }
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
