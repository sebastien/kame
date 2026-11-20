package scriptcheck

import (
	"bytes"
	"kame/lang/ast"
	"kame/lang/script"
	"kame/lang/source"
	"strings"
	"testing"
)

func TestModuleDeclarationsRoundTrip(t *testing.T) {
	const text = "import   ./lib/a\\ b as words # module\nfrom ~/lib/common import first as one, \\\n\tsecond\nexport one,second\n"
	const want = "import ./lib/a\\ b as words # module\nfrom ~/lib/common import first as one, second\nexport one, second\n"
	authored := source.New(nil, "modules.km", text)
	defer authored.Free(nil)
	for _, language := range []string{"km", "kash"} {
		t.Run(language, func(t *testing.T) {
			parsed := script.ParseFragment(nil, authored, language, 0, len(text))
			defer parsed.Free()
			if len(parsed.Diagnostics) != 0 || len(parsed.Items) != 3 {
				t.Fatalf("module declarations failed to parse: %v", parsed.Diagnostics)
			}
			module, from, exports := parsed.Items[0].Module, parsed.Items[1].Module, parsed.Items[2].Module
			if module.Path != "./lib/a b" || module.Alias != "words" ||
				len(from.Names) != 2 || from.Names[0].Name != "first" || from.Names[0].Alias != "one" || from.Names[1].Name != "second" ||
				len(exports.Names) != 2 || exports.Names[0].Name != "one" {
				t.Fatalf("incorrect module declaration AST: %+v %+v %+v", module, from, exports)
			}
			if formatted := script.Format(nil, parsed); formatted != want {
				t.Errorf("module declaration formatting = %q", formatted)
			}
			var json bytes.Buffer
			if status := ast.WriteAST(&json, language, authored.Name, want); status != 0 ||
				!strings.Contains(json.String(), "\"kind\":\"from\"") ||
				!strings.Contains(json.String(), "\"path\":\"./lib/a b\"") ||
				!strings.Contains(json.String(), "\"name\":\"first\"") ||
				!strings.Contains(json.String(), "\"alias\":\"one\"") {
				t.Errorf("public AST lost module fields: %s", json.String())
			}
		})
	}
}

func TestModuleDeclarationOperandsAndPlacement(t *testing.T) {
	bad := []string{
		"import \"./lib\"",
		"import ./lib/${name}",
		"import ./lib/*.km",
		"import ./lib extra",
		"import ./lib as",
		"from ./lib first",
		"from ./lib import first,",
		"export first second",
		"export first as one",
		"if true\n\timport ./lib\n",
	}
	for _, language := range []string{"km", "kash"} {
		for _, text := range bad {
			authored := source.New(nil, "invalid.km", text)
			parsed := script.ParseFragment(nil, authored, language, 0, len(text))
			if len(parsed.Diagnostics) == 0 || parsed.Diagnostics[0].Code != "PARSE_ERR" {
				t.Errorf("%s accepted invalid module declaration %q: %v", language, text, parsed.Diagnostics)
			}
			parsed.Free()
			authored.Free(nil)
		}
	}
}

func TestModuleNamespacesRequireValidUnreservedAliases(t *testing.T) {
	tests := []struct {
		text string
		code string
	}{
		{"import ./lib/x", "DEF_INVALID"},
		{"import ./lib/env.kame", "DEF_INVALID"},
		{"import ./lib/std.kash", "DEF_INVALID"},
		{"import ./lib/module.ksh", "DEF_INVALID"},
		{"import ./lib/words as std", "DEF_INVALID"},
		{"import ./lib/two\\ words.km", "DEF_INVALID"},
		{"import ./lib/page.kmt as page", "RES_INVALID"},
		{"from ./lib/page.kmt import thing", "RES_INVALID"},
		{"import ./lib/two\\ words.km as words", ""},
		{"import ./lib/x.kash as external-tools", ""},
		{"from ./lib/values import thing as std", ""},
		{"import ./lib/archive.kash", ""},
	}
	for _, language := range []string{"km", "kash"} {
		for _, test := range tests {
			authored := source.New(nil, "modules.km", test.text)
			parsed := script.ParseFragment(nil, authored, language, 0, len(test.text))
			got := ""
			if len(parsed.Diagnostics) != 0 {
				got = parsed.Diagnostics[0].Code
			}
			if got != test.code {
				t.Errorf("%s %q produced %q, want %q", language, test.text, got, test.code)
			}
			parsed.Free()
			authored.Free(nil)
		}
	}
}
