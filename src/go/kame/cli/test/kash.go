package cli_test

import (
	"kame/cli"
	"solod.dev/so/mem"
	"solod.dev/so/slices"
	"solod.dev/so/testing"
)

type kashInterpreterCase struct {
	Name string
	Args []string
	Want []string
}

func TestKashInterpreterArgs(t *testing.T) {
	cases := []kashInterpreterCase{
		{"default stdin", []string{}, []string{"--lang=kash", "-"}},
		{"file and literal tail", []string{"script.kash", "--help", "a=b"}, []string{"--lang=kash", "script.kash", "--", "--help", "a=b"}},
		{"explicit stdin", []string{"-", "arg"}, []string{"--lang=kash", "-", "--", "arg"}},
		{"short file", []string{"-f", "script.kash", "-n"}, []string{"--lang=kash", "-f", "script.kash", "--", "-n"}},
		{"long file", []string{"--file", "script.kash"}, []string{"--lang=kash", "--file", "script.kash", "--"}},
		{"short command", []string{"-c", "echo hello", "arg"}, []string{"--lang=kash", "-c", "echo hello", "--", "arg"}},
		{"long command", []string{"--command", ""}, []string{"--lang=kash", "--command", "", "--"}},
		{"attached file", []string{"--file=script.kash", "arg"}, []string{"--lang=kash", "--file=script.kash", "--", "arg"}},
		{"attached command", []string{"--command=echo hello", "arg"}, []string{"--lang=kash", "--command=echo hello", "--", "arg"}},
		{"language overrides", []string{"-l", "km", "--lang", "expr", "--lang=kmk", "script.kash"}, []string{"--lang=kash", "script.kash", "--"}},
		{"options and defines", []string{"-n", "-C", "work", "NAME=value", "script.kash"}, []string{"-n", "-C", "work", "NAME=value", "--lang=kash", "script.kash", "--"}},
		{"missing language", []string{"--lang"}, []string{"--lang"}},
		{"missing source", []string{"-f"}, []string{"--lang=kash", "-f"}},
		{"missing option value", []string{"-C"}, []string{"-C"}},
	}
	for i := range cases {
		got := cli.KashInterpreterArgs(cases[i].Args)
		if len(got) != len(cases[i].Want) {
			t.Error(cases[i].Name + ": argument count differs")
		} else {
			for j := range got {
				if got[j] != cases[i].Want[j] {
					t.Error(cases[i].Name + ": argument differs")
				}
			}
		}
		slices.Free(mem.System, got)
	}
}
