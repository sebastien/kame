package clicheck

import (
	"kame/cli"
	"kame/internal/mem"
	"kame/internal/testing"
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
		{"language overrides", []string{"--lang", "expr", "--lang=kmk", "script.kash"}, []string{"--lang=kash", "script.kash", "--"}},
		{"login", []string{"-l"}, []string{"--login", "--lang=kash", "-"}},
		{"login command", []string{"-lc", "echo hello"}, []string{"--login", "--lang=kash", "-c", "echo hello", "--"}},
		{"interactive login", []string{"-il"}, []string{"--login", "--interactive", "--lang=kash"}},
		{"options and defines", []string{"-n", "-C", "work", "NAME=value", "script.kash"}, []string{"-n", "-C", "work", "NAME=value", "--lang=kash", "script.kash", "--"}},
		{"missing language", []string{"--lang"}, []string{"--lang"}},
		{"missing source", []string{"-f"}, []string{"--lang=kash", "-f"}},
		{"missing option value", []string{"-C"}, []string{"-C"}},
		{"interactive", []string{"-i"}, []string{"-i", "--lang=kash"}},
		{"interactive after preload", []string{"--interactive", "lib.kash", "arg"}, []string{"--interactive", "--lang=kash", "lib.kash", "--", "arg"}},
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
		mem.FreeSlice(nil, got)
	}
}
