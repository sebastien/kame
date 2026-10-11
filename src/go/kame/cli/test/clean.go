package cli_test

import (
	"kame/cli"
	"solod.dev/so/testing"
)

func TestCleanCommandGrammar(t *testing.T) {
	inv := cli.Parse("clean", []string{"--directory=work", "--dry-run", "--json"})
	if !inv.OK || inv.Directory != "work" || !inv.DryRun || !inv.JSON {
		t.Error("clean inspection options were lost")
	}
	inv.Free()
	inv = cli.Parse("clean", []string{"--keep", "--", "-literal", "./out"})
	if !inv.OK || inv.Command != "--keep" || len(inv.Args) != 2 {
		t.Error("literal protection paths were lost")
	}
	inv.Free()
	invalid := [][]string{{"--force"}, {"target"}, {"--keep"}, {"--release", "--keep", "./out"}, {"--keep", "./out", "--dry-run"}, {"-C"}}
	for i := range invalid {
		inv = cli.Parse("clean", invalid[i])
		if inv.OK {
			t.Error("invalid clean invocation was accepted")
		}
		inv.Free()
	}
}
