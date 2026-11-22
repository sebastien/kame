package usage

import (
	"bytes"
	"compress/gzip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const reportKDL = `
// A comment
bin "report"
about "Make reports"
flag "-f --format <format>" help="Output format" {
    choices "text" "json" "csv"
}
flag "--config <file>" help="Configuration file" {
    complete type="file"
}
flag "-v --verbose" global=#true
flag "--secret" hide=#true
/- flag "--commented-out"
complete "dir" type="dir"
cmd "publish" help="Publish a report" {
    alias "pub"
    flag "--dry-run"
    arg "<target>" { choices "web" "mail"; }
    arg "<dir>"
}
cmd "list" help="List reports" {
    arg "[path]" var=#true
}
`

func TestParseKDL(t *testing.T) {
	spec, err := ParseKDL(reportKDL)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "report" || spec.Help != "Make reports" || len(spec.Flags) != 3 || len(spec.Commands) != 2 {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	format := spec.Flags[0]
	if !slices.Equal(format.Names, []string{"-f", "--format"}) || format.Value != "format" || !slices.Equal(format.Choices, []string{"text", "json", "csv"}) {
		t.Fatalf("format flag: %+v", format)
	}
	publish := spec.Commands[0]
	if !slices.Equal(publish.Aliases, []string{"pub"}) || len(publish.Args) != 2 || publish.Args[1].Complete != "dir" {
		t.Fatalf("publish: %+v", publish)
	}
	if !spec.Commands[1].Args[0].Variadic || !spec.Commands[1].Args[0].Optional {
		t.Fatalf("list args: %+v", spec.Commands[1].Args)
	}
	rendered := RenderKDL(spec)
	again, err := ParseKDL(rendered)
	if err != nil || !reflect.DeepEqual(again, spec) {
		t.Fatalf("round trip changed the spec (%v):\n%s", err, rendered)
	}
}

func TestParseKDLSyntax(t *testing.T) {
	spec, err := ParseKDL("bin r#\"a\"b\"#\nflag \"-x\" \\\n  help=\"multi\\nline \\u{41}\"\nflag (t)\"--y\" help=#\"raw \\n\"#; arg <FILE>\n/* block /* nested */ */\n")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != `a"b` || spec.Flags[0].Help != "multi\nline A" || spec.Flags[1].Help != `raw \n` || spec.Args[0].Name != "FILE" {
		t.Fatalf("%+v", spec)
	}
	for _, bad := range []string{`flag "x`, "cmd \"a\" {\n", "}", "/* open"} {
		if _, err := ParseKDL(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestParseKDLTruncatedPrefixes(t *testing.T) {
	for _, text := range []string{
		"/-", "bin x /-", "bin x /- // comment", "cmd x {\n/-\n",
		"(t)", "bin x (t)", "bin x help=(t)", "bin x /- (t)",
		"/- /* open", "bin x /- /* open",
	} {
		t.Run(text, func(t *testing.T) {
			if _, err := ParseKDL(text); err == nil || !strings.HasPrefix(err.Error(), "line ") {
				t.Fatalf("expected a line-numbered parse error, got %v", err)
			}
		})
	}
}

func complete(t *testing.T, spec Spec, line string) ([]string, Files) {
	t.Helper()
	words := strings.Split(line, " ")
	candidates, files := spec.Complete(words[:len(words)-1], words[len(words)-1])
	var texts []string
	for _, c := range candidates {
		texts = append(texts, c.Text)
	}
	return texts, files
}

func TestComplete(t *testing.T) {
	spec, err := ParseKDL(reportKDL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line  string
		want  []string
		files Files
	}{
		{"", []string{"publish", "list"}, NoFiles},
		{"p", []string{"publish"}, NoFiles},
		{"--", []string{"--format", "--config", "--verbose"}, NoFiles},
		{"-", []string{"-f", "--format", "--config", "-v", "--verbose"}, NoFiles},
		{"--format j", []string{"json"}, NoFiles},
		{"-f ", []string{"text", "json", "csv"}, NoFiles},
		{"-vf c", []string{"csv"}, NoFiles},
		{"-fjson ", []string{"publish", "list"}, NoFiles},
		{"--format=c", []string{"--format=csv"}, NoFiles},
		{"--config ", nil, AnyFiles},
		{"publish ", []string{"web", "mail"}, NoFiles},
		{"pub --dry-run w", []string{"web"}, NoFiles},
		{"publish web ", nil, DirFiles},
		{"publish web here ", nil, AnyFiles},
		{"publish --", []string{"--dry-run", "--verbose"}, NoFiles},
		{"list a b ", nil, AnyFiles},
		{"-- -", nil, NoFiles},
	} {
		got, files := complete(t, spec, tc.line)
		if !slices.Equal(got, tc.want) || files != tc.files {
			t.Errorf("%q: got %q %v, want %q %v", tc.line, got, files, tc.want, tc.files)
		}
	}
}

func TestMerge(t *testing.T) {
	high := Spec{Name: "x", Flags: []Flag{{Names: []string{"--format"}, Value: "f", Choices: []string{"a"}}}, Commands: []Spec{{Name: "run"}}}
	low := Spec{Name: "x", Help: "the x tool", Flags: []Flag{
		{Names: []string{"-f", "--format"}, Value: "FILE", Help: "format"},
		{Names: []string{"-q"}, Help: "quiet"},
	}, Args: []Arg{{Name: "file"}}, Commands: []Spec{{Name: "run", Help: "run it"}, {Name: "stop"}}}
	merged := Merge(high, low)
	want := Spec{Name: "x", Help: "the x tool", Flags: []Flag{
		{Names: []string{"--format", "-f"}, Value: "f", Choices: []string{"a"}, Help: "format"},
		{Names: []string{"-q"}, Help: "quiet"},
	}, Args: []Arg{{Name: "file"}}, Commands: []Spec{{Name: "run", Help: "run it"}, {Name: "stop"}}}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("got %+v\nwant %+v", merged, want)
	}
	if len(high.Flags[0].Names) != 1 {
		t.Fatal("merge changed its input")
	}
}

func TestMergePositionalArgs(t *testing.T) {
	first := Arg{Name: "first", Choices: []string{"one"}}
	rest := Arg{Name: "rest", Variadic: true, Choices: []string{"more"}}
	low := Spec{Args: []Arg{{Name: "ignored", Optional: true}, rest}}
	for _, tc := range []struct {
		name string
		high Arg
		want []Arg
	}{
		{"fixed", first, []Arg{first, rest}},
		{"variadic", rest, []Arg{rest}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			high := Spec{Args: []Arg{tc.high}}
			lower := Spec{Args: []Arg{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
			addSubcommand := func(s Spec) Spec {
				s.Commands = []Spec{{Name: "run", Args: s.Args}}
				return s
			}
			got := Merge(addSubcommand(high), addSubcommand(low), addSubcommand(lower))
			if !reflect.DeepEqual(got.Args, tc.want) || !reflect.DeepEqual(got.Commands[0].Args, tc.want) {
				t.Fatalf("got root args %v and subcommand args %v, want %v for both", got.Args, got.Commands[0].Args, tc.want)
			}
		})
	}
}

const manPage = `.\" A GNU-style page
.TH DEMO 1
.SH NAME
demo \- show a demonstration
.SH SYNOPSIS
.B demo run
[\fIOPTION\fR]... \fIFILE\fR
.SH OPTIONS
.TP
\fB\-a\fR, \fB\-\-all\fR
do not ignore entries starting with .
Second sentence.
.TP
.B \-\-block\-size\fR=\fISIZE
scale sizes by SIZE. More text.
.TP
\X'tty: link https://example.com'\fB\-\-color\fR[=\fIWHEN\fR]\X'tty: link'
colorize the output
.IP "\-o file" 4
write to file
.HP
\fB\-n\fR, \fB\-\-quiet\fR
.IP
suppress output
.PP
\fB\-v\fR, \fB\-\-verbose\fR
.RS 4
Be verbose\&. Very.
.RE
.sp
\fB\-\-header\fP
.INDENT 0.0
Generate headers.
.UNINDENT
.SH SEE ALSO
.TP
not an option
`

func TestParseMan(t *testing.T) {
	spec := ParseMan(manPage)
	want := []Flag{
		{Names: []string{"-a", "--all"}, Help: "do not ignore entries starting with ."},
		{Names: []string{"--block-size"}, Value: "SIZE", Help: "scale sizes by SIZE"},
		{Names: []string{"--color"}, Value: "WHEN", Optional: true, Help: "colorize the output"},
		{Names: []string{"-o"}, Value: "file", Help: "write to file"},
		{Names: []string{"-n", "--quiet"}, Help: "suppress output"},
		{Names: []string{"-v", "--verbose"}, Help: "Be verbose"},
		{Names: []string{"--header"}, Help: "Generate headers"},
	}
	if spec.Name != "demo run" || spec.Help != "show a demonstration" || !reflect.DeepEqual(spec.Flags, want) {
		t.Fatalf("got %q %q %+v", spec.Name, spec.Help, spec.Flags)
	}
}

const mdocPage = `.Dd $Mdocdate$
.Dt DEMO 1
.Sh NAME
.Nm demo
.Nd demonstrate mdoc
.Sh SYNOPSIS
.Nm
.Op Fl 4
.Sh DESCRIPTION
.Bl -tag -width Ds
.It Fl 4
Forces
.Nm
to use IPv4.
.It Fl B Ar bind_interface
Bind to the address of
.Ar bind_interface .
See
.Xr ssh_config 5 .
.It Xo
.Fl \-level Ns = Ns Ar n
.Xc
Set the level.
.It Fl v , Fl \-verbose
Verbose mode.
.El
`

func TestParseMdoc(t *testing.T) {
	spec := ParseMan(mdocPage)
	want := []Flag{
		{Names: []string{"-4"}, Help: "Forces demo to use IPv4"},
		{Names: []string{"-B"}, Value: "bind_interface", Help: "Bind to the address of bind_interface"},
		{Names: []string{"--level"}, Value: "n", Help: "Set the level"},
		{Names: []string{"-v", "--verbose"}, Help: "Verbose mode"},
	}
	if spec.Name != "demo" || spec.Help != "demonstrate mdoc" || !reflect.DeepEqual(spec.Flags, want) {
		t.Fatalf("got %q %+v", spec.Help, spec.Flags)
	}
}

func TestDecodeManAndRedirect(t *testing.T) {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	z.Write([]byte(".\\\" comment\n.so man1/other.1\n"))
	z.Close()
	text, err := DecodeMan(b.Bytes())
	if err != nil || ManRedirect(text) != "man1/other.1" || ManRedirect(manPage) != "" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err := DecodeMan([]byte{0x28, 0xb5, 0x2f, 0xfd}); err == nil {
		t.Fatal("zstd should be unsupported")
	}
}

const fishFile = `# Completions for demo
set -l commands start stop
complete -c demo -f
complete -c demo -n "not __fish_seen_subcommand_from $commands" -a "$commands"
complete -c demo -a "start stop" -d 'Subcommand'
complete -c demo -s h -l help -d 'Show help'
complete -c demo -l color -xa 'auto always never' -d "When to color"
complete -c demo -s o -l output -r -d 'Output file'
complete -c demo -l level -a '(seq 3)'
complete -c other -l nope
complete demo -o old
function __demo_helper
    complete -c demo -l hidden
end
complete -c demo -l piped | cat
`

func TestParseFish(t *testing.T) {
	spec, err := ParseFish("demo", fishFile)
	if err != nil {
		t.Fatal(err)
	}
	want := Spec{Name: "demo",
		Args: []Arg{{Name: "argument", Optional: true, Variadic: true, Choices: []string{"start", "stop"}, Complete: "none"}},
		Flags: []Flag{
			{Names: []string{"-h", "--help"}, Help: "Show help"},
			{Names: []string{"--color"}, Help: "When to color", Value: "value", Choices: []string{"auto", "always", "never"}},
			{Names: []string{"-o", "--output"}, Help: "Output file", Value: "value"},
			{Names: []string{"-old"}},
		},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("got %+v\nwant %+v", spec, want)
	}
	if _, err := ParseFish("demo", "complete -c demo -d 'open"); err == nil {
		t.Fatal("unclosed quote should fail")
	}
}
