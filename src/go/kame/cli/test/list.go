package cli_test

import (
	"kame/cli"
	"solod.dev/so/testing"
)

func TestListQueryGrammar(t *testing.T) {
	arguments := [][]string{
		{}, {"biuld"}, {"--json", "-C", "project", "-f", "Build.kmk", "--set", "mode=:true", "biuld"},
		{"--", "--help"}, {"name=value"},
	}
	for i := range arguments {
		inv := cli.Parse("list", arguments[i])
		if !inv.OK || inv.Name != "list" || len(inv.Targets) > 1 || len(inv.Parameters) != 0 { t.Error("valid listing or literal query rejected") }
		inv.Free()
	}
	invalid := [][]string{{"first", "second"}, {"--watch"}, {"--dry-run"}, {"--depth", "1"}, {"--jobs=2"}}
	for i := range invalid {
		inv := cli.Parse("list", invalid[i])
		if inv.OK { t.Error("invalid list arguments accepted") }
		inv.Free()
	}
}
