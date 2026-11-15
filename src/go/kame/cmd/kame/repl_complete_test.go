package main

import (
	"os"
	"path/filepath"
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
		Callables: []string{"shout", "show-all", "uppercase"},
		Values:    []string{"greeting", "group"},
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

func TestCompleteLine(t *testing.T) {
	ctx := completionFixture(t)
	for _, c := range []struct {
		name, line, want string
		list             []string
	}{
		{name: "unique binding", line: "echo $gree", want: "echo $greeting "},
		{name: "binding prefix", line: "echo $g", want: "echo $gr"},
		{name: "binding list", line: "echo $gr", want: "echo $gr", list: []string{"$greeting", "$group"}},
		{name: "function", line: "(upp", want: "(uppercase "},
		{name: "function list", line: "(sho", want: "(sho", list: []string{"shout", "show-all"}},
		{name: "functions are not commands", line: "shou", want: "shou"},
		{name: "session command", line: "expo", want: "export "},
		{name: "keyword", line: "whi", want: "while "},
		{name: "executable", line: "kashto", want: "kashtool "},
		{name: "executable list", line: "kasht", want: "kasht", list: []string{"kashtest", "kashtool"}},
		{name: "non-executable skipped", line: "kashd", want: "kashd"},
		{name: "after pipe", line: "cat a | kashto", want: "cat a | kashtool "},
		{name: "after if", line: "if kashto", want: "if kashtool "},
		{name: "file", line: "cat rea", want: "cat readme.md "},
		{name: "file list", line: "cat re", want: "cat re", list: []string{"readme.md", "release.txt"}},
		{name: "directory", line: "cat sr", want: "cat src/"},
		{name: "nested", line: "cat src/ma", want: "cat src/main.go "},
		{name: "escaped name", line: "cat a", want: "cat a\\(b\\).txt "},
		{name: "escaped directory", line: "cat sp", want: "cat spaced\\ dir/"},
		{name: "typed escape", line: "cat spaced\\ d", want: "cat spaced\\ dir/"},
		{name: "hidden needs dot", line: "cat .h", want: "cat .hidden "},
		{name: "cd prefix", line: "cd ", want: "cd s"},
		{name: "cd directories", line: "cd s", want: "cd s", list: []string{"spaced dir/", "src/"}},
		{name: "home", line: "cat ~/do", want: "cat ~/docs/"},
		{name: "path in command position", line: "./sr", want: "./src/"},
		{name: "no candidate", line: "cat zz", want: "cat zz"},
		{name: "redirect target", line: "echo x > ./rel", want: "echo x > ./release.txt "},
	} {
		got, pos, list := completeLine(c.line, len(c.line), ctx)
		if got != c.want || pos != len(c.want) || !slices.Equal(list, c.list) {
			t.Errorf("%s: complete %q = %q@%d %q, want %q %q", c.name, c.line, got, pos, list, c.want, c.list)
		}
	}
	// The cursor may sit inside the line; text after it is kept.
	if got, pos, _ := completeLine("(upp x)", 4, ctx); got != "(uppercase  x)" || pos != 11 {
		t.Errorf("mid-line completion = %q@%d", got, pos)
	}
}

func TestFormatCompletionList(t *testing.T) {
	if got := formatCompletionList([]string{"aa", "bb", "cc"}, 80); got != "aa  bb  cc\n" {
		t.Errorf("one row = %q", got)
	}
	if got := formatCompletionList([]string{"aa", "bb", "cc"}, 8); got != "aa  cc\nbb\n" {
		t.Errorf("columns = %q", got)
	}
	many := make([]string, 120)
	for i := range many {
		many[i] = "x"
	}
	if got := formatCompletionList(many, 4); !strings.HasSuffix(got, "... and 20 more\n") || strings.Count(got, "x") != 100 {
		t.Errorf("limited list = %q", got)
	}
}

func TestKameString(t *testing.T) {
	if got, want := kameString("a\"b\\c@{d}\n"), `"a\"b\\c\@\{d}\n"`; got != want {
		t.Errorf("kameString = %s, want %s", got, want)
	}
}
