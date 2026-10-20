package program_test

import (
	"kame/host/posix"
	"kame/lang/eval"
	"kame/lang/script"
	"kame/program"
	"solod.dev/so/mem"
	"solod.dev/so/os"
	"solod.dev/so/strings"
	"solod.dev/so/testing"
)

func checkPrecedencePlan(t *testing.T, source, target, input, code string) {
	checkPrecedencePlanAt(t, source, target, input, code, ".")
}

func checkPrecedencePlanAt(t *testing.T, source, target, input, code, directory string) {
	a := t.Allocator()
	parsed := script.Parse(a, "precedence.kmk", source)
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	compiled := program.Compile(a, parsed, registry, program.Options{Directory: directory})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Errorf("compile failed for %s", target)
		return
	}
	defer compiled.Program.Free()
	planned := compiled.Program.Plan(target)
	defer planned.Plan.Free(a)
	defer planned.Diagnostic.Free(a)
	if planned.Diagnostic.Code != code {
		t.Errorf("%s: diagnostic %s, want %s", target, planned.Diagnostic.Code, code)
		return
	}
	if code == "" && (len(planned.Plan.Inputs) != 1 || planned.Plan.Inputs[0] != input) {
		t.Errorf("%s: selected wrong prerequisite, want %s", target, input)
	}
}

type precedenceExample struct {
	Left   string
	Right  string
	Target string
	Input  string
	Code   string
}

func TestRulePrecedenceSelection(t *testing.T) {
	cases := []precedenceExample{
		{Left: "build-{p} : ./one\n", Right: "build-xml-{p} : ./two\n", Target: "build-xml-feed", Code: "TGT_AMBIG"},
		{Left: "./www/{path:**}.html : ./html/{path}.html\n", Right: "./www/xml/{path:**}.html : ./xml/{path}.xml\n", Target: "www/xml/feed.html", Input: "./xml/feed.xml"},
		{Left: "./www/{path:**}.html : ./html/{path}.html\n", Right: "./www/xml/{path:**}.html : ./xml/{path}.xml\n", Target: "www/index.html", Input: "./html/index.html"},
		{Left: "./out/{p}.js : ./normal/{p}\n", Right: "./out/{p}.min.js : ./min/{p}\n", Target: "out/app.min.js", Input: "./min/app"},
		{Left: "./out/{p:**}.js : ./recursive/{p}\n", Right: "./out/{p}.js : ./segment/{p}\n", Target: "out/app.js", Input: "./segment/app"},
		{Left: "./out/{p:**}.js : ./recursive/{p}\n", Right: "./out/{p}.js : ./segment/{p}\n", Target: "out/sub/app.js", Input: "./recursive/sub/app"},
		{Left: "./out/{p}.js : ./one\n", Right: "./out/{q:*}.js : ./two\n", Target: "out/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/{**}.js : ./one\n", Right: "./out/{q:**}.js : ./two\n", Target: "out/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/xml/{p:**} : ./one\n", Right: "./out/{q:**}.html : ./two\n", Target: "out/xml/feed.html", Code: "TGT_AMBIG"},
		{Left: "./out/{p:~.*}.js : ./one\n", Right: "./out/{q}.js : ./two\n", Target: "out/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/{p:a*}.js : ./one\n", Right: "./out/{q}.js : ./two\n", Target: "out/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/{p:**}/{q}.js : ./one\n", Right: "./out/xml/{p}.js : ./two\n", Target: "out/xml/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/{p}.js : ./one\n", Right: "./out/{q:**}.min.js : ./two\n", Target: "out/app.min.js", Code: "TGT_AMBIG"},
		{Left: "./out/{p:**}.js ./out/xml/{p:**}.js : ./one\n", Target: "out/xml/app.js", Code: "TGT_AMBIG"},
		{Left: "./out/{p:**}.js ./maps/{p:**}.map : ./source/{p}\n", Target: "maps/app.map", Input: "./source/app"},
		{Left: "./out/{p:**}.js ./out/{p:**}.map : ./one\n", Right: "./out/xml/{p:**}.js : ./two\n", Target: "out/xml/app.map", Code: "TGT_AMBIG"},
		{Left: "./out/{p:**}.js ./out/{p:**}.map : ./one\n", Right: "./out/xml/{p:**}.js : ./two\n", Target: "out/xml/app.js", Input: "./two"},
		{Left: "./out/{p:**}.js ./out/{p:**}.map : ./one\n", Right: "./out/xml/app.js : ./two\n", Target: "out/xml/app.map", Code: "TGT_AMBIG"},
	}
	for i := range cases {
		c := cases[i]
		checkPrecedencePlan(t, c.Left+c.Right, c.Target, c.Input, c.Code)
		checkPrecedencePlan(t, c.Right+c.Left, c.Target, c.Input, c.Code)
	}
}

func checkPrecedenceGroupExecution(t *testing.T, specialFirst bool) {
	a := t.Allocator()
	buffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(buffer, "", "kame-precedence-group-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	parsed := script.Parse(a, "precedence.kmk", "./out/{p:**}.html ./maps/{p:**}.map :\n\tprintf forbidden > ran-broad\n./out/xml/{p:**}.html :\n\tprintf special > @>\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("compile failed")
		return
	}
	defer compiled.Program.Free()
	requests := []string{"maps/xml/feed.map", "out/xml/feed.html"}
	if specialFirst {
		requests[0], requests[1] = "out/xml/feed.html", "maps/xml/feed.map"
	}
	for i := range requests {
		result := compiled.Program.Materialize(requests[i])
		want := ""
		if requests[i] == "maps/xml/feed.map" {
			want = "TGT_AMBIG"
		}
		if result.Diagnostic.Code != want {
			t.Errorf("group request %s: got %s, want %s", requests[i], result.Diagnostic.Code, want)
		}
		result.Free(a)
	}
	marker, markerErr := os.ReadFile(a, dir+"/ran-broad")
	if markerErr == nil {
		t.Error("shadowed grouped recipe executed")
	}
	mem.FreeSlice(a, marker)
	data, readErr := os.ReadFile(a, dir+"/out/xml/feed.html")
	if readErr != nil || string(data) != "special" {
		t.Error("group overwrote specialized output")
	}
	mem.FreeSlice(a, data)
}

func TestRulePrecedenceGroupedOwnership(t *testing.T) {
	checkPrecedenceGroupExecution(t, false)
	checkPrecedenceGroupExecution(t, true)
}

func TestRulePrecedenceGroupedConflictLocatesMatchedOutput(t *testing.T) {
	a := t.Allocator()
	competitors := []string{"./other/{p:**}.js ./out/xml/{p:**}.js : ./special\n", "./other/app.js ./out/xml/app.js : ./special\n"}
	matched := []string{"./out/xml/{p:**}.js", "./out/xml/app.js"}
	broad := "./out/{p:**}.js ./maps/{p:**}.map : ./general\n"
	for i := range competitors {
		text := broad + competitors[i]
		parsed := script.Parse(a, "precedence.kmk", text)
		registry := eval.NewRegistry(a)
		compiled := program.Compile(a, parsed, registry, program.Options{Directory: "."})
		if compiled.Program == nil {
			t.Error("grouped conflict fixture did not compile")
		} else {
			planned := compiled.Program.Plan("maps/xml/app.map")
			d := planned.Diagnostic
			start := strings.Index(text, matched[i])
			if d.Code != "TGT_AMBIG" || d.Source != "precedence.kmk" || d.Span.Start != start || d.Span.End != start+len(matched[i]) {
				t.Error("grouped conflict did not locate the competing rule's matched output")
			}
			if len(d.Related) != 1 {
				t.Error("grouped conflict lost the original producer location")
			} else if d.Related[0].Source != "precedence.kmk" || d.Related[0].Span.Start != 0 || d.Related[0].Span.End != len("./out/{p:**}.js") {
				t.Error("grouped conflict located the wrong original output")
			}
			planned.Plan.Free(a)
			planned.Diagnostic.Free(a)
			compiled.Program.Free()
		}
		compiled.Free(a)
		registry.Free()
		parsed.Free()
	}
}

func TestRulePrecedenceCollectsAllMatches(t *testing.T) {
	rules := []string{
		"./www/{p:**}.html : ./general\n",
		"./www/xml/{p:**} : ./xml\n",
		"./www/xml/{p:**}.html : ./special/{p}\n",
	}
	chain := []string{
		"./www/{p:**}.html : ./general\n",
		"./www/xml/{p:**}.html : ./xml\n",
		"./www/xml/news/{p:**}.html : ./special/{p}\n",
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i == j {
				continue
			}
			k := 3 - i - j
			checkPrecedencePlan(t, rules[i]+rules[j]+rules[k], "www/xml/feed.html", "./special/feed", "")
			checkPrecedencePlan(t, rules[i]+rules[j]+rules[k]+"./www/xml/feed.html : ./literal\n", "www/xml/feed.html", "./literal", "")
			checkPrecedencePlan(t, chain[i]+chain[j]+chain[k], "www/xml/news/feed.html", "./special/feed", "")
		}
	}
}

func TestRulePrecedenceExecutionDoesNotFallback(t *testing.T) {
	a := t.Allocator()
	buffer := make([]byte, os.MaxPathLen)
	dir, err := os.MkdirTemp(buffer, "", "kame-precedence-")
	if err != nil {
		t.Fatal("temporary directory failed")
		return
	}
	defer os.Remove(dir)
	broad := "./out/{p:**}.html : ./broad\n"
	special := "ROOT = \"" + dir + "\"\n@{ROOT}/out/xml/{p:**}.html : ./special\n"
	checkPrecedencePlanAt(t, broad+special, dir+"/out/xml/feed.html", "./special", "", dir)
	checkPrecedencePlanAt(t, special+broad, dir+"/out/xml/feed.html", "./special", "", dir)
	if os.WriteFile(dir+"/broad", []byte("broad"), 0o644) != nil {
		t.Fatal("broad source write failed")
		return
	}
	parsed := script.Parse(a, "precedence.kmk", "./out/{p:**}.html : ./broad\n\tcp @< @>\n./out/xml/{p:**}.html : ./special\n\tcp @< @>\n")
	defer parsed.Free()
	registry := eval.NewRegistry(a)
	defer registry.Free()
	compiled := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	defer compiled.Free(a)
	if compiled.Program == nil {
		t.Fatal("compile failed")
		return
	}
	defer compiled.Program.Free()
	result := compiled.Program.Materialize("out/xml/feed.html")
	if result.Diagnostic.Code == "" {
		t.Error("missing specialized source fell back to broad producer")
	}
	result.Free(a)
	if os.WriteFile(dir+"/special", []byte("special"), 0o644) != nil {
		t.Fatal("special source write failed")
		return
	}
	// A new session must select the same producer once its prerequisite exists.
	second := program.Compile(a, parsed, registry, program.Options{Host: posix.New(a), Directory: dir})
	defer second.Free(a)
	if second.Program == nil {
		t.Fatal("second compile failed")
		return
	}
	defer second.Program.Free()
	expanded := second.Program.ExpandPlan("out/xml/feed.html")
	if expanded.Diagnostic.Code != "" || len(expanded.Plan.Inputs) != 1 || expanded.Plan.Inputs[0] != "./special" {
		t.Error("inspection selected a different producer")
	}
	expanded.Plan.Free(a)
	expanded.Diagnostic.Free(a)
	built := second.Program.Materialize("out/xml/feed.html")
	if built.Diagnostic.Code != "" {
		t.Errorf("specialized execution failed: %s", built.Diagnostic.Code)
	}
	built.Free(a)
	data, readErr := os.ReadFile(a, dir+"/out/xml/feed.html")
	if readErr != nil || string(data) != "special" {
		t.Error("wrong producer executed")
	}
	mem.FreeSlice(a, data)
}
