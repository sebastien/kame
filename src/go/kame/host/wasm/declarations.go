package wasm

import (
 "kame/core"
 "kame/program"
 "solod.dev/so/mem"
 "solod.dev/so/slices"
)

// DeclarationPredicate is a host-free configuration query used by source
// composition. The descriptor contains name/prefix/predicate strings and
// defines/environment string arrays. Borrowed fields live until the query ends.
func DeclarationPredicate(a mem.Allocator, data []byte) PureResult {
 var value core.Value
 if !core.ParseJSON(a, data, &value) { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "invalid declaration predicate descriptor")} }
 defer value.Free(a)
 if value.Kind != core.Record { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate descriptor must be a record")} }
 name, prefix, predicate := "<declaration>", "", ""
 var defines []string
 var environment []string
 defer slices.Free(a, defines)
 defer slices.Free(a, environment)
 hasPredicate := false
 for i := range value.Record {
  field := value.Record[i]
  if field.Key == "name" || field.Key == "prefix" || field.Key == "predicate" {
   if field.Value.Kind != core.String { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate text fields must be strings")} }
   if field.Key == "name" { name = field.Value.Text } else if field.Key == "prefix" { prefix = field.Value.Text } else { predicate, hasPredicate = field.Value.Text, true }
  } else if field.Key == "defines" || field.Key == "environment" {
   if field.Value.Kind != core.List { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate settings must be string arrays")} }
   for j := range field.Value.List {
    setting := field.Value.List[j]
    if setting.Kind != core.String { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate settings must contain strings")} }
    if field.Key == "defines" { defines = slices.Append(a, defines, setting.Text) } else { environment = slices.Append(a, environment, setting.Text) }
   }
  } else { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "unknown declaration predicate descriptor field")} }
 }
 if !hasPredicate { return PureResult{Code: pureText(a, "EXPR_INVALID"), Message: pureText(a, "declaration predicate is required")} }
 result := program.DeclarationPredicate(a, name, prefix, predicate, defines, environment)
 defer result.Diagnostic.Free(a)
 if result.Diagnostic.Code != "" { return PureResult{Code: pureText(a, result.Diagnostic.Code), Message: pureText(a, result.Diagnostic.Message)} }
 if result.Selected { return PureResult{Text: pureText(a, "true")} }
 return PureResult{Text: pureText(a, "false")}
}
