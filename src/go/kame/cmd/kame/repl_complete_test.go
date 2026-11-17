package main

import (
	"kame/lang/eval"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func completionFixture(t *testing.T) completionContext {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	work := filepath.Join(root, "work")
	for _, dir := range []string{bin, work, filepath.Join(work, "src"), filepath.Join(work, "spaced dir"), filepath.Join(root, "home", "docs")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, mode := range map[string]os.FileMode{
		filepath.Join(bin, "kashtool"):                   0o755,
		filepath.Join(bin, "kashtest"):                   0o755,
		filepath.Join(bin, "kashdata"):                   0o644,
		filepath.Join(work, "readme.md"):                 0o644,
		filepath.Join(work, "release.txt"):               0o644,
		filepath.Join(work, ".hidden"):                   0o644,
		filepath.Join(work, "src", "main.go"):            0o644,
		filepath.Join(work, "a(b).txt"):                  0o644,
		filepath.Join(root, "home", "docs", "notes.txt"): 0o644,
	} {
		if err := os.WriteFile(name, nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	return completionContext{
		Directory: work, Home: filepath.Join(root, "home"), Path: bin,
		Names: []eval.InteractiveName{
			{Name: "shout", Callable: true, Detail: "function name"},
			{Name: "show-all", Callable: true, Detail: "function"},
			{Name: "uppercase", Callable: true, Detail: "operation, 1 args"},
			{Name: "greeting", Detail: "let"},
			{Name: "group", Detail: "lazy"},
		},
	}
}

func TestCompletionWordStart(t *testing.T) {
	for _, c := range []struct {
		line string
		want string
	}{
		{"echo foo", "foo"},
		{"(upper", "upper"},
		{"cat a|gr", "gr"},
		{"echo spaced\\ di", "spaced\\ di"},
		{"x = $(ec", "ec"},
		{"--out=./re", "./re"},
		{"def f\n\tec", "ec"},
		{"", ""},
	} {
		if got := c.line[completionWordStart(c.line, len(c.line)):]; got != c.want {
			t.Errorf("word of %q = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestCompletionCommandPosition(t *testing.T) {
	for text, want := range map[string]bool{
		"": true, "cat a |": true, "echo a;": true, "sleep 1 &": true, "x = $(": true,
		"if": true, "if test -e a && while": true, "echo if": false, "echo": false, "echo a >": false,
	} {
		if got := completionCommandPosition(text); got != want {
			t.Errorf("command position after %q = %v, want %v", text, got, want)
		}
	}
}

func TestCompletionsAt(t *testing.T) {
	ctx := completionFixture(t)
	for _, c := range []struct {
		name, line string
		want       []string // Text|Tag|Description
	}{
		{name: "bindings", line: "echo $gr", want: []string{"$greeting|bindings|let", "$group|bindings|lazy"}},
		{name: "functions", line: "(sho", want: []string{"shout|functions|function name", "show-all|functions|function"}},
		{name: "operation", line: "(upp", want: []string{"uppercase|functions|operation, 1 args"}},
		{name: "functions are not commands", line: "shou"},
		{name: "session command", line: "expo", want: []string{"export|session commands|set child environment variables"}},
		{name: "session commands only lead the line", line: "echo a; expo"},
		{name: "keyword", line: "whi", want: []string{"while|keywords|"}},
		{name: "executables", line: "kasht", want: []string{"kashtest|commands|" + ctx.Path, "kashtool|commands|" + ctx.Path}},
		{name: "non-executable skipped", line: "kashd"},
		{name: "after pipe", line: "cat a | kashto", want: []string{"kashtool|commands|" + ctx.Path}},
		{name: "continuation line", line: "def f\n\tkashto", want: []string{"kashtool|commands|" + ctx.Path}},
		{name: "files", line: "cat re", want: []string{"readme.md|files|", "release.txt|files|"}},
		{name: "directory", line: "cat sr", want: []string{"src/|files|"}},
		{name: "nested", line: "cat src/ma", want: []string{"src/main.go|files|"}},
		{name: "escaped name", line: "cat a", want: []string{"a\\(b\\).txt|files|"}},
		{name: "escaped directory", line: "cat sp", want: []string{"spaced\\ dir/|files|"}},
		{name: "typed escape", line: "cat spaced\\ d", want: []string{"spaced\\ dir/|files|"}},
		{name: "hidden needs dot", line: "cat .h", want: []string{".hidden|files|"}},
		{name: "cd directories", line: "cd ", want: []string{"spaced\\ dir/|files|", "src/|files|"}},
		{name: "home", line: "cat ~/do", want: []string{"~/docs/|files|"}},
		{name: "path in command position", line: "./sr", want: []string{"./src/|files|"}},
		{name: "redirect target", line: "echo x > ./rel", want: []string{"./release.txt|files|"}},
	} {
		_, candidates := completionsAt(c.line, len(c.line), ctx)
		var got []string
		for _, candidate := range candidates {
			got = append(got, candidate.Text+"|"+candidate.Tag+"|"+candidate.Description)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: candidates of %q = %q, want %q", c.name, c.line, got, c.want)
		}
	}
}

func TestReplEntryComplete(t *testing.T) {
	for text, want := range map[string]bool{
		"":                            true,
		"echo hi":                     true,
		"echo (map":                   false,
		"echo (map\n  uppercase [])":  true,
		"def f":                       false,
		"def f\n\techo hi":            false,
		"def f\n\techo hi\n":          true,
		"if true\n\tif false\n\t\tls": false,
		"echo a\necho b":              true,
		"echo a\n&":                   true,
		"def f\n\techo hi\n  &  ":     true,
		"\n&":                         true, // submitted; readEntry ignores it
	} {
		if got := replEntryComplete(text); got != want {
			t.Errorf("entry complete %q = %v, want %v", text, got, want)
		}
	}
}

func TestReplHighlight(t *testing.T) {
	ctx := completionFixture(t)
	e := &editorReader{snapshot: ctx}
	highlight := func(text string) string { return e.highlight([]rune(text)) }
	if got := highlight("kashtool a | nosuchtool 'q'"); got != "\x1b[1;32mkashtool\x1b[0m a \x1b[1m|\x1b[0m \x1b[1;31mnosuchtool\x1b[0m \x1b[33m'q'\x1b[0m" {
		t.Errorf("highlight = %q", got)
	}
	got := highlight("cd ./src; ./missing; $tool x")
	for _, want := range []string{"\x1b[1;32mcd\x1b[0m", "\x1b[1;31m./missing\x1b[0m", "\x1b[1;36m$tool\x1b[0m"} {
		if !strings.Contains(got, want) {
			t.Errorf("highlight %q lacks %q", got, want)
		}
	}
	// Highlighting never changes the visible text.
	text := "def f x\n\techo \"$x (y\" | tee ./o 2>&1"
	if plain := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(highlight(text), ""); plain != text {
		t.Errorf("highlight changed text: %q", plain)
	}
}
