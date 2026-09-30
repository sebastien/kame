package program

import (
	"kame/diagnostic"
	"kame/lang/rule"
	"kame/lang/template"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/strings"
)

// failure builds an owned diagnostic. Dynamic messages come from stack
// concatenations, so they are cloned into the program allocator before they
// can outlive the function that produced them.
func failure(a mem.Allocator, code string, message string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{Code: cloneText(a, code), Severity: diagnostic.Error, Message: cloneText(a, message), Owned: true}
}
func failureAt(a mem.Allocator, code string, span diagnostic.Span, message string) diagnostic.Diagnostic {
	return diagnostic.Diagnostic{Code: cloneText(a, code), Severity: diagnostic.Error, Message: cloneText(a, message), Span: span, Owned: true}
}
func cloneText(a mem.Allocator, text string) string {
	if text == "" {
		return ""
	}
	b := mem.AllocSlice[byte](a, len(text), len(text))
	copy(b, []byte(text))
	return string(b)
}
func cloneCaptures(a mem.Allocator, in []template.CaptureValue) []template.CaptureValue {
	var out []template.CaptureValue
	for i := range in {
		out = slices.Append(a, out, template.CaptureValue{Name: cloneText(a, in[i].Name), Text: cloneText(a, in[i].Text)})
	}
	return out
}
func freeCaptures(a mem.Allocator, values []template.CaptureValue) {
	for i := range values {
		mem.FreeString(a, values[i].Name)
		mem.FreeString(a, values[i].Text)
	}
	slices.Free(a, values)
}

func renderTarget(a mem.Allocator, target rule.Target, captures []template.CaptureValue) string {
	if !target.Template {
		return cloneText(a, target.Text)
	}
	b := strings.NewBuilder(a)
	defer b.Free()
	for i := range target.TargetForm.Parts {
		part := target.TargetForm.Parts[i]
		if part.Kind == template.TargetLiteral {
			b.WriteString(part.Text)
		} else {
			for j := range captures {
				if captures[j].Name == part.Name {
					b.WriteString(captures[j].Text)
					break
				}
			}
		}
	}
	return cloneText(a, b.String())
}

func renderInput(a mem.Allocator, input rule.Input, captures []template.CaptureValue) string {
	target := rule.Target{Text: input.Text, Template: true, TargetForm: input.TargetForm}
	return renderTarget(a, target, captures)
}
