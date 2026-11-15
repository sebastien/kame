package scriptcheck

import (
	"kame/internal/testing"
	"kame/lang/script"
	"strings"
)

var kashTokenNames = map[script.KashTokenKind]string{
	script.KashTokenText: "text", script.KashTokenCommand: "cmd", script.KashTokenKeyword: "kw",
	script.KashTokenDefinition: "def", script.KashTokenOption: "opt", script.KashTokenString: "str",
	script.KashTokenVariable: "var", script.KashTokenOperator: "op", script.KashTokenComment: "comment",
	script.KashTokenFunction: "fn", script.KashTokenConstant: "const", script.KashTokenNumber: "num",
	script.KashTokenPunct: "punct",
}

func describeKashTokens(text string) string {
	var parts []string
	for _, token := range script.KashTokens(text) {
		parts = append(parts, kashTokenNames[token.Kind]+":"+text[token.Start:token.End])
	}
	return strings.Join(parts, " ")
}

func TestKashTokensClassifyInteractiveText(t *testing.T) {
	cases := []struct{ text, want string }{
		{"echo hello -n --x=1", "cmd:echo text:hello opt:-n opt:--x=1"},
		{"cat a | grep b && ls; pwd", "cmd:cat text:a op:| cmd:grep text:b op:&& cmd:ls op:; cmd:pwd"},
		{"echo 'a $b' \"c $d e\"", "cmd:echo str:'a $b' str:\"c  var:$d str: e\""},
		{"echo $x ${y}.c $...zs", "cmd:echo var:$x var:${y} text:.c var:$...zs"},
		{"x = $(git rev-parse HEAD)", "def:x op:= op:$( cmd:git text:rev-parse text:HEAD op:)"},
		{"let n = (count [1 2])", "kw:let def:n op:= punct:( fn:count punct:[ num:1 num:2 punct:] punct:)"},
		{"(shout \"bob\" :loud)", "punct:( fn:shout str:\"bob\" const::loud punct:)"},
		{"(label name) = (uppercase name)", "punct:( fn:label text:name punct:) op:= punct:( fn:uppercase text:name punct:)"},
		{"def greet name\n\techo $name", "kw:def def:greet def:name cmd:echo var:$name"},
		{"if test -e ./f\n\techo yes", "kw:if cmd:test opt:-e text:./f cmd:echo text:yes"},
		{"for f in $files\n\techo $f", "kw:for def:f kw:in var:$files cmd:echo var:$f"},
		{"make 2> ./err.log > out", "cmd:make op:2> text:./err.log op:> text:out"},
		{":cwd ./src :tty :true make", "const::cwd text:./src const::tty text::true cmd:make"},
		{"echo a # note", "cmd:echo text:a comment:# note"},
		{"echo --path=(dir) x\\ y", "cmd:echo opt:--path= punct:( fn:dir punct:) text:x\\ y"},
		{"sleep 1 &", "cmd:sleep text:1 op:&"},
	}
	for _, c := range cases {
		if got := describeKashTokens(c.text); got != c.want {
			t.Error("tokens of " + c.text + "\n got: " + got + "\nwant: " + c.want)
		}
	}
}

// Partial input never fails: unclosed constructs extend to the end, and
// every byte is covered at most once in order.
func TestKashTokensTolerateUnclosedInput(t *testing.T) {
	for _, text := range []string{"echo \"open $x", "echo 'open", "echo (map", "x = $(ls", "echo ${na", "(f [1 \"a", "echo )", "def", "let", "for", "echo \\", "a=(", "2>"} {
		last := 0
		for _, token := range script.KashTokens(text) {
			if token.Start < last || token.End > len(text) || token.End <= token.Start {
				t.Error("bad token range in " + text + ": " + describeKashTokens(text))
				break
			}
			last = token.End
		}
	}
	if got := describeKashTokens("echo \"open $x"); got != "cmd:echo str:\"open  var:$x" {
		t.Error("unclosed double quote: " + got)
	}
}
