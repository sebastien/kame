package cli_test

import (
	"kame/cli"
	"solod.dev/so/testing"
)

func TestCacheCommandParsesActionsAndDirectory(t *testing.T) {
	inv := cli.Parse("cache", []string{"list", "-C", "work"})
	if !inv.OK || inv.Name != "cache" || inv.Directory != "work" || len(inv.Args) != 1 || inv.Args[0] != "list" {
		t.Error("cache list invocation did not retain its action and directory")
	}
	inv.Free()
	inv = cli.Parse("cache", []string{"clean", "--directory=work"})
	if !inv.OK || inv.Directory != "work" || inv.Args[0] != "clean" {
		t.Error("cache clean long directory option was not parsed")
	}
	inv.Free()
}

func TestCacheCommandRejectsMissingOrExtraArguments(t *testing.T) {
	invalid := [][]string{nil, {"show"}, {"list", "extra"}, {"clean", "-C"}}
	for i := range invalid {
		inv := cli.Parse("cache", invalid[i])
		if inv.OK || inv.Error.Code == "" {
			t.Error("invalid cache command was accepted")
		}
		inv.Free()
	}
}
