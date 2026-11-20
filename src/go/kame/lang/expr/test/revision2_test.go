package exprcheck

import (
	"bytes"
	"encoding/json"
	"kame/internal/mem"
	"kame/lang/ast"
	"kame/lang/expr"
	"kame/lang/source"
	"strings"
	"testing"
)

func TestHomeRelativePathAtom(t *testing.T) {
	const text = "~/src/main.c"
	result := expr.Parse(nil, "paths.km", text)
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Expr == nil || result.Expr.Kind != expr.Path || result.Expr.Text != text {
		t.Fatalf("expected one home-relative path atom: %v", result.Diagnostics)
	}
}

func TestPathAtomBoundaries(t *testing.T) {
	for _, test := range []struct{ atom, suffix string }{
		{`./{name:*}.c`, `,next`},
		{`//server/share`, `,next`},
		{`./${dir}/file`, `}`},
		{`./[a-z]`, `]`},
		{`./a\,b`, `,next`},
		{`./{name:a,b}`, `,next`},
		{`./file`, `)`},
		{`./parent(dir,sub)/file`, `)`},
		{`./name"|suffix`, `]`},
		{`./{name:~(foo|bar)}/file`, `}`},
	} {
		text := test.atom + test.suffix
		s := source.New(nil, "paths.km", text)
		r := expr.ParsePrefix(nil, s, 0)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Path || r.End != len(test.atom) || r.Expr.Text != test.atom {
			t.Errorf("%q: incorrect path boundary at byte %d: %v", text, r.End, r.Diagnostics)
		}
		expr.Free(nil, r.Expr)
		s.Free(nil)
	}
	for _, text := range []string{`./[]`, `./[!]`, `./[z-a]`, `./[abc`} {
		r := expr.Parse(nil, "paths.km", text)
		if len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: wanted one PARSE_ERR, got %v", text, r.Diagnostics)
		}
		r.Free()
	}
}

func TestActionAtomsPreserveGraphAndForm(t *testing.T) {
	for _, opener := range []string{"$(", "$?(", "$&("} {
		for _, body := range []string{"printf hello | cat", "printf hello && printf world"} {
			text := opener + body + ")"
			t.Run(text, func(t *testing.T) {
				r := expr.Parse(nil, "actions.km", text)
				defer r.Free()
				if r.Expr == nil || len(r.Diagnostics) != 0 {
					t.Fatalf("parse failed: %v", r.Diagnostics)
				}
				want := expr.CommandCapture
				if body == "printf hello && printf world" {
					want = expr.CommandList
				}
				if r.Expr.Kind != want || len(r.Expr.Items) != 2 || r.Expr.Span.Start != 0 || r.Expr.Span.End != len(text) {
					t.Fatalf("action graph lost its structure or span: %+v", r.Expr)
				}
				clone := expr.Clone(nil, r.Expr)
				defer expr.Free(nil, clone)
				got := expr.Format(nil, clone)
				defer mem.FreeString(nil, got)
				if got != text {
					t.Fatalf("formatted as %q, want %q", got, text)
				}
			})
		}
		bad := expr.Parse(nil, "actions.km", opener+"printf hello &)")
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: terminal & must be rejected inside an action atom", opener)
		}
		bad.Free()
	}
}

func TestActionAtomJSON(t *testing.T) {
	for _, opener := range []string{"$?(", "$&("} {
		var out bytes.Buffer
		if code := ast.WriteAST(&out, "expr", "actions.km", opener+"printf hello)"); code != 0 {
			t.Fatalf("AST encoding failed: %s", out.String())
		}
		var parsed struct {
			AST struct {
				ActionPrefix string
			}
		}
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || parsed.AST.ActionPrefix != opener {
			t.Fatalf("AST did not retain %q: %s (%v)", opener, out.String(), err)
		}
	}
}

func TestRawStringAtom(t *testing.T) {
	const text = `'a\n ${name} @ "quoted"
second line'`
	result := expr.Parse(nil, "literal.km", text)
	defer result.Free()
	if len(result.Diagnostics) != 0 || result.Expr == nil || result.Expr.Kind != expr.String {
		t.Fatalf("expected a raw string, got %v", result.Diagnostics)
	}
	parts := result.Expr.Parts
	if len(parts) != 1 || parts[0].Expr != nil || parts[0].Text != text[1:len(text)-1] {
		t.Fatalf("raw content was interpreted: %+v", parts)
	}
	formatted := expr.Format(nil, result.Expr)
	defer mem.FreeString(nil, formatted)
	if formatted != text {
		t.Fatalf("raw string formatted as %q", formatted)
	}
	bad := expr.Parse(nil, "literal.km", "'unterminated")
	defer bad.Free()
	if len(bad.Diagnostics) != 1 || bad.Diagnostics[0].Code != "PARSE_ERR" {
		t.Fatalf("expected PARSE_ERR for unclosed raw string: %v", bad.Diagnostics)
	}
}

func TestStringAtEscape(t *testing.T) {
	for _, test := range []struct{ text, literal string }{
		{`"a@b"`, `a@b`},
		{`"@(missing)"`, `@(missing)`},
		{`"\${name}"`, `${name}`},
		{`"@{name}"`, `@{name}`},
		{`"@${name}"`, `@`},
	} {
		r := expr.Parse(nil, "strings.km", test.text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || len(r.Expr.Parts) == 0 || r.Expr.Parts[0].Expr != nil || r.Expr.Parts[0].Text != test.literal {
			t.Errorf("%s: incorrect literal escape: %v", test.text, r.Diagnostics)
		}
		if test.text == `"@${name}"` && (r.Expr == nil || len(r.Expr.Parts) != 2 || r.Expr.Parts[1].Expr == nil) {
			t.Error("literal @ swallowed the following interpolation")
		}
		formatted := expr.Format(nil, r.Expr)
		again := expr.Parse(nil, "strings.km", formatted)
		twice := expr.Format(nil, again.Expr)
		if len(again.Diagnostics) != 0 || again.Expr == nil || r.Expr == nil || len(again.Expr.Parts) == 0 || len(again.Expr.Parts) != len(r.Expr.Parts) || again.Expr.Parts[0].Text != test.literal || twice != formatted {
			t.Errorf("%s: failed format/reparse round trip: %q", test.text, formatted)
		}
		mem.FreeString(nil, twice)
		again.Free()
		mem.FreeString(nil, formatted)
		r.Free()
	}
}

func TestReferenceFieldSyntax(t *testing.T) {
	const text = `record."key with space"."0"`
	r := expr.Parse(nil, "references.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Reference || len(r.Expr.Reference) != 3 {
		t.Fatalf("quoted field syntax did not parse: %v", r.Diagnostics)
	}
	if first, second := r.Expr.Reference[1], r.Expr.Reference[2]; first.Text != "key with space" || first.Span.Start != 6 || first.Span.End != 23 || second.Text != "0" || second.Kind != expr.ReferenceName {
		t.Errorf("quoted field lost its key kind or span: %+v", r.Expr.Reference)
	}
	clone := expr.Clone(nil, r.Expr)
	defer expr.Free(nil, clone)
	formatted := expr.Format(nil, clone)
	defer mem.FreeString(nil, formatted)
	if formatted != text {
		t.Errorf("format = %q, want %q", formatted, text)
	}
	bad := expr.Parse(nil, "references.km", `a/b/c`)
	defer bad.Free()
	if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
		t.Errorf("multiple namespace levels accepted: %v", bad.Diagnostics)
	}
}

func TestQuotedNamespaceSegment(t *testing.T) {
	for _, test := range []struct{ text, name string }{
		{`x/"python3.12"`, `x/python3.12`},
		{`lib/"record-key"."field name"`, `lib/record-key`},
	} {
		r := expr.Parse(nil, "references.km", test.text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || len(r.Expr.Reference) == 0 || r.Expr.Reference[0].Text != test.name {
			t.Errorf("%s: incorrect qualified name: %v", test.text, r.Diagnostics)
		}
		clone := expr.Clone(nil, r.Expr)
		r.Free()
		formatted := expr.Format(nil, clone)
		if formatted != test.text {
			t.Errorf("format = %q, want %q", formatted, test.text)
		}
		mem.FreeString(nil, formatted)
		expr.Free(nil, clone)
	}
	for _, text := range []string{`lib/"${value}"`, `lib/"name"/extra`} {
		r := expr.Parse(nil, "references.km", text)
		if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: expected PARSE_ERR", text)
		}
		r.Free()
	}
}

func TestQuotedKeyAndPatternSpelling(t *testing.T) {
	for _, tc := range []struct{ input, spelling string }{
		{`record."\{id}"`, `"\{id}"`},
		{`x/"with\@sign"`, `"with\@sign"`},
		{`{"\{id}": 1}`, `"\{id}"`},
		{"match value\n\t\"\\@prefix/{name:*}\" => 1\n\telse => 0", `"\@prefix/{name:*}"`},
	} {
		parsed := expr.Parse(nil, "spelling.km", tc.input)
		if len(parsed.Diagnostics) != 0 {
			t.Fatalf("parse %q: %v", tc.input, parsed.Diagnostics)
		}
		clone := expr.Clone(nil, parsed.Expr)
		parsed.Free()
		formatted := expr.Format(nil, clone)
		expr.Free(nil, clone)
		reparsed := expr.Parse(nil, "spelling.km", formatted)
		again := expr.Format(nil, reparsed.Expr)
		if !strings.Contains(formatted, tc.spelling) || len(reparsed.Diagnostics) != 0 || formatted != again {
			t.Errorf("formatting changed %q to %q: %v", tc.input, formatted, reparsed.Diagnostics)
		}
		mem.FreeString(nil, again)
		mem.FreeString(nil, formatted)
		reparsed.Free()
	}
}

func TestSelectorsOnValueAtoms(t *testing.T) {
	for _, text := range []string{`[1, 2].0`, `(nop record)."a key"`, `$?(true).ok`, `:+dir.name`, `true.field`} {
		r := expr.Parse(nil, "references.km", text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Reference || len(r.Expr.Items) != 1 || len(r.Expr.Reference) != 1 || r.Expr.Span.End != len(text) {
			t.Errorf("%s: expected one selection on a value atom: %v", text, r.Diagnostics)
		}
		formatted := expr.Format(nil, r.Expr)
		if formatted != text {
			t.Errorf("format = %q, want %q", formatted, text)
		}
		mem.FreeString(nil, formatted)
		r.Free()
	}
}

func TestKeywordLiterals(t *testing.T) {
	for _, test := range []struct {
		text  string
		kind  expr.Kind
		truth bool
	}{
		{"true", expr.Boolean, true},
		{"false", expr.Boolean, false},
		{"nil", expr.Nil, false},
		{":true", expr.Symbol, false},
		{":false", expr.Symbol, false},
		{":nil", expr.Symbol, false},
		{":+true", expr.Symbol, false},
		{":-false", expr.Symbol, false},
		{"true?", expr.Name, false},
		{"true-value", expr.Name, false},
		{"std/true", expr.Name, false},
	} {
		r := expr.Parse(nil, "literals.km", test.text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != test.kind || r.Expr.Bool != test.truth {
			t.Errorf("%s: incorrect keyword classification: %v", test.text, r.Diagnostics)
		}
		got := expr.Format(nil, r.Expr)
		if got != test.text {
			t.Errorf("format = %q, want %q", got, test.text)
		}
		mem.FreeString(nil, got)
		r.Free()
	}
}

func TestStatementKeywordsCannotBeValueNames(t *testing.T) {
	for _, word := range strings.Fields("def let set rule task service always cached generate then elif else for in while break continue return yield exit defer with stream import from as export") {
		for _, input := range []string{word, "list " + word, "{" + word + "}"} {
			parsed := expr.Parse(nil, "keyword.km", input)
			if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
				t.Errorf("%q accepted a statement keyword as a value name", input)
			}
			parsed.Free()
		}
	}
	for _, input := range []string{"sep", "set!", "return?", ":return", "x/return", "{return: 42}.return"} {
		parsed := expr.Parse(nil, "keyword.km", input)
		if len(parsed.Diagnostics) != 0 || parsed.Expr == nil {
			t.Errorf("%q rejected a contextual name: %v", input, parsed.Diagnostics)
		}
		parsed.Free()
	}
}

func TestJuxtaposedApplication(t *testing.T) {
	for _, test := range []struct {
		text string
		argc int
	}{
		{"f a b", 2},
		{"f (g a) b", 2},
		{"f ()", 0},
		{"f \\\n a", 1},
	} {
		r := expr.Parse(nil, "calls.km", test.text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Application || len(r.Expr.Items) != test.argc+1 {
			t.Errorf("%q: expected an application of arity %d: %v", test.text, test.argc, r.Diagnostics)
		}
		got := expr.Format(nil, r.Expr)
		want := test.text
		if test.text == "f \\\n a" {
			want = "f a"
		}
		if got != want {
			t.Errorf("format = %q, want %q", got, want)
		}
		mem.FreeString(nil, got)
		r.Free()
	}
	for _, text := range []string{"f a ()", "f () a", "f\na"} {
		r := expr.Parse(nil, "calls.km", text)
		if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected PARSE_ERR", text)
		}
		r.Free()
	}
}

func TestGroupedZeroArgumentCall(t *testing.T) {
	const text = "outer (inner ()).field"
	result := expr.Parse(nil, "calls.km", text)
	defer result.Free()
	if len(result.Diagnostics) != 0 {
		t.Fatalf("parse failed: %v", result.Diagnostics)
	}
	field := result.Expr.Items[1]
	group := field.Items[0]
	if field.Kind != expr.Reference || group.Kind != expr.Group ||
		group.Items[0].Kind != expr.Application || len(group.Items[0].Items) != 1 ||
		!group.Items[0].Juxtaposed || group.Items[0].Items[0].Text != "inner" {
		t.Fatalf("group lost its zero-argument call: %+v", group)
	}
	if formatted := expr.Format(nil, result.Expr); formatted != text {
		t.Errorf("formatted %q, want %q", formatted, text)
	}
	for _, input := range []string{"(f x ())", "(f () x)", "(f () ())"} {
		bad := expr.Parse(nil, "calls.km", input)
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected PARSE_ERR, got %v", input, bad.Diagnostics)
		}
		bad.Free()
	}
}

func TestInfixPrecedence(t *testing.T) {
	const text = "f 1 2 * 3 + 4 / 2 == 8 and true or false ?? nil"
	r := expr.Parse(nil, "operators.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatalf("parse failed: %v", r.Diagnostics)
	}
	operands := func(e *expr.Expr, spelling string) (*expr.Expr, *expr.Expr) {
		t.Helper()
		if e == nil || e.Kind != expr.Application || len(e.Items) != 3 || e.Infix != spelling {
			t.Fatalf("expected binary %q, got %+v", spelling, e)
		}
		return e.Items[1], e.Items[2]
	}
	or, _ := operands(r.Expr, "??")
	and, _ := operands(or, "or")
	comparison, _ := operands(and, "and")
	add, _ := operands(comparison, "==")
	product, division := operands(add, "+")
	call, _ := operands(product, "*")
	operands(division, "/")
	if call.Kind != expr.Application || !call.Juxtaposed || len(call.Items) != 3 || call.Items[0].Text != "f" {
		t.Fatal("application must bind more tightly than multiplication")
	}
	formatted := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, formatted)
	if formatted != text {
		t.Errorf("format = %q, want %q", formatted, text)
	}
	fallback := expr.Parse(nil, "operators.km", "a ?? b ?? c")
	defer fallback.Free()
	_, fallbackRight := operands(fallback.Expr, "??")
	operands(fallbackRight, "??")
	subtraction := expr.Parse(nil, "operators.km", "a-b - -1")
	defer subtraction.Free()
	lhs, rhs := operands(subtraction.Expr, "-")
	if lhs.Kind != expr.Name || lhs.Text != "a-b" || rhs.Kind != expr.Integer || rhs.Int != -1 {
		t.Error("subtraction confused a hyphenated name or negative literal with an operator")
	}
	for _, invalid := range []string{"a < b < c", "a = b", "1-2", "-x", "a/ 2"} {
		bad := expr.Parse(nil, "operators.km", invalid)
		if len(bad.Diagnostics) == 0 {
			t.Errorf("%q: expected PARSE_ERR", invalid)
		}
		bad.Free()
	}
}

func TestGroupedInfixKeepsParentheses(t *testing.T) {
	const text = "f (a + b * c) (g x y == z)"
	r := expr.Parse(nil, "operators.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || len(r.Expr.Items) != 3 {
		t.Fatalf("grouped infix calls did not parse: %v", r.Diagnostics)
	}
	for i, operator := range []string{"+", "=="} {
		group := r.Expr.Items[i+1]
		if group.Kind != expr.Group || len(group.Items) != 1 || group.Items[0].Infix != operator {
			t.Errorf("expected group over %q, got %+v", operator, group)
		}
	}
	formatted := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, formatted)
	if formatted != text {
		t.Errorf("format = %q, want %q", formatted, text)
	}
	r2 := expr.Parse(nil, "operators.km", "(_ + 1)")
	defer r2.Free()
	if len(r2.Diagnostics) != 0 || r2.Expr == nil || r2.Expr.Kind != expr.Section || len(r2.Expr.Parameters) != 1 || r2.Expr.Body[0].Infix != "+" {
		t.Errorf("infix group did not become a section: %v", r2.Diagnostics)
	}
}

func TestConditionalExpression(t *testing.T) {
	const text = "if ready? server then f a elif retry? job then g b else nil"
	r := expr.Parse(nil, "conditional.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Conditional || len(r.Expr.Items) != 5 {
		t.Fatalf("expected an if/elif/else expression: %v", r.Diagnostics)
	}
	for i, head := range []string{"ready?", "f", "retry?", "g"} {
		e := r.Expr.Items[i]
		if e.Kind != expr.Application || len(e.Items) != 2 || e.Items[0].Text != head {
			t.Errorf("conditional operand %d: %+v", i, e)
		}
	}
	formatted := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, formatted)
	if formatted != text {
		t.Errorf("format = %q, want %q", formatted, text)
	}
	nested := expr.Parse(nil, "conditional.km", "if true then if false then 1 else 2 else 3")
	defer nested.Free()
	if len(nested.Diagnostics) != 0 || nested.Expr == nil || len(nested.Expr.Items) != 3 || nested.Expr.Items[1].Kind != expr.Conditional || nested.Expr.Items[2].Int != 3 {
		t.Errorf("nested conditional captured the outer else: %v", nested.Diagnostics)
	}
	for _, invalid := range []string{"if", "if true then 1", "if true 1 else 2", "if true then else 2", "if true then 1 else"} {
		bad := expr.Parse(nil, "conditional.km", invalid)
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected PARSE_ERR", invalid)
		}
		bad.Free()
	}
}

func TestArrowLambdas(t *testing.T) {
	for _, test := range []struct {
		text  string
		arity int
		rest  bool
		group bool
	}{
		{"(a b => a * b)", 2, false, true},
		{"(() => now ())", 0, false, true},
		{"(first rest... => first)", 2, true, true},
		{"x => x - 1", 1, false, false},
	} {
		r := expr.Parse(nil, "lambda.km", test.text)
		got := expr.Format(nil, r.Expr)
		body := r.Expr
		if test.group && body != nil && body.Kind == expr.Group {
			body = body.Items[0]
		}
		if len(r.Diagnostics) != 0 || body == nil || body.Kind != expr.Lambda || len(body.Parameters) != test.arity || len(body.Body) != 1 || (test.rest && !body.Parameters[test.arity-1].Rest) {
			t.Errorf("%s: unexpected lambda AST: %v", test.text, r.Diagnostics)
		}
		if got != test.text {
			t.Errorf("format = %q, want %q", got, test.text)
		}
		mem.FreeString(nil, got)
		r.Free()
	}
	for _, text := range []string{"(x x => x)", "(x... y => y)", "(true => true)", "(() =>)"} {
		r := expr.Parse(nil, "lambda.km", text)
		if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: expected PARSE_ERR", text)
		}
		r.Free()
	}
}

func TestCommaSeparatedLists(t *testing.T) {
	const text = `[1, -2, ./src/{name:*}.c, ...xs,]`
	r := expr.Parse(nil, "lists.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.List || len(r.Expr.Items) != 4 || r.Expr.Items[2].Pattern == nil || r.Expr.Items[3].Kind != expr.ListSpread {
		t.Fatalf("comma-separated list did not retain its items: %v", r.Diagnostics)
	}
	got := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, got)
	if got != `[1, -2, ./src/{name:*}.c, ...xs]` {
		t.Errorf("format = %q", got)
	}
	again := expr.Parse(nil, "lists.km", got)
	defer again.Free()
	twice := expr.Format(nil, again.Expr)
	defer mem.FreeString(nil, twice)
	if len(again.Diagnostics) != 0 || got != twice {
		t.Errorf("format/reparse is not idempotent: %q, %v", twice, again.Diagnostics)
	}
	longString := `"` + strings.Repeat("a", 80) + `"`
	expanded := expr.Parse(nil, "lists.km", "["+longString+", 2]")
	defer expanded.Free()
	layout := expr.Format(nil, expanded.Expr)
	defer mem.FreeString(nil, layout)
	if want := "[\n\t" + longString + "\n\t2\n]"; layout != want {
		t.Errorf("expanded format = %q, want %q", layout, want)
	}
	for _, invalid := range []string{"[,]", "[1,, 2]", "[1, ...]"} {
		bad := expr.Parse(nil, "lists.km", invalid)
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: expected PARSE_ERR", invalid)
		}
		bad.Free()
	}
}

func TestBraceRecordSyntax(t *testing.T) {
	const text = `{name: build tag, version, "key with space": ./src/{name:*}.c, ...defaults,}`
	r := expr.Parse(nil, "records.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Record || len(r.Expr.Fields) != 4 {
		t.Fatalf("expected four record fields: %v", r.Diagnostics)
	}
	fields := r.Expr.Fields
	if fields[0].Key != "name" || fields[0].Value.Kind != expr.Application || fields[1].Key != "version" || !fields[1].Shorthand || fields[2].Key != "key with space" || !fields[2].Quoted || fields[2].Value.Kind != expr.Path || !fields[3].Spread {
		t.Errorf("record fields lost their authored forms: %+v", fields)
	}
	got := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, got)
	if got != strings.TrimSuffix(text, ",}")+"}" {
		t.Errorf("format = %q", got)
	}
	for _, text := range []string{`{key: 1, "key": 2}`, `{key: 1, key}`, `{...}`, `{...[1]}`, `{"key"}`, `{*}.c`, `{name:*}.c`, `{~a+}`} {
		bad := expr.Parse(nil, "records.km", text)
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: expected PARSE_ERR", text)
		}
		bad.Free()
	}
}

func TestRevisionTwoValuePipeAST(t *testing.T) {
	const text = `xs ?? [] |> filter (_ != nil) |> join "-" _`
	r := expr.Parse(nil, "pipes.km", text)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.ValuePipe || len(r.Expr.Items) != 3 {
		t.Fatalf("expected a value pipe with two stages: %v", r.Diagnostics)
	}
	subject, first, second := r.Expr.Items[0], r.Expr.Items[1], r.Expr.Items[2]
	if subject.Infix != "??" || first.Kind != expr.Application || first.Items[1].Kind != expr.Section ||
		second.Kind != expr.Application || second.Items[2].Kind != expr.Placeholder || second.Items[2].Text != "_" {
		t.Errorf("pipe or nested section lost its structure")
	}
	got := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, got)
	if got != text {
		t.Errorf("formatted pipe = %q, want %q", got, text)
	}
	group := expr.Parse(nil, "pipes.km", "("+text+")")
	defer group.Free()
	if len(group.Diagnostics) != 0 || group.Expr == nil || group.Expr.Kind != expr.Group || group.Expr.Items[0].Kind != expr.ValuePipe {
		t.Errorf("grouped pipe was not retained or became a section: %v", group.Diagnostics)
	}
	for _, invalid := range []string{"xs |> ", "xs |> f __", "xs |> f _0", "xs |> f ?? y", "xs |> 1", "xs | f"} {
		bad := expr.Parse(nil, "pipes.km", invalid)
		if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected PARSE_ERR", invalid)
		}
		bad.Free()
	}
}

func TestOperatorLineContinuation(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"x +\n\ty", "x + y"},
		{"x\r\n\t+ y", "x + y"},
		{"xs\n\t|> f\n\t|> g", "xs |> f |> g"},
		{"xs |>\n\tf", "xs |> f"},
	} {
		r := expr.Parse(nil, "operators.km", test.text)
		if len(r.Diagnostics) != 0 {
			t.Errorf("%q: parse failed: %v", test.text, r.Diagnostics)
		}
		formatted := expr.Format(nil, r.Expr)
		if formatted != test.want {
			t.Errorf("format = %q, want %q", formatted, test.want)
		}
		mem.FreeString(nil, formatted)
		r.Free()
	}
	for _, text := range []string{"x\n+ y", "\tx\n\t+ y", "xs\n|> f"} {
		r := expr.Parse(nil, "operators.km", text)
		if len(r.Diagnostics) == 0 {
			t.Errorf("%q: accepted a continuation without more indentation", text)
		}
		r.Free()
	}
	r := expr.Parse(nil, "operators.km", `x`+strings.Repeat("x", 65)+` |> first-stage |> second-stage`)
	defer r.Free()
	formatted := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, formatted)
	if !strings.HasSuffix(formatted, "\n\t|> first-stage\n\t|> second-stage") {
		t.Errorf("expected expanded pipe stages: %q", formatted)
	}
	again := expr.Parse(nil, "operators.km", formatted)
	defer again.Free()
	twice := expr.Format(nil, again.Expr)
	defer mem.FreeString(nil, twice)
	if len(again.Diagnostics) != 0 || twice != formatted {
		t.Errorf("expanded pipe did not round-trip: %q (%v)", twice, again.Diagnostics)
	}
}

func TestValueExpressionsInInterpolation(t *testing.T) {
	for _, text := range []string{`"Hi ${upper name}"`, `${root ?? ./fallback}/file`, `./${if ready then name else "default"}.c`, `"${{key: "value"}."key"}"`} {
		r := expr.Parse(nil, "interpolation.km", text)
		if len(r.Diagnostics) != 0 || r.Expr == nil {
			t.Errorf("%s: interpolation did not parse: %v", text, r.Diagnostics)
		}
		got := expr.Format(nil, r.Expr)
		if got != text {
			t.Errorf("formatted interpolation = %q, want %q", got, text)
		}
		mem.FreeString(nil, got)
		r.Free()
	}
}

func TestExpressionLineComments(t *testing.T) {
	s := source.New(nil, "comments.km", "# preface\r\n a + 2 # trailing")
	defer s.Free(nil)
	r := expr.ParseRange(nil, s, 0, len(s.Text), "")
	defer expr.Free(nil, r.Expr)
	if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Infix != "+" || r.Expr.Span.Start != 12 || r.Expr.Span.End != 17 {
		t.Fatalf("comments changed the expression or its authored span: %+v, %v", r.Expr, r.Diagnostics)
	}
	for _, text := range []string{`"x#y"`, `./x#y`, `$(printf x#y)`} {
		r := expr.Parse(nil, "comments.km", text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Span.End != len(text) {
			t.Errorf("%q: # inside a token was treated as a comment: %v", text, r.Diagnostics)
		}
		r.Free()
	}
}

func TestPlaceholderSectionBoundaries(t *testing.T) {
	for _, test := range []struct {
		text  string
		arity int
	}{
		{`(f _ __ ___)`, 3},
		{`(f _1)`, 2},
		{`(f _ (g __))`, 1},
		{`(f "${_}")`, 1},
	} {
		r := expr.Parse(nil, "sections.km", test.text)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Section || len(r.Expr.Parameters) != test.arity {
			t.Errorf("%s: incorrect section arity: %v", test.text, r.Diagnostics)
		}
		got := expr.Format(nil, r.Expr)
		if got != test.text {
			t.Errorf("format = %q, want %q", got, test.text)
		}
		mem.FreeString(nil, got)
		r.Free()
	}
	for _, text := range []string{`f _`, `(f [_, 1])`, `(f {key: _})`, `(f (x => _))`} {
		r := expr.Parse(nil, "sections.km", text)
		if len(r.Diagnostics) == 0 || r.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%s: unowned placeholder must be PARSE_ERR", text)
		}
		r.Free()
	}
}

func TestMatchExpressionArms(t *testing.T) {
	input := "(match source\n    ./{name}.c => name\n    \"other\" => false\n    else => (x => x))"
	result := expr.Parse(nil, "match.km", input)
	defer result.Free()
	if len(result.Diagnostics) != 0 {
		t.Fatalf("parse failed: %v", result.Diagnostics)
	}
	match := result.Expr.Items[0]
	if result.Expr.Kind != expr.Group || match.Kind != expr.Match || len(match.Items) != 3 {
		t.Fatalf("unexpected match structure: %+v", result.Expr)
	}
	pattern := match.Items[0].Items[0].Pattern
	if pattern == nil || pattern.References != 0 || pattern.Matchers != 1 || pattern.Names[0] != "name" {
		t.Fatalf("arm pattern did not capture name: %+v", pattern)
	}
	formatted := expr.Format(nil, result.Expr)
	defer mem.FreeString(nil, formatted)
	want := "(\n\tmatch source\n\t\t./{name}.c => name\n\t\t\"other\"    => false\n\t\telse       => (x => x)\n)"
	if formatted != want {
		t.Fatalf("formatted %q, want %q", formatted, want)
	}
	reparsed := expr.Parse(nil, "formatted.km", formatted)
	defer reparsed.Free()
	if len(reparsed.Diagnostics) != 0 || expr.Format(nil, reparsed.Expr) != formatted {
		t.Fatalf("format did not round-trip: %v", reparsed.Diagnostics)
	}

	for _, input := range []string{
		"match x",
		"match x\n    name => 1",
		"match x\n    else => 1\n    2 => 3",
		"match x\n    1 => x => x",
		"match x\n    1 =>\n    2",
		"(match x\n    1 => 2 3 => 4)",
	} {
		result := expr.Parse(nil, "invalid-match.km", input)
		if len(result.Diagnostics) == 0 || result.Diagnostics[0].Code != "PARSE_ERR" {
			t.Errorf("%q: expected PARSE_ERR, got %v", input, result.Diagnostics)
		}
		result.Free()
	}
}

func TestMatchExpressionJSON(t *testing.T) {
	var buffer bytes.Buffer
	if status := ast.WriteAST(&buffer, "expr", "match.km", "match true\n    true => 1\n    else => 2"); status != 0 {
		t.Fatalf("match AST failed: %s", buffer.String())
	}
	for _, field := range []string{`"schema":2`, `"kind":"match"`, `"kind":"match-arm"`, `"subject":`, `"pattern":`, `"result":`} {
		if !strings.Contains(buffer.String(), field) {
			t.Errorf("missing %q in %s", field, buffer.String())
		}
	}
	for _, input := range []string{"match", "match true\n    true =>"} {
		buffer.Reset()
		if status := ast.WriteAST(&buffer, "expr", "match.km", input); status != 1 || !json.Valid(buffer.Bytes()) {
			t.Errorf("invalid match needs a diagnostic AST: %s", buffer.String())
		}
	}
}

func TestApplicationSpreadSyntax(t *testing.T) {
	for _, input := range []string{"f 1 ...xs 2", "(f ...[1, 2])", "f ...(g xs)", "xs |> f ...ys"} {
		parsed := expr.Parse(nil, "spread.km", input)
		if len(parsed.Diagnostics) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", input, parsed.Diagnostics)
		} else if got := expr.Format(nil, parsed.Expr); got != input {
			t.Errorf("formatted %q, want %q", got, input)
		}
		parsed.Free()
	}
	r := expr.Parse(nil, "spread.km", "f ...items")
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatal(r.Diagnostics)
	}
	spread := r.Expr.Items[1]
	if spread.Kind != expr.ListSpread || spread.Span.Start != 2 || spread.Items[0].Span.Start != 5 {
		t.Fatalf("argument spread lost authored spans: %+v", spread)
	}
}

func TestInfixOperatorTokenBoundaries(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"1+2*3", "1 + 2 * 3"},
		{"(1+2)", "(1 + 2)"},
		{"a!=b", "a != b"},
		{"a??b", "a ?? b"},
		{"1e+2+3", "1e+2 + 3"},
		{"0xe+2", "0xe + 2"},
		{"a /2", "a /2"},
		{"a -2", "a -2"},
		{"[42]|>first", "[42] |> first"},
	} {
		r := expr.Parse(nil, "operators.km", tc.input)
		if len(r.Diagnostics) != 0 {
			t.Errorf("%s: unexpected diagnostics: %v", tc.input, r.Diagnostics)
		} else if got := expr.Format(nil, r.Expr); got != tc.want {
			t.Errorf("%s: formatted %q, want %q", tc.input, got, tc.want)
		}
		r.Free()
	}
	for _, input := range []string{"4/2", "a/2", "a/ 2", "1-2", "1 -2 == 0 == 1"} {
		r := expr.Parse(nil, "operators.km", input)
		if len(r.Diagnostics) == 0 {
			t.Errorf("%s: accepted invalid operator syntax", input)
		}
		r.Free()
	}
}

func TestListItemsUseFullValueExpressions(t *testing.T) {
	input := "[1 + 2, f x, if true then y else z, ...make-list x,]"
	r := expr.Parse(nil, "collections.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 || r.Expr == nil || len(r.Expr.Items) != 4 {
		t.Fatalf("list did not parse its full items: %v", r.Diagnostics)
	}
	items := r.Expr.Items
	if items[0].Infix != "+" || items[1].Kind != expr.Application ||
		items[2].Kind != expr.Conditional || items[3].Kind != expr.ListSpread ||
		items[3].Items[0].Kind != expr.Application {
		t.Fatalf("unexpected list item structure: %+v", items)
	}
	if formatted := expr.Format(nil, r.Expr); formatted != strings.TrimSuffix(input, ",]")+"]" {
		t.Errorf("unexpected compact list: %q", formatted)
	}
	one := expr.Parse(nil, "collections.km", "[f x]")
	defer one.Free()
	if len(one.Diagnostics) != 0 || len(one.Expr.Items) != 1 || one.Expr.Items[0].Kind != expr.Application {
		t.Errorf("application inside a list did not remain one item: %v", one.Diagnostics)
	}
	for _, input := range []string{"[key: 1]", "[{key: 1}{other: 2},]", "[...{x: 1}]", "[1\n, ,]"} {
		bad := expr.Parse(nil, "collections.km", input)
		if len(bad.Diagnostics) == 0 {
			t.Errorf("%s: missing PARSE_ERR", input)
		}
		bad.Free()
	}
}

func TestInterpolationDelimiterContinuesValueExpression(t *testing.T) {
	input := "\"hello ${format-name\n first-name\n last-name}\""
	r := expr.Parse(nil, "interpolation.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 || len(r.Expr.Parts) != 2 {
		t.Fatalf("interpolation did not retain its multiline value: %v", r.Diagnostics)
	}
	call := r.Expr.Parts[1].Expr
	if call.Kind != expr.Application || len(call.Items) != 3 {
		t.Fatalf("interpolation split its application: %+v", call)
	}
	if got := expr.Format(nil, r.Expr); got != `"hello ${format-name first-name last-name}"` {
		t.Errorf("unexpected formatted interpolation: %q", got)
	}
}

func TestCollectionNewlineSeparators(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{"[f x\n  y, z\n]", "[f x, y, z]"},
		{"[1 # first\r\n\r\n2\r\n]", "[1, # first\n\n2]"},
		{"{name: f x\nactive: true\nlabel\n...extra}", "{name: f x, active: true, label, ...extra}"},
		{"[f \\\n  x\n(g\n y)\n1 +\n2\n3\n  + 4]", "[f x, (g y), 1 + 2, 3 + 4]"},
		{"{items:\n  [1\n   2]\nlabel: if check\n  then yes\n  else no\nextra}", "{items: [1, 2], label: if check then yes else no, extra}"},
		{"[{x: 1\ny: 2}\n[3\n4]]", "[{x: 1, y: 2}, [3, 4]]"},
	} {
		r := expr.Parse(nil, "collections.km", tc.source)
		if len(r.Diagnostics) != 0 || r.Expr == nil {
			t.Errorf("%q: %v", tc.source, r.Diagnostics)
		} else if got := expr.Compact(nil, r.Expr); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.source, got, tc.want)
		}
		r.Free()
	}
	for _, input := range []string{`{a: 1 b: 2}`, `{a b}`, "[1\n,, 2]", "{a\n,, b}"} {
		r := expr.Parse(nil, "collections.km", input)
		if len(r.Diagnostics) == 0 {
			t.Errorf("%q: expected a parse error", input)
		}
		r.Free()
	}
}

func TestCollectionLayoutWrapsDelimitedApplications(t *testing.T) {
	long := strings.Repeat("x", 67)
	input := "[call " + long + " 2, {key: " + long + ", short: 1}]"
	r := expr.Parse(nil, "collections.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatal(r.Diagnostics)
	}
	got := expr.Format(nil, r.Expr)
	defer mem.FreeString(nil, got)
	want := "[\n\tcall \\\n\t\t" + long + " \\\n\t\t2\n\t{\n\t\tkey:\n\t\t\t" + long + ",\n\t\tshort: 1,\n\t}\n]"
	if want != got {
		t.Fatalf("layout = %q, want %q", got, want)
	}
	reparsed := expr.Parse(nil, "formatted.km", got)
	defer reparsed.Free()
	if len(reparsed.Diagnostics) != 0 || reparsed.Expr.Items[0].Kind != expr.Application {
		t.Fatalf("expanded application changed the list: %v", reparsed.Diagnostics)
	}
	again := expr.Format(nil, reparsed.Expr)
	defer mem.FreeString(nil, again)
	if got != again {
		t.Fatalf("formatter changed expanded source on second pass: %q", again)
	}
}

func TestParenthesizedAtomRemainsAValue(t *testing.T) {
	for _, input := range []string{"(name)", "(1)", "([1, 2])", "((name))", "({name: 1})"} {
		r := expr.Parse(nil, "grouping.km", input)
		if len(r.Diagnostics) != 0 || r.Expr == nil || r.Expr.Kind != expr.Group ||
			len(r.Expr.Items) != 1 || r.Expr.Items[0].Kind == expr.Application {
			t.Errorf("%q: expected an ordinary group, got %+v %v", input, r.Expr, r.Diagnostics)
		} else if got := expr.Format(nil, r.Expr); got != input {
			t.Errorf("%q: formatted as %q", input, got)
		}
		r.Free()
	}
	r := expr.Parse(nil, "grouping.km", "([name] name)")
	defer r.Free()
	if len(r.Diagnostics) != 0 || expr.Ungroup(r.Expr).Kind != expr.Application {
		t.Fatal("list-headed expression did not remain an application")
	}
}

func TestArrowLambdaLayoutPreservesGroup(t *testing.T) {
	word := strings.Repeat("x", 72)
	input := "(name => join name " + word + ")"
	r := expr.Parse(nil, "lambda.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatal(r.Diagnostics)
	}
	formatted := expr.Format(nil, r.Expr)
	want := "(name =>\n\tjoin\n\t\tname\n\t\t" + word + "\n)"
	if formatted != want {
		t.Fatalf("formatted %q, want %q", formatted, want)
	}
	again := expr.Parse(nil, "lambda.km", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || again.Expr.Kind != expr.Group ||
		expr.Format(nil, again.Expr) != formatted {
		t.Errorf("expanded lambda did not round-trip: %v", again.Diagnostics)
	}
}

func TestExpandedInfixAndSectionLayout(t *testing.T) {
	word := strings.Repeat("name", 18)
	for _, tc := range []struct{ input, want string }{
		{word + " and a == b and c > d", word + "\n\tand a == b\n\tand c > d"},
		{word + " + a * b - c", word + "\n\t+ a * b\n\t- c"},
		{word + " ?? f value ?? (g 1)", word + "\n\t?? f value\n\t?? (g 1)"},
		{"(join __ " + word + " _)", "(\n\tjoin\n\t\t__\n\t\t" + word + "\n\t\t_\n)"},
	} {
		parsed := expr.Parse(nil, "layout.km", tc.input)
		if len(parsed.Diagnostics) != 0 {
			t.Fatalf("parse %q: %v", tc.input, parsed.Diagnostics)
		}
		formatted := expr.Format(nil, parsed.Expr)
		if formatted != tc.want {
			t.Fatalf("formatted %q, want %q", formatted, tc.want)
		}
		reparsed := expr.Parse(nil, "layout.km", formatted)
		if len(reparsed.Diagnostics) != 0 {
			t.Fatalf("reparse %q: %v", formatted, reparsed.Diagnostics)
		}
		before, after := expr.Compact(nil, parsed.Expr), expr.Compact(nil, reparsed.Expr)
		again := expr.Format(nil, reparsed.Expr)
		if before != after || again != formatted || parsed.Expr.Kind != reparsed.Expr.Kind {
			t.Errorf("expanded layout changed expression structure or failed to round-trip: %q", formatted)
		}
		mem.FreeString(nil, formatted)
		mem.FreeString(nil, before)
		mem.FreeString(nil, after)
		mem.FreeString(nil, again)
		parsed.Free()
		reparsed.Free()
	}
}

func TestUndelimitedApplicationExpandsFromTheRight(t *testing.T) {
	word := strings.Repeat("x", 65)
	input := "call " + word + " (left) [a, b] end"
	parsed := expr.Parse(nil, "layout.km", input)
	defer parsed.Free()
	if len(parsed.Diagnostics) != 0 {
		t.Fatal(parsed.Diagnostics)
	}
	formatted := expr.Format(nil, parsed.Expr)
	defer mem.FreeString(nil, formatted)
	want := "call " + word + " (left) [\n\ta\n\tb\n] end"
	if formatted != want {
		t.Fatalf("formatted %q, want %q", formatted, want)
	}
	reparsed := expr.Parse(nil, "layout.km", formatted)
	defer reparsed.Free()
	if len(reparsed.Diagnostics) != 0 {
		t.Fatal(reparsed.Diagnostics)
	}
	before, after := expr.Compact(nil, parsed.Expr), expr.Compact(nil, reparsed.Expr)
	defer mem.FreeString(nil, before)
	defer mem.FreeString(nil, after)
	again := expr.Format(nil, reparsed.Expr)
	defer mem.FreeString(nil, again)
	if before != after || again != formatted {
		t.Fatalf("expanded application changed structure or failed to round-trip: %q", formatted)
	}
}

func TestExpressionFormattingKeepsInternalTrivia(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"[1, # first  \r\n\r\n # second\r\n  2]", "[\n\t1 # first\n\n\t# second\n\t2\n]"},
		{"[1,\n  \n  2]", "[\n\t1\n\n\t2\n]"},
		{"[\"literal # text\", 1 # last\n]", "[\n\t\"literal # text\"\n\t1 # last\n]"},
		{"a # decision\n  and b", "a # decision\n\tand b"},
		{"match value\n  1 => red # one\n  # remaining\n  else => blue", "match value\n\t1    => red # one\n\t# remaining\n\telse => blue"},
	} {
		parsed := expr.Parse(nil, "comments.km", tc.input)
		if len(parsed.Diagnostics) != 0 {
			t.Fatalf("parse %q: %v", tc.input, parsed.Diagnostics)
		}
		cloned := expr.Clone(nil, parsed.Expr)
		parsed.Free()
		formatted := expr.Format(nil, cloned)
		expr.Free(nil, cloned)
		if formatted != tc.want {
			t.Errorf("formatted %q, want %q", formatted, tc.want)
		}
		again := expr.Parse(nil, "comments.km", formatted)
		twice := expr.Format(nil, again.Expr)
		if len(again.Diagnostics) != 0 || twice != formatted {
			t.Errorf("reformat %q as %q: %v", formatted, twice, again.Diagnostics)
		}
		mem.FreeString(nil, formatted)
		mem.FreeString(nil, twice)
		again.Free()
	}
}

func TestProcessPipeIsRejectedInValueGroups(t *testing.T) {
	value := expr.Parse(nil, "pipe.km", "(input | first)")
	defer value.Free()
	if len(value.Diagnostics) == 0 || value.Diagnostics[0].Code != "PARSE_ERR" {
		t.Fatal("a process pipe parsed in value context")
	}
	action := expr.Parse(nil, "pipe.km", "$(input | first)")
	defer action.Free()
	if len(action.Diagnostics) != 0 || action.Expr.Kind != expr.CommandCapture {
		t.Errorf("a process pipe failed in action context: %v", action.Diagnostics)
	}
}

func TestGroupedConditionalUsesValueGrammar(t *testing.T) {
	const input = "apply (if test x then yes x elif other then 2 else 3) []"
	r := expr.Parse(nil, "condition.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatal(r.Diagnostics)
	}
	operand := r.Expr.Items[1]
	if operand.Kind != expr.Group || operand.Items[0].Kind != expr.Conditional || len(operand.Items[0].Items) != 5 {
		t.Fatal("grouped conditional lost its branches")
	}
	if formatted := expr.Format(nil, r.Expr); formatted != input {
		t.Errorf("format changed the conditional group: %q", formatted)
	}
}

func TestGroupedConditionalLayout(t *testing.T) {
	condition := strings.Repeat("x", 50)
	input := "(if " + condition + " then first elif second then 2 else 3)"
	r := expr.Parse(nil, "condition.km", input)
	defer r.Free()
	if len(r.Diagnostics) != 0 {
		t.Fatal(r.Diagnostics)
	}
	formatted := expr.Format(nil, r.Expr)
	want := "(if " + condition + " then first\n\telif second then 2\n\telse 3\n)"
	if formatted != want {
		t.Fatalf("formatted %q, want %q", formatted, want)
	}
	again := expr.Parse(nil, "condition.km", formatted)
	defer again.Free()
	if len(again.Diagnostics) != 0 || expr.Format(nil, again.Expr) != formatted {
		t.Errorf("conditional layout did not round-trip: %v", again.Diagnostics)
	}
}
