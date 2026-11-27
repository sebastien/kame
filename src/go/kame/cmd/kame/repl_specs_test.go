package main

import (
	"bytes"
	"compress/gzip"
	"kame/lang/usage"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCommandWords(t *testing.T) {
	for _, tc := range []struct {
		text  string
		words []string
		last  string
		ok    bool
	}{
		{"git commit -m", []string{"git", "commit"}, "-m", true},
		{"git commit ", []string{"git", "commit"}, "", true},
		{"ls | grep --co", []string{"grep"}, "--co", true},
		{"a; b && c -", []string{"c"}, "-", true},
		{"echo $(git sta", []string{"git"}, "sta", true},
		{"echo $(git status) -", []string{"echo", "$(git status)"}, "-", true},
		{"echo (count x", nil, "", false},
		{"if test -f", []string{"test"}, "-f", true},
		{`tar -C my\ dir -x`, []string{"tar", "-C", "my dir"}, "-x", true},
		{`grep "a b" --c`, []string{"grep", "a b"}, "--c", true},
		{`grep "a\tb" café é\* --c`, []string{"grep", "a\tb", "café", "é*"}, "--c", true},
		{`grep a\\ --c`, []string{"grep", `a\`}, "--c", true},
		{`grep "--co`, nil, "", false},
		{"sort < in.txt -", []string{"sort"}, "-", true},
		{"sort > out", nil, "", false},
		{"sort >out", nil, "", false},
		{"sort 2>out", nil, "", false},
		{"ls 2>&1 --co", []string{"ls"}, "--co", true},
		{"ls &>log -", []string{"ls"}, "-", true},
		{"ls &>lo", nil, "", false},
		{"a & b -", []string{"b"}, "-", true},
	} {
		words, last, ok := commandWords(tc.text)
		var texts []string
		for _, w := range words {
			texts = append(texts, w.text)
		}
		if ok != tc.ok || ok && (!slices.Equal(texts, tc.words) || last.text != tc.last) {
			t.Errorf("%q: got %q %q %v", tc.text, texts, last.text, ok)
		}
	}
}

const kashtoolSpec = `bin "kashtool"
flag "-f --format <format>" help="Output format" {
    choices "json" "text"
}
flag "--out <file>"
cmd "serve" help="Serve the site" {
    flag "--port <port>"
}
`

// specFixture is a completion fixture whose kashtool has a cached spec. The
// spec counts as refreshed, so completion never starts a build.
func specFixture(t *testing.T) completionContext {
	t.Helper()
	ctx := completionFixture(t)
	root := t.TempDir()
	ctx.Specs = specStore(root)
	ctx.Specs.refreshed["kashtool"], ctx.Specs.refreshed["kashtest"] = time.Now(), time.Now()
	specWrite(t, filepath.Join(root, "specs", "kashtool.usage.kdl"), kashtoolSpec)
	ctx.Aliases = map[string][]string{"kt": {"kashtool", "--format", "json"}}
	return ctx
}

func TestCommandSpecCompletions(t *testing.T) {
	ctx := specFixture(t)
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"kashtool --f", []string{"--format"}},
		{"kashtool -", []string{"--format", "--out", "-f"}},
		{"kashtool --format ", []string{"json", "text"}},
		{"kashtool --format=j", []string{"json"}},
		{"kashtool ", []string{"serve"}},
		{"kashtool serve --p", []string{"--port"}},
		{"kashtool --out re", []string{"readme.md", "release.txt"}},
		{"kt s", []string{"serve"}},
		{"echo $(kashtool --fo", []string{"--format"}},
		{"kashtool > re", []string{"readme.md", "release.txt"}},
		{`kashtool "--fo`, nil},
		{"kashtest --x", nil}, // no spec: paths
		{"cd sr", []string{"src/"}},
	} {
		_, candidates := completionsAt(tc.line, len(tc.line), ctx)
		var texts []string
		for _, c := range candidates {
			texts = append(texts, c.Text)
		}
		if !slices.Equal(texts, tc.want) {
			t.Errorf("%q: got %q, want %q", tc.line, texts, tc.want)
		}
	}
	_, candidates := completionsAt("kashtool --format=", 18, ctx)
	if len(candidates) != 2 || candidates[0].Display != "json" || candidates[0].Tag != tagValues {
		t.Errorf("attached values: %+v", candidates)
	}
	_, candidates = completionsAt("kashtool ser", 12, ctx)
	if len(candidates) != 1 || candidates[0].Description != "Serve the site" || candidates[0].Tag != tagCommand {
		t.Errorf("subcommand: %+v", candidates)
	}
}

func TestOptionHint(t *testing.T) {
	ctx := specFixture(t)
	ctx.Aliases["format"] = []string{"kashtool", "--format"}
	const format = "-f, --format <format> — Output format · json, text"
	for _, tc := range []struct {
		line, want string
	}{
		{"kashtool --", "--format  --out"},
		{"kashtool --fo", format},
		{"kashtool -f", format},
		{"kashtool --format=j", format},
		{"kashtool --format j", format},
		{"kashtool --format j ", ""},
		{"kashtool -- --fo", ""},
		{"kashtool serve --p", "--port <port>"},
		{"kashtool serve --fo", ""},
		{"kt --fo", format},
		{"format j", format},
		{"echo $(kashtool --fo", format},
		{"echo | kashtool --fo", format},
		{"kashtool > --fo", ""},
		{"kashtool # --fo", ""},
		{`kashtool "--fo`, ""},
		{"kashtest --f", ""},
		{"history --g", "--global — show global history"},
		{"option --l", "--local — option scope"},
	} {
		if got := optionHint(tc.line, ctx); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestCompleteBuiltin(t *testing.T) {
	ctx := specFixture(t)
	var out, errOut bytes.Buffer
	if status := ctx.Specs.command([]string{"--show", "kashtool"}, ctx, &out, &errOut); status != 0 || !strings.Contains(out.String(), `cmd "serve"`) || !strings.HasPrefix(out.String(), "// "+ctx.Specs.root) {
		t.Fatalf("show: %d %q %q", status, out.String(), errOut.String())
	}
	for _, args := range [][]string{nil, {"--show"}, {"--show", "a", "b"}, {"--show", "../x"}, {"--refresh"}, {"--bogus", "x"}} {
		errOut.Reset()
		if status := ctx.Specs.command(args, ctx, &out, &errOut); status != 2 || errOut.Len() == 0 {
			t.Errorf("%q: status %d %q", args, status, errOut.String())
		}
	}
	if status := ctx.Specs.command([]string{"--show", "nothing"}, ctx, &out, &errOut); status != 1 {
		t.Errorf("missing spec: status %d", status)
	}
	var none *completionSpecs
	if status := none.command([]string{"--show", "x"}, ctx, &out, &errOut); status != 1 {
		t.Errorf("no cache: status %d", status)
	}
}

func TestKamePathLiteral(t *testing.T) {
	if got := kamePathLiteral("/a b/c{d}*.km"); got != `/a\ b/c\{d\}\*.km` {
		t.Fatal(got)
	}
}

// The embedded program merges a Usage file, a user rule, a fish file and
// man pages, and rebuilds when a page changes.
func TestCompletionProgram(t *testing.T) {
	binary := nativeCLIBinary(t)
	dir := t.TempDir()
	c := specStore(filepath.Join(dir, "cache"))
	c.usageDirs, c.fishDirs, c.manDirs = []string{filepath.Join(dir, "usage")}, []string{filepath.Join(dir, "fish")}, []string{filepath.Join(dir, "man")}
	c.userRules, c.executable = filepath.Join(dir, "config dir", "completions.km"), binary
	page := func(name, text string) {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		z.Write([]byte(text))
		z.Close()
		specWrite(t, filepath.Join(dir, "man", "man1", name), b.String())
	}
	page("demo.1.gz", ".SH NAME\ndemo \\- a demo\n.SH OPTIONS\n.TP\n\\fB\\-a\\fR, \\fB\\-\\-all\\fR\nshow all\n.TP\n\\fB\\-\\-format\\fR=\\fIF\\fR\nman format\n")
	page("demo-run.1.gz", ".SH SYNOPSIS\n.B demo run\n[\\fIOPTION\\fR]\n.SH OPTIONS\n.TP\n\\fB\\-\\-fast\\fR\nrun fast\n")
	page("demo-tool.1.gz", ".SH SYNOPSIS\n.B demo-tool\n.SH OPTIONS\n.TP\n\\fB\\-\\-tool\\fR\nnot a subcommand\n")
	specWrite(t, filepath.Join(dir, "man", "man1", "alias.1"), ".so man1/demo.1\n")
	specWrite(t, filepath.Join(dir, "usage", "demo.usage.kdl"), "flag \"--format <f>\" help=\"usage format\" { choices \"a\" \"b\"; }\n")
	specWrite(t, filepath.Join(dir, "fish", "demo.fish"), "complete -c demo -l fish -d 'from fish'\ncomplete -c demo -l dyn -a '(ls)'\n")
	specWrite(t, c.userRules, "rule ./user/demo.usage.kdl =\n\t\"flag \\\"--user\\\" help=\\\"from a rule\\\"\\n\"\n")

	var log bytes.Buffer
	if status := c.build([]string{"demo", "alias", "missing"}, &log, time.Minute); status != 0 {
		t.Fatalf("build status %d: %s", status, log.String())
	}
	spec, found, cached := c.lookup("demo")
	if !found || !cached {
		t.Fatal("demo spec not cached")
	}
	help := map[string]string{}
	for _, f := range spec.Flags {
		help[strings.Join(f.Names, " ")] = f.Help
	}
	want := map[string]string{"--format": "usage format", "--user": "from a rule", "--fish": "from fish", "-a --all": "show all"}
	if !slices.Equal(sortedKeys(help), sortedKeys(want)) || help["--format"] != "usage format" {
		t.Fatalf("merged flags %q", help)
	}
	if len(spec.Commands) != 1 || spec.Commands[0].Name != "run" || spec.Commands[0].Flags[0].Names[0] != "--fast" {
		t.Fatalf("subcommands %+v", spec.Commands)
	}
	if alias, _, _ := c.lookup("alias"); len(alias.Flags) != 2 {
		t.Fatalf("redirected page %+v", alias)
	}
	if missing, found, _ := c.lookup("missing"); !found || missing.Name != "missing" || len(missing.Flags) != 0 {
		t.Fatalf("missing command %+v", missing)
	}
	page("demo.1.gz", ".SH OPTIONS\n.TP\n\\fB\\-\\-changed\\fR\nchanged\n")
	if status := c.build([]string{"demo"}, &log, time.Minute); status != 0 {
		t.Fatalf("rebuild status %d: %s", status, log.String())
	}
	if spec, _, _ := c.lookup("demo"); slices.IndexFunc(spec.Flags, func(f usage.Flag) bool { return f.Names[0] == "--changed" }) < 0 {
		t.Fatalf("changed page not rebuilt: %+v", spec.Flags)
	}
}

func TestCompletionRefreshAll(t *testing.T) {
	binary := nativeCLIBinary(t)
	dir := t.TempDir()
	c := specStore(filepath.Join(dir, "cache"))
	c.manDirs = []string{filepath.Join(dir, "man")}
	c.userRules, c.executable = filepath.Join(dir, "config", "completions.km"), binary
	bin := filepath.Join(dir, "bin")
	for _, name := range []string{"subonly", "useronly", "pattern-only", "unrelated", "none"} {
		writeReplTool(t, bin, name, "exit 0")
	}
	specWrite(t, filepath.Join(dir, "man", "man1", "subonly-run.1"), ".SH SYNOPSIS\n.B subonly run\n")
	specWrite(t, filepath.Join(dir, "man", "man1", "unrelated-tool.1"), ".SH SYNOPSIS\n.B unrelated-tool\n")
	specWrite(t, c.userRules, "rule ./user/useronly.usage.kdl = \"flag \\\"--user\\\"\\n\"\n"+
		"rule ./user/{name:pattern-*}.usage.kdl ; {shell: \"/bin/sh\"}\n\tprintf 'flag \"--pattern\"\\n' > '@{outputs.0}'\n")
	var out, errOut bytes.Buffer
	ctx := completionContext{Path: bin}
	if status := c.command([]string{"--refresh", "--all"}, ctx, &out, &errOut); status != 0 || !strings.Contains(out.String(), "refreshed 3") {
		t.Fatalf("refresh all: %d %s %s", status, out.String(), errOut.String())
	}
	if spec, _, cached := c.lookup("subonly"); !cached || len(spec.Commands) != 1 || spec.Commands[0].Name != "run" {
		t.Fatalf("subcommand-only spec: %+v cached=%v", spec, cached)
	}
	for name, flag := range map[string]string{"useronly": "--user", "pattern-only": "--pattern"} {
		if spec, _, cached := c.lookup(name); !cached || len(spec.Flags) != 1 || spec.Flags[0].Names[0] != flag {
			t.Fatalf("%s spec: %+v cached=%v", name, spec, cached)
		}
	}
	for _, name := range []string{"unrelated", "none"} {
		if _, found, _ := c.lookup(name); found {
			t.Errorf("built unrelated spec %s", name)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func specWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// liveFixture installs bin/kashtool as a shell script answering a live
// protocol with reply, recording its argv and COMPLETE in a log.
func liveFixture(t *testing.T, reply string) (completionContext, string, string) {
	t.Helper()
	ctx := specFixture(t)
	executable := filepath.Join(ctx.Path, "kashtool")
	log := filepath.Join(t.TempDir(), "argv")
	specWrite(t, executable, "#!/bin/sh\nfor a in \"$@\"; do printf '[%s]' \"$a\"; done > "+log+"\nprintf '|%s' \"$COMPLETE\" >> "+log+"\n"+reply)
	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatal(err)
	}
	return ctx, executable, log
}

func liveTexts(ctx completionContext, line string) []string {
	_, candidates := completionsAt(line, len(line), ctx)
	var texts []string
	for _, c := range candidates {
		texts = append(texts, c.Text)
	}
	return texts
}

func TestLiveCobraCompletion(t *testing.T) {
	ctx, executable, log := liveFixture(t, "printf 'alpha\\tfirst one\\nbeta\\n--gamma\\n:4\\n'\n")
	// Unregistered: the spec answers and the executable never runs.
	if got := liveTexts(ctx, "kashtool "); !slices.Equal(got, []string{"serve"}) {
		t.Fatalf("unregistered: %q", got)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("an unregistered executable ran")
	}
	var out, errOut bytes.Buffer
	if status := ctx.Specs.command([]string{"--live", "cobra", executable}, ctx, &out, &errOut); status != 0 {
		t.Fatalf("register: %d %q", status, errOut.String())
	}
	if got := liveTexts(ctx, `kashtool serve my\ dir `); !slices.Equal(got, []string{"--gamma", "alpha", "beta"}) {
		t.Fatalf("cobra candidates: %q", got)
	}
	if argv, _ := os.ReadFile(log); string(argv) != "[__complete][serve][my dir][]|" {
		t.Fatalf("cobra argv: %q", argv)
	}
	for _, tc := range []struct{ line, argv string }{
		{`kashtool 'a*' "\t\n\r\$" \* \~/literal café `, "[__complete][a*][\t\n\r$][*][~/literal][café][]|"},
		{`kash\tool abc\\ `, "[__complete][abc\\][]|"},
		{"kashtool café\\\r\nbar ", "[__complete][cafébar][]|"},
		{`kashtool \*`, "[__complete][*]|"},
		{`"kashtool" '[a-z]' `, "[__complete][[a-z]][]|"},
		{`alias-tool `, "[__complete][$x][*][]|"},
	} {
		ctx.Aliases["alias-tool"] = []string{"kashtool", "$x", "*"}
		os.Remove(log)
		liveTexts(ctx, tc.line)
		if argv, _ := os.ReadFile(log); string(argv) != tc.argv {
			t.Errorf("%q: cobra argv %q, want %q", tc.line, argv, tc.argv)
		}
	}
	_, candidates := completionsAt("kashtool a", 10, ctx)
	if len(candidates) != 1 || candidates[0].Description != "first one" {
		t.Fatalf("description: %+v", candidates)
	}
	// Nonliteral words never reach the executable.
	for _, line := range []string{`kashtool $x `, `kashtool "$x" `, `kashtool (str x) `, `kashtool ${str "a b"} `, `kashtool [a-z] `, `kashtool ~/x `, `kashtool "\q" `, `kashtool foo\`, `kashtool # kashtool `} {
		os.Remove(log)
		liveTexts(ctx, line)
		if _, err := os.Stat(log); err == nil {
			t.Errorf("nonliteral or invalid input ran the executable: %q", line)
		}
	}
	out.Reset()
	ctx.Specs.command([]string{"--live"}, ctx, &out, &errOut)
	if out.String() != "complete --live cobra "+executable+"\n" {
		t.Fatalf("list: %q", out.String())
	}
	for _, bad := range [][]string{{"--live", "cobra", "relative"}, {"--live", "fish", executable}, {"--live", "cobra"}} {
		if status := ctx.Specs.command(bad, ctx, &out, &errOut); status != 2 {
			t.Errorf("%q: status %d", bad, status)
		}
	}
}

func TestLiveCobraFallsBack(t *testing.T) {
	for name, reply := range map[string]string{
		"error directive": "printf 'alpha\\n:1\\n'\n",
		"no directive":    "printf 'alpha\\n'\n",
		"failure":         "exit 3\n",
		"timeout":         "/bin/sleep 5\n",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, executable, _ := liveFixture(t, reply)
			ctx.Specs.live[executable] = "cobra"
			start := time.Now()
			if got := liveTexts(ctx, "kashtool "); !slices.Equal(got, []string{"serve"}) {
				t.Fatalf("got %q, want the spec's candidates", got)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("a slow executable delayed completion")
			}
		})
	}
	ctx, executable, _ := liveFixture(t, "printf ':16\\n'\n")
	ctx.Specs.live[executable] = "cobra"
	if got := liveTexts(ctx, "kashtool sr"); !slices.Equal(got, []string{"src/"}) {
		t.Fatalf("directory directive: %q", got)
	}
}

func TestLiveClapCompletion(t *testing.T) {
	ctx, executable, log := liveFixture(t, "printf 'one\\tthe first\\ntwo\\n'\n")
	ctx.Specs.live[executable] = "clap"
	if got := liveTexts(ctx, "kashtool --out t"); !slices.Equal(got, []string{"two"}) {
		t.Fatalf("clap candidates: %q", got)
	}
	if argv, _ := os.ReadFile(log); string(argv) != "[--][kashtool][--out][t]|fish" {
		t.Fatalf("clap argv: %q", argv)
	}
}
