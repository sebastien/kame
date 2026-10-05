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

func cloneArguments(a mem.Allocator, in []ArgumentValue) []ArgumentValue {
	var out []ArgumentValue
	for i := range in {
		out = slices.Append(a, out, ArgumentValue{Name: cloneText(a, in[i].Name), Value: cloneText(a, in[i].Value)})
	}
	return out
}

func renderTarget(a mem.Allocator, target rule.Target, captures []template.CaptureValue) string {
	if !target.Template {
		return cloneText(a, target.Text)
	}
	b := strings.NewBuilder(a)
	defer b.Free()
	index := 0
	for i := range target.TargetForm.Parts {
		part := target.TargetForm.Parts[i]
		if part.Kind == template.TargetLiteral {
			b.WriteString(part.Text)
		} else {
			captureIndex := index
			index++
			found := false
			if part.Name == "" {
				if captureIndex < len(captures) {
					b.WriteString(captures[captureIndex].Text)
					found = true
				}
			} else {
				for j := range captures {
					if captures[j].Name == part.Name {
						b.WriteString(captures[j].Text)
						found = true
						break
					}
				}
			}
			if !found {
				if part.Name != "" {
					if positional, ok := positionalCapture(part.Name); ok {
						captureIndex = positional
					} else {
						continue
					}
				}
				if captureIndex >= 0 && captureIndex < len(captures) {
					b.WriteString(captures[captureIndex].Text)
				}
			}
		}
	}
	return cloneText(a, b.String())
}

func positionalCapture(name string) (int, bool) {
	if len(name) < 2 || name[0] != '_' || (len(name) > 2 && name[1] == '0') {
		return 0, false
	}
	index := 0
	for i := 1; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return 0, false
		}
		digit := int(name[i] - '0')
		if index > (int(^uint(0)>>1)-digit)/10 {
			return 0, false
		}
		index = index*10 + digit
	}
	return index, true
}

func renderInput(a mem.Allocator, input rule.Input, captures []template.CaptureValue) string {
	target := rule.Target{Text: input.Text, Template: true, TargetForm: input.TargetForm}
	return renderTarget(a, target, captures)
}
